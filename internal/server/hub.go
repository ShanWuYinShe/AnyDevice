package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"any_device/internal/audio"
	"any_device/internal/mouse"

	"github.com/gorilla/websocket"
)

type OutMessage struct {
	isBinary bool
	data     []byte
	// queuedAt 音频消息入 hub 广播队列的时间：writePump 用它统计
	// 「服务端队列 → WebSocket 写完成」延迟（发送端延迟分项之一）。
	queuedAt time.Time
}

// InboundClientInfo represents public status of a connected client.
type InboundClientInfo struct {
	ID              string `json:"id"`
	RemoteAddr      string `json:"remote_addr"`
	RemoteIP        string `json:"remote_ip"`
	UserAgent       string `json:"user_agent"`
	DeviceType      string `json:"device_type"`
	ConnectedAt     string `json:"connected_at"`
	DurationSec     int64  `json:"duration_sec"`
	AudioEnabled    bool   `json:"audio_enabled"`
	MouseEnabled    bool   `json:"mouse_enabled"`
	KeyboardEnabled bool   `json:"keyboard_enabled"`
}

// Client represents a connected browser or receiver client.
type Client struct {
	id         string
	hub        *Hub
	conn       *websocket.Conn
	send       chan OutMessage
	remoteIP   string
	remoteAddr string
	userAgent  string
	deviceType string
	joinedAt   time.Time
	// 音频订阅按流拆分：系统声与麦克风各自独立，只有被映射的流才采集/下发。
	// 旧版对端只发 audio（并集），收到后两条都置位（兼容旧协议）。
	sysAudioEnabled bool
	micAudioEnabled bool
	audioSplit      bool // 是否已收到新协议的分流订阅（sys_audio/mic_audio）
	audioPCM        bool // 客户端音频能力：true=无 WebCodecs AudioDecoder，音频改发 float32 PCM
	mouseEnabled    bool
	keyboardEnabled bool
	isLocal         bool // 本机回环 UI 客户端（127.0.0.1 / 本机网卡地址发起）
	mu              sync.RWMutex
}

var clientSeq uint64

// ParseDeviceType detects friendly device name from User-Agent string.
func ParseDeviceType(ua string) string {
	if ua == "" {
		return "🌐 网页终端"
	}
	if strings.Contains(ua, "iPhone") {
		return "📱 苹果手机 (iOS)"
	}
	if strings.Contains(ua, "iPad") {
		return "📱 苹果平板 (iPadOS)"
	}
	if strings.Contains(ua, "Android") {
		return "📱 安卓设备 (Android)"
	}
	if strings.Contains(ua, "Windows") {
		return "💻 Windows 电脑"
	}
	if strings.Contains(ua, "Macintosh") || strings.Contains(ua, "Mac OS") {
		return "💻 Mac 电脑"
	}
	if strings.Contains(ua, "Linux") {
		return "💻 Linux 设备"
	}
	return "🌐 局域网终端"
}

// Hub maintains the set of active clients and broadcasts messages to them.
type Hub struct {
	clients           map[*Client]bool
	audioBroadcast    chan audioPacket
	audioStats        chan []byte
	mouseBroadcast    chan []byte
	keyboardBroadcast chan []byte
	register          chan *Client
	unregister        chan *Client
	updateSubs        chan struct{}
	mu                sync.RWMutex

	totalBytesSent uint64
	clientCount    int64
	// latSendUs：音频「广播队列 → WS 写完成」延迟（µs，EWMA）。
	latSendUs     atomic.Int64
	onSubsChanged func(sysAudioSubs, micAudioSubs, mouseSubs, keyboardSubs int)
	// onNodeIdentity 在对端通过握手上报自己的节点身份时触发
	// （remoteIP=连接源 IP，nodeID=对端节点 ID，nodeName=对端广播名），
	// 服务端据此把入站档案归并到正确的设备（跨 IP 场景按 IP 归并会失效）。
	onNodeIdentity func(remoteIP, nodeID, nodeName string)
}

// SetNodeIdentityHandler 注册对端节点身份回调（由 server 启动时注入）。
func (h *Hub) SetNodeIdentityHandler(fn func(remoteIP, nodeID, nodeName string)) {
	h.onNodeIdentity = fn
}

func NewHub(onSubsChanged func(sysAudioSubs, micAudioSubs, mouseSubs, keyboardSubs int)) *Hub {
	return &Hub{
		clients:           make(map[*Client]bool),
		audioBroadcast:    make(chan audioPacket, 128),
		audioStats:        make(chan []byte, 8),
		mouseBroadcast:    make(chan []byte, 64),
		keyboardBroadcast: make(chan []byte, 64),
		register:          make(chan *Client),
		unregister:        make(chan *Client),
		updateSubs:        make(chan struct{}, 8),
		onSubsChanged:     onSubsChanged,
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			atomic.StoreInt64(&h.clientCount, int64(len(h.clients)))
			h.mu.Unlock()
			h.notifySubscribers()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				atomic.StoreInt64(&h.clientCount, int64(len(h.clients)))
			}
			h.mu.Unlock()
			h.notifySubscribers()

		case <-h.updateSubs:
			h.notifySubscribers()

		case data := <-h.audioStats:
			// 发送端延迟统计（文本消息）：只发给音频订阅客户端；
			// 接收端 peer_manager 解析后写入 mapping，前端忽略未知文本类型。
			msg := OutMessage{isBinary: false, data: data}
			h.mu.RLock()
			for client := range h.clients {
				client.mu.RLock()
				want := client.sysAudioEnabled || client.micAudioEnabled
				client.mu.RUnlock()
				if !want {
					continue
				}
				select {
				case client.send <- msg:
				default:
				}
			}
			h.mu.RUnlock()

		case pkt := <-h.audioBroadcast:
			recvAt := time.Now()
			opusMsg := OutMessage{isBinary: true, data: pkt.chunk, queuedAt: recvAt}
			pcmMsg := OutMessage{isBinary: true, data: pkt.pcmChunk, queuedAt: recvAt}
			// 音频能力路由：
			//   - pcmChunk 非空（对端转发的音频，同时提供 Opus 与 PCM 两种形态）：
			//     无 AudioDecoder 的客户端（Wails macOS WKWebView / Safari）收 PCM，
			//     其余收 Opus；PCM 体积大（约 24 倍），只发给本机回环 UI 或声明了
			//     该能力的客户端，避免局域网带宽膨胀。
			//   - pcmChunk 为空（本机捕获引擎的广播，只有 Opus 形态）：
			//     无法解码 Opus 的客户端直接跳过，避免每帧都做一次失败的解码。
			isSysChunk := bytes.HasPrefix(pkt.chunk, audio.TagSystemAudioOpus) ||
				bytes.HasPrefix(pkt.chunk, audio.TagSystemAudio)
			isMicChunk := bytes.HasPrefix(pkt.chunk, audio.TagMicAudioOpus) ||
				bytes.HasPrefix(pkt.chunk, audio.TagMicAudio)
			if !isSysChunk && !isMicChunk {
				// 旧协议无 tag 的音频帧：按系统声处理（与既有回退口径一致）
				isSysChunk = true
			}
			isOpusChunk := bytes.HasPrefix(pkt.chunk, audio.TagSystemAudioOpus) ||
				bytes.HasPrefix(pkt.chunk, audio.TagMicAudioOpus)
			h.mu.RLock()
			for client := range h.clients {
				client.mu.RLock()
				wantSys := client.sysAudioEnabled
				wantMic := client.micAudioEnabled
				local := client.isLocal
				wantPCM := client.audioPCM
				client.mu.RUnlock()

				// 占位测试音只发给远程订阅端：本机 UI 播放测试音没有意义
				// （会听到与真实音频无关的周期性提示音），远程端映射本机
				// 系统声时仍可收到测试音以确认链路。
				if pkt.remoteOnly && local {
					continue
				}
				// 按流归属过滤：只订阅了系统声的客户端不再收到麦克风流
				if (isSysChunk && !wantSys) || (isMicChunk && !wantMic) {
					continue
				}
				msg := opusMsg
				switch {
				case pkt.pcmChunk != nil && wantPCM:
					msg = pcmMsg
				case pkt.pcmChunk != nil:
					// 有 AudioDecoder：继续收 Opus
				case isOpusChunk && wantPCM:
					continue
				}
				select {
				case client.send <- msg:
					atomic.AddUint64(&h.totalBytesSent, uint64(len(msg.data)))
				default:
					// Drop chunk on laggy client to prevent audio latency accumulation
				}
			}
			h.mu.RUnlock()

		case mouseEvent := <-h.mouseBroadcast:
			msg := OutMessage{isBinary: false, data: mouseEvent}
			h.mu.RLock()
			for client := range h.clients {
				client.mu.RLock()
				enabled := client.mouseEnabled
				client.mu.RUnlock()

				if enabled {
					select {
					case client.send <- msg:
						atomic.AddUint64(&h.totalBytesSent, uint64(len(mouseEvent)))
					default:
						// Drop frame if client buffer is backed up
					}
				}
			}
			h.mu.RUnlock()

		case keyboardEvent := <-h.keyboardBroadcast:
			msg := OutMessage{isBinary: false, data: keyboardEvent}
			h.mu.RLock()
			for client := range h.clients {
				client.mu.RLock()
				enabled := client.keyboardEnabled
				client.mu.RUnlock()

				if enabled {
					select {
					case client.send <- msg:
						atomic.AddUint64(&h.totalBytesSent, uint64(len(keyboardEvent)))
					default:
						// Drop frame if client buffer is backed up
					}
				}
			}
			h.mu.RUnlock()
		}
	}
}

func (h *Hub) notifySubscribers() {
	h.mu.RLock()
	var sysAudioCount, micAudioCount, mouseCount, keyboardCount int
	for client := range h.clients {
		client.mu.RLock()
		if client.sysAudioEnabled {
			sysAudioCount++
		}
		if client.micAudioEnabled {
			micAudioCount++
		}
		if client.mouseEnabled {
			mouseCount++
		}
		if client.keyboardEnabled {
			keyboardCount++
		}
		client.mu.RUnlock()
	}
	h.mu.RUnlock()

	if h.onSubsChanged != nil {
		h.onSubsChanged(sysAudioCount, micAudioCount, mouseCount, keyboardCount)
	}
}

// SubscriberCounts 返回各类流的当前订阅客户端数（诊断用）。
func (h *Hub) SubscriberCounts() (sysAudio, micAudio, mouse, keyboard int) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.clients {
		client.mu.RLock()
		if client.sysAudioEnabled {
			sysAudio++
		}
		if client.micAudioEnabled {
			micAudio++
		}
		if client.mouseEnabled {
			mouse++
		}
		if client.keyboardEnabled {
			keyboard++
		}
		client.mu.RUnlock()
	}
	return
}

// audioPacket 一次音频广播：Opus 数据 + 可选的 float32 PCM 形态 + 是否只发远程客户端。
type audioPacket struct {
	chunk      []byte // Opus 包（或旧版对端直接发来的 PCM 包）
	pcmChunk   []byte // 同一音频包的 float32 PCM 形态（无 AudioDecoder 的客户端用）
	remoteOnly bool
}

// BroadcastAudio 广播一份音频数据给订阅客户端。
// chunk 既可能是 Opus（AUPS/AUPM），也可能是旧版对端的 float32 PCM（AUDS/AUDM）。
func (h *Hub) BroadcastAudio(chunk []byte) {
	select {
	case h.audioBroadcast <- audioPacket{chunk: chunk}:
	default:
	}
}

// BroadcastAudioDual 广播同一音频的两种形态：opusChunk 给有 WebCodecs
// AudioDecoder 的客户端，pcmChunk 给没有的客户端（Wails macOS WKWebView、Safari）。
// 用于转发对端音频：对端只发 Opus，本机侧按客户端能力分流。
func (h *Hub) BroadcastAudioDual(opusChunk, pcmChunk []byte) {
	select {
	case h.audioBroadcast <- audioPacket{chunk: opusChunk, pcmChunk: pcmChunk}:
	default:
	}
}

// BroadcastAudioRemoteOnly 广播 PCM 但跳过本机 UI 客户端。
// 用于占位测试音等「仅对远程接收端有意义」的音频流。
func (h *Hub) BroadcastAudioRemoteOnly(chunk []byte) {
	select {
	case h.audioBroadcast <- audioPacket{chunk: chunk, remoteOnly: true}:
	default:
	}
}

// BroadcastMouse pushes a mouse event JSON to subscribed clients.
func (h *Hub) BroadcastMouse(event []byte) {
	select {
	case h.mouseBroadcast <- event:
	default:
	}
}

// BroadcastKeyboard pushes a keyboard event JSON to subscribed clients.
func (h *Hub) BroadcastKeyboard(event []byte) {
	select {
	case h.keyboardBroadcast <- event:
	default:
	}
}

// PCMAudioClientCount 返回声明了 PCM 音频能力的客户端数（诊断用）：
// 这些客户端没有 WebCodecs AudioDecoder，对端音频会以 float32 PCM 下发。
func (h *Hub) PCMAudioClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	count := 0
	for client := range h.clients {
		client.mu.RLock()
		if client.audioPCM {
			count++
		}
		client.mu.RUnlock()
	}
	return count
}

// ClientCount returns current connected client count.
func (h *Hub) ClientCount() int {
	return int(atomic.LoadInt64(&h.clientCount))
}

// TotalBytesSent returns accumulated bytes sent.
func (h *Hub) TotalBytesSent() uint64 {
	return atomic.LoadUint64(&h.totalBytesSent)
}

// AudioSendLatencyMs 返回音频「广播队列 → WebSocket 写完成」的平滑延迟（ms）。
// 属于发送端延迟分项（不含采集、编码与网络传输）。
func (h *Hub) AudioSendLatencyMs() int {
	return int(h.latSendUs.Load() / 1000)
}

// BroadcastAudioStats 把发送端延迟统计（JSON 文本消息）推给音频订阅客户端：
// 接收端 peer_manager 解析后写入 mapping，供 UI 展示「采集端」延迟分项。
// 未知类型的文本消息会被前端忽略，对旧版接收端安全。
func (h *Hub) BroadcastAudioStats(data []byte) {
	select {
	case h.audioStats <- data:
	default:
	}
}

// ewmaInt64 1/4 步长指数平滑（0=尚无样本），避免单帧抖动让延迟数字乱跳。
func ewmaInt64(v *atomic.Int64, cur int64) {
	if cur < 0 {
		cur = 0
	}
	if prev := v.Load(); prev != 0 {
		cur = (prev*3 + cur) / 4
	}
	v.Store(cur)
}

// NewClient constructs a new connected client.
// local 表示连接是否来自本机（回环/本机 IP 打开的 UI 窗口）：
//   - 本机 UI 默认不订阅音频/鼠标/键盘，由前端通过 set_mapping 按需开启，
//     避免本机捕获引擎在无人消费事件时 60Hz 空转轮询；
//   - 局域网远端保持默认全订阅，兼容未升级的旧版本对端（无需握手即可收到数据）。
func NewClient(hub *Hub, conn *websocket.Conn, remoteAddr, userAgent string, local bool) *Client {
	remoteIP := remoteAddr
	if idx := strings.LastIndex(remoteAddr, ":"); idx != -1 {
		remoteIP = remoteAddr[:idx]
	}
	// 默认订阅：局域网远端全开（兼容旧对端：旧协议只有一个 audio 并集开关）；
	// 本机 UI 全关（前端按需 set_mapping 开启）。新协议握手会立刻按
	// sys_audio/mic_audio 分流，把不需要的流关掉。
	sysAudioEnabled, micAudioEnabled, mouseEnabled, keyboardEnabled := true, true, true, true
	if local {
		sysAudioEnabled = false
		micAudioEnabled = false
		mouseEnabled = false
		keyboardEnabled = false
	}
	return &Client{
		id:              fmt.Sprintf("client-%d", atomic.AddUint64(&clientSeq, 1)),
		hub:             hub,
		conn:            conn,
		send:            make(chan OutMessage, 64),
		remoteIP:        remoteIP,
		remoteAddr:      remoteAddr,
		userAgent:       userAgent,
		deviceType:      ParseDeviceType(userAgent),
		joinedAt:        time.Now(),
		isLocal:         local,
		sysAudioEnabled: sysAudioEnabled,
		micAudioEnabled: micAudioEnabled,
		mouseEnabled:    mouseEnabled,
		keyboardEnabled: keyboardEnabled,
	}
}

// applyMapping 应用一条 set_mapping 订阅声明（readPump 与单测共用）：
//   - sys_audio/mic_audio：新协议按流订阅（只映射系统声时不必采集麦克风）；
//   - audio：旧协议并集开关（兼容旧版对端，收到后两条都置位）；
//   - pcm：客户端无 WebCodecs AudioDecoder，服务端改发 float32 PCM。
func (c *Client) applyMapping(audio, sysAudio, micAudio, mouse, keyboard, pcm *bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if sysAudio != nil || micAudio != nil {
		c.audioSplit = true
		if sysAudio != nil {
			c.sysAudioEnabled = *sysAudio
		}
		if micAudio != nil {
			c.micAudioEnabled = *micAudio
		}
	} else if audio != nil && !c.audioSplit {
		c.sysAudioEnabled = *audio
		c.micAudioEnabled = *audio
	}
	if mouse != nil {
		c.mouseEnabled = *mouse
	}
	if keyboard != nil {
		c.keyboardEnabled = *keyboard
	}
	if pcm != nil {
		c.audioPCM = *pcm
	}
}

// GetInboundClients returns real-time snapshot of all inbound connected clients.
func (h *Hub) GetInboundClients() []InboundClientInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make([]InboundClientInfo, 0, len(h.clients))
	now := time.Now()
	for c := range h.clients {
		c.mu.RLock()
		info := InboundClientInfo{
			ID:              c.id,
			RemoteAddr:      c.remoteAddr,
			RemoteIP:        c.remoteIP,
			UserAgent:       c.userAgent,
			DeviceType:      c.deviceType,
			ConnectedAt:     c.joinedAt.Format("15:04:05"),
			DurationSec:     int64(now.Sub(c.joinedAt).Seconds()),
			AudioEnabled:    c.sysAudioEnabled || c.micAudioEnabled,
			MouseEnabled:    c.mouseEnabled,
			KeyboardEnabled: c.keyboardEnabled,
		}
		c.mu.RUnlock()
		result = append(result, info)
	}
	return result
}

func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			msgType := websocket.BinaryMessage
			if !msg.isBinary {
				msgType = websocket.TextMessage
			}

			w, err := c.conn.NextWriter(msgType)
			if err != nil {
				return
			}
			w.Write(msg.data)

			if err := w.Close(); err != nil {
				return
			}
			// 发送端延迟分项：广播队列 → WS 写完成（EWMA，跨客户端平滑）
			if msg.isBinary && !msg.queuedAt.IsZero() {
				ewmaInt64(&c.hub.latSendUs, time.Since(msg.queuedAt).Microseconds())
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Client) readPump() {
	defer func() {
		mouse.ReleaseAllButtons()
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(2048)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		msgType, p, err := c.conn.ReadMessage()
		if err != nil {
			break
		}

		if msgType == websocket.TextMessage {
			var cmd struct {
				Action   string `json:"action"` // "set_mapping"
				Audio    *bool  `json:"audio"`  // 旧协议并集开关（sys||mic）
				SysAudio *bool  `json:"sys_audio"`
				MicAudio *bool  `json:"mic_audio"`
				Mouse    *bool  `json:"mouse"`
				Keyboard *bool  `json:"keyboard"`
				// 客户端音频解码能力：true 表示无 WebCodecs AudioDecoder，
				// 服务端对端音频改发 float32 PCM（WKWebView/Safari 无法解 Opus）
				PCM *bool `json:"pcm"`
				// 对端节点身份（新版实例握手时上报，用于设备档案归并）
				NodeID   string `json:"node_id"`
				NodeName string `json:"node_name"`
			}
			if err := json.Unmarshal(p, &cmd); err != nil {
				continue
			}

			if cmd.Action == "set_mapping" {
				if cmd.NodeID != "" && c.hub.onNodeIdentity != nil {
					c.hub.onNodeIdentity(c.remoteIP, cmd.NodeID, cmd.NodeName)
				}
				c.applyMapping(cmd.Audio, cmd.SysAudio, cmd.MicAudio, cmd.Mouse, cmd.Keyboard, cmd.PCM)

				select {
				case c.hub.updateSubs <- struct{}{}:
				default:
				}
			}
		}
	}
}
