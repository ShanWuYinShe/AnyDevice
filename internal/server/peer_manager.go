package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"any_device/internal/audio"
	"any_device/internal/discovery"
	"any_device/internal/keyboard"
	"any_device/internal/mouse"
	"any_device/internal/util"

	"github.com/gorilla/websocket"
)

// PeerMappingState stores what the local user chooses to map from a specific remote peer.
type PeerMappingState struct {
	PlaySystemAudioLocal bool   `json:"play_system_audio_local"`
	PlayMicAudioLocal    bool   `json:"play_mic_audio_local"`
	ExecuteMouseLocal    bool   `json:"execute_mouse_local"`
	ExecuteKeyboardLocal bool   `json:"execute_keyboard_local"`
	ConnectionStatus     string `json:"connection_status"` // "connected", "connecting", "disconnected", "error"
	LatencyMs            int64  `json:"latency_ms"`
	LastError            string `json:"last_error,omitempty"`

	// 对端推流到本机的实际码率（KiB/s，服务端按 ~2 秒窗口统计）。
	// 这是「网络侧」字节率：Opus 约 16 KB/s，旧版对端 float32 PCM 约 350–380 KB/s。
	// UI 必须显示这个值而不是本机窗口收到的字节数——原生窗口收到的是解码后的
	// PCM（约 375 KB/s），比真实网络码率大 24 倍，会把带宽显示成假的。
	RxSystemAudioKBps int `json:"rx_system_audio_kbps"`
	RxMicAudioKBps    int `json:"rx_mic_audio_kbps"`

	// Backward compatibility fields
	PlayAudioLocal bool `json:"play_audio_local"`
}

// UnifiedPeer is the clean, discoverer-centric peer model exposed to the UI.
type UnifiedPeer struct {
	ID       string                       `json:"id"`
	Name     string                       `json:"name"`
	Alias    string                       `json:"alias,omitempty"` // 本机备注（后端已把 Name 解析为“备注优先”的显示名）
	OS       string                       `json:"os"`
	IP       string                       `json:"ip"`
	Port     int                          `json:"port"`
	Target   string                       `json:"target"`
	Online   bool                         `json:"online"`
	Provides discovery.DeviceCapabilities `json:"provides"`
	Mapping  PeerMappingState             `json:"mapping"`
	LastSeen string                       `json:"last_seen"`
	IsManual bool                         `json:"is_manual"`
	Source   string                       `json:"source,omitempty"` // beacon=自动发现 inbound=外部UI接入 manual=手动添加
}

type peerConnection struct {
	conn      *websocket.Conn
	cancel    context.CancelFunc
	startTime time.Time
	// writeMu 串行化对该连接的写：gorilla/websocket 禁止并发写，
	// SetMapping（HTTP handler goroutine）与 connectAndLoop（连接 goroutine）
	// 都会写握手，无互斥时会 panic 或帧交错（2026-10-24 审查发现）。
	writeMu sync.Mutex
	// decoders 按音频流 tag 懒创建的 Opus→float32 PCM 解码器：
	// 本机 UI 客户端（Wails macOS WKWebView / Safari）没有 WebCodecs
	// AudioDecoder，转发对端音频时需要服务端先解码成 PCM 再下发。
	decoders map[string]*audio.OpusDecoder
}

// writeJSON 对对端连接的串行化写入口：所有 WriteJSON 必须经此发送。
func (pc *peerConnection) writeJSON(v interface{}) error {
	pc.writeMu.Lock()
	defer pc.writeMu.Unlock()
	return pc.conn.WriteJSON(v)
}

type PeerManager struct {
	discovery *discovery.Service
	hub       *Hub
	mappings  map[string]*PeerMappingState
	conns     map[string]*peerConnection
	mu        sync.RWMutex
}

func NewPeerManager(disc *discovery.Service, hub *Hub) *PeerManager {
	return &PeerManager{
		discovery: disc,
		hub:       hub,
		mappings:  make(map[string]*PeerMappingState),
		conns:     make(map[string]*peerConnection),
	}
}

// viewerHost 从 remoteAddr 提取纯 IP（去端口与 IPv6 方括号）。
func viewerHost(addr string) string {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	return strings.Trim(host, "[]")
}

// ListPeers returns all discovered peers with their current local mapping settings.
// viewerIP 是发起本次请求的客户端 IP：
// 手机/另一台电脑打开本 UI 时会被服务端登记为“入站对端”，
// 若不按查看者过滤，该设备自己的列表里会永远出现一台“自己”
// （用户报告的“设备列表一直显示自己”的根因），因此这里按 IP 剔除。
func (pm *PeerManager) ListPeers(viewerIP string) []UnifiedPeer {
	discPeers := pm.discovery.GetPeers()

	pm.mu.RLock()
	defer pm.mu.RUnlock()

	result := make([]UnifiedPeer, 0, len(discPeers))
	for _, dp := range discPeers {
		if pm.discovery.IsSelf(dp.ID, dp.IP, dp.Port) || util.IsLocalIP(dp.IP) || util.IsLocalIP(dp.Target) {
			continue
		}
		if viewerIP != "" && viewerIP == viewerHost(dp.IP) {
			continue
		}

		mapping, exists := pm.mappings[dp.ID]
		if !exists {
			mapping = &PeerMappingState{
				ConnectionStatus: "disconnected",
			}
		}

		result = append(result, UnifiedPeer{
			ID:       dp.ID,
			Name:     dp.Name,
			Alias:    dp.Alias,
			OS:       dp.OS,
			IP:       dp.IP,
			Port:     dp.Port,
			Target:   dp.Target,
			Online:   dp.Online,
			Provides: dp.Provides,
			Mapping:  *mapping,
			LastSeen: dp.LastSeen.Format("15:04:05"),
			IsManual: dp.IsManual,
			Source:   string(dp.Source),
		})
	}
	return result
}

// AddManualPeer adds a peer by IP:port and returns it.
func (pm *PeerManager) AddManualPeer(target, name string) (UnifiedPeer, error) {
	dp := pm.discovery.AddManualPeer(target, name)
	if dp == nil {
		return UnifiedPeer{}, fmt.Errorf("无法添加该地址 (不能添加本机作为对端设备)")
	}
	return UnifiedPeer{
		ID:       dp.ID,
		Name:     dp.Name,
		OS:       dp.OS,
		IP:       dp.IP,
		Port:     dp.Port,
		Target:   dp.Target,
		Online:   dp.Online,
		Provides: dp.Provides,
		Mapping: PeerMappingState{
			ConnectionStatus: "disconnected",
		},
		LastSeen: dp.LastSeen.Format("15:04:05"),
		IsManual: true,
	}, nil
}

// RenamePeer assigns a custom name or alias to a peer.
func (pm *PeerManager) RenamePeer(peerID, newName string) error {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return fmt.Errorf("设备名称不能为空")
	}
	if !pm.discovery.RenamePeer(peerID, newName) {
		return fmt.Errorf("未找到该对端设备")
	}
	return nil
}

// SetMapping updates the discoverer's local mapping decisions for a peer.
func (pm *PeerManager) SetMapping(peerID string, playSystemAudio, playMicAudio, executeMouse, executeKeyboard *bool) error {
	pm.mu.Lock()
	mapping, exists := pm.mappings[peerID]
	if !exists {
		mapping = &PeerMappingState{
			ConnectionStatus: "disconnected",
		}
		pm.mappings[peerID] = mapping
	}

	if playSystemAudio != nil {
		mapping.PlaySystemAudioLocal = *playSystemAudio
		mapping.PlayAudioLocal = *playSystemAudio
	}
	if playMicAudio != nil {
		mapping.PlayMicAudioLocal = *playMicAudio
	}
	if executeMouse != nil {
		mapping.ExecuteMouseLocal = *executeMouse
	}
	if executeKeyboard != nil {
		mapping.ExecuteKeyboardLocal = *executeKeyboard
	}
	// 关闭音频映射后立即清零码率，避免重开映射前卡片展示上次的旧数值
	if playSystemAudio != nil && !*playSystemAudio {
		mapping.RxSystemAudioKBps = 0
	}
	if playMicAudio != nil && !*playMicAudio {
		mapping.RxMicAudioKBps = 0
	}

	needsConn := mapping.PlaySystemAudioLocal || mapping.PlayMicAudioLocal || mapping.ExecuteMouseLocal || mapping.ExecuteKeyboardLocal
	pm.mu.Unlock()

	// Find peer target
	var target string
	for _, dp := range pm.discovery.GetPeers() {
		if dp.ID == peerID {
			target = dp.Target
			break
		}
	}
	if target == "" {
		return fmt.Errorf("找不到该对端设备信息")
	}

	if needsConn {
		pm.ensureConnected(peerID, target)
	} else {
		pm.disconnect(peerID)
		return nil
	}

	// 若本次决策关闭了本地鼠标或键盘执行，立即释放可能卡住的按钮与按键
	if executeMouse != nil && !*executeMouse {
		mouse.ReleaseAllButtons()
	}
	if executeKeyboard != nil && !*executeKeyboard {
		keyboard.ReleaseAll()
	}

	// 连接已建立时同步推送最新订阅声明：中途追加映射（如再开鼠标/键盘）
	// 无需断开重连，对端 hub 会按声明开关对应流转发。
	pm.mu.RLock()
	connObj, connected := pm.conns[peerID]
	var snap PeerMappingState
	if connected && pm.mappings[peerID] != nil {
		snap = *pm.mappings[peerID]
	}
	pm.mu.RUnlock()
	if connected && connObj != nil && connObj.conn != nil {
		_ = connObj.writeJSON(pm.mappingHandshake(snap))
	}

	return nil
}

// mappingHandshake 依据本地映射决策生成发给对端的握手声明：
//   - 订阅部分（audio/mouse/keyboard）：对端 hub 按此决定是否把它的
//     音频/鼠标/键盘流转发给本连接；
//   - 身份部分（node_id/node_name）：让对端把本连接归并到本机的设备档案，
//     避免“同一台设备因自动发现/主动连接出现多条目”。
func (pm *PeerManager) mappingHandshake(m PeerMappingState) map[string]interface{} {
	return map[string]interface{}{
		"action":    "set_mapping",
		"audio":     m.PlaySystemAudioLocal || m.PlayMicAudioLocal || m.PlayAudioLocal,
		"mouse":     m.ExecuteMouseLocal,
		"keyboard":  m.ExecuteKeyboardLocal,
		"node_id":   pm.discovery.ID(),
		"node_name": pm.discovery.Name(),
	}
}

func (pm *PeerManager) ensureConnected(peerID, target string) {
	pm.mu.Lock()
	if _, exists := pm.conns[peerID]; exists {
		pm.mu.Unlock()
		return
	}

	mapping := pm.mappings[peerID]
	mapping.ConnectionStatus = "connecting"
	mapping.LastError = ""
	pm.mu.Unlock()

	go pm.connectAndLoop(peerID, target)
}

func (pm *PeerManager) disconnect(peerID string) {
	pm.mu.Lock()
	connObj, exists := pm.conns[peerID]
	if exists {
		delete(pm.conns, peerID)
		if connObj.cancel != nil {
			connObj.cancel()
		}
		if connObj.conn != nil {
			_ = connObj.conn.Close()
		}
	}

	if mapping, ok := pm.mappings[peerID]; ok {
		mapping.ConnectionStatus = "disconnected"
	}
	pm.mu.Unlock()
}

// setRxRates 把服务端统计的对端推流码率写入映射状态（UI 轮询 /api/peers 读取）。
func (pm *PeerManager) setRxRates(peerID string, sysKBps, micKBps int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if m, ok := pm.mappings[peerID]; ok {
		m.RxSystemAudioKBps = sysKBps
		m.RxMicAudioKBps = micKBps
	}
}

// kibPerSec 把窗口内累计字节数换算成 KiB/s（窗口长度按实际经过时间归一化，
// 避免消息到达抖动导致读数跳变）。
// maxWheelDelta 滚轮增量的安全上限（WHEEL_DELTA=120/格，±83 格足够任何真实滚动）。
const maxWheelDelta = 10000

// clampUnit 把对端发来的归一化坐标限制到 [0,1]，NaN 归零：
// 异常/恶意对端发任意浮点值时，避免 CGEvent/SendInput 收到未定义参数。
func clampUnit(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func kibPerSec(bytes int64, seconds float64) int {
	if bytes <= 0 || seconds <= 0 {
		return 0
	}
	return int(float64(bytes) / seconds / 1024)
}

// forwardPeerAudio 把对端 Opus 音频转发给本机订阅客户端：
//   - 有 WebCodecs AudioDecoder 的客户端（Chrome/WebView2）收 Opus 原始包；
//   - 没有的客户端（Wails macOS WKWebView、Safari）收解码后的 float32 PCM。
//
// 为什么要在这里解码：对端只发 Opus（128kbps，带宽是 PCM 的 1/24），而
// WKWebView 目前只有 VideoDecoder、没有 AudioDecoder，无法前端解码。
// 服务端在回环转发路径上解码，收到的仍是省带宽的 Opus，付出的是本机 CPU
// （两条流各 50 帧/秒，可忽略），且不改变对端协议、旧版本对端照常可用。
//
// 每个 peer 连接按流 tag 各持一个解码器（Opus 解码器有状态，不能跨流复用）；
// 解码失败时退回只发 Opus，保证有解码能力的客户端不受影响。
func (pm *PeerManager) forwardPeerAudio(pc *peerConnection, data []byte) {
	if len(data) < 4 || pc == nil {
		return
	}
	tag := string(data[:4])
	dec := pc.decoders[tag]
	if dec == nil {
		d, err := audio.NewOpusDecoder()
		if err != nil {
			util.Logf("[音频] 转发端创建 Opus 解码器失败: %v\n", err)
			pm.hub.BroadcastAudio(data)
			return
		}
		pc.decoders[tag] = d
		dec = d
	}
	pcm, err := dec.DecodeToPCM(data[4:])
	if err != nil {
		util.Logf("[音频] 转发端 Opus 解码失败: %v\n", err)
		pm.hub.BroadcastAudio(data)
		return
	}
	if len(pcm) == 0 {
		return
	}
	pcmTag := audio.TagSystemAudio
	if tag == "AUPM" {
		pcmTag = audio.TagMicAudio
	}
	pcmChunk := make([]byte, 0, len(pcmTag)+len(pcm))
	pcmChunk = append(pcmChunk, pcmTag...)
	pcmChunk = append(pcmChunk, pcm...)
	pm.hub.BroadcastAudioDual(data, pcmChunk)
}

func (pm *PeerManager) connectAndLoop(peerID, target string) {
	clean := strings.TrimPrefix(target, "ws://")
	clean = strings.TrimPrefix(clean, "http://")
	if !strings.Contains(clean, ":") {
		// 兜底端口与本机服务端口一致（discovery 侧 AddManualPeer 同口径）
		clean = fmt.Sprintf("%s:%d", clean, pm.discovery.Port())
	}

	u := url.URL{Scheme: "ws", Host: clean, Path: "/ws"}
	dialer := websocket.Dialer{
		HandshakeTimeout: 4 * time.Second,
	}

	t0 := time.Now()
	conn, _, err := dialer.Dial(u.String(), nil)
	latency := time.Since(t0).Milliseconds()

	pm.mu.Lock()
	mapping, ok := pm.mappings[peerID]
	if !ok {
		pm.mu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
		return
	}

	if err != nil {
		mapping.ConnectionStatus = "error"
		mapping.LastError = fmt.Sprintf("连接失败: %v", err)
		pm.mu.Unlock()
		return
	}

	// 建连竞态防护：Dial（最长 4s）期间用户可能再次 SetMapping 触发了第二个
	// connectAndLoop。若这里不 re-check，后写者会无条件覆盖 pm.conns——
	// 被覆盖的连接无人 Close（goroutine/连接泄漏、音频双流），且先者的
	// defer 误删后者的 map 条目，disconnect 再也关不掉它。
	if existing, exists := pm.conns[peerID]; exists && existing != nil {
		pm.mu.Unlock()
		_ = conn.Close() // 我们这条是多余的，只关自己
		return
	}

	// 出站方向的入站防护（与入站 hub.readPump 对齐）：手动添加的对端可以是
	// 任意 LAN 主机，超大帧会整帧读入内存；假死对端（TCP 未断）会让
	// ReadMessage 永久阻塞。读超时靠对端 writePump 的 30s ping 续命。
	conn.SetReadLimit(1 << 20)
	conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	conn.SetPingHandler(func(appData string) error {
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(5*time.Second))
	})

	ctx, cancel := context.WithCancel(context.Background())
	connObj := &peerConnection{
		conn:      conn,
		cancel:    cancel,
		startTime: time.Now(),
		decoders:  make(map[string]*audio.OpusDecoder),
	}
	// 连接建立即声明本端需要的流订阅（此前依赖对端默认全开；
	// 现在对端把默认订阅收紧后，必须显式握手，否则收不到任何流）。
	// 放锁后经 writeJSON 串行写，避免与 SetMapping 的推送并发写。
	pm.conns[peerID] = connObj
	mapping.ConnectionStatus = "connected"
	mapping.LatencyMs = latency
	mapping.LastError = ""
	pm.mu.Unlock()
	_ = connObj.writeJSON(pm.mappingHandshake(*mapping))

	// 对端连接断开：释放本机注入的按钮与按键，防止卡在“按住”状态
	defer mouse.ReleaseAllButtons()
	defer keyboard.ReleaseAll()

	var lastMouseDownBtn string

	// 音频链路诊断计数（ANY_DEVICE_AUDIO_DEBUG=1 时输出）
	var audioFwdFrames, audioFwdBytes int
	var audioFwdLastFrames, audioFwdLastBytes int
	audioFwdLastLog := time.Now()

	// 对端推流码率统计（写入映射状态供 UI 展示）：按 tag 区分系统声/麦克风，
	// 每 ~2 秒窗口把字节数换算成 KiB/s。窗口长度按实际经过时间归一化。
	var rxSysBytes, rxMicBytes int64
	rxLast := time.Now()
	flushRxRates := func() {
		now := time.Now()
		elapsed := now.Sub(rxLast)
		if elapsed < 2*time.Second {
			return
		}
		secs := elapsed.Seconds()
		pm.setRxRates(peerID, kibPerSec(rxSysBytes, secs), kibPerSec(rxMicBytes, secs))
		rxSysBytes, rxMicBytes = 0, 0
		rxLast = now
	}

	defer func() {
		for _, d := range connObj.decoders {
			d.Destroy()
		}
		cancel()
		_ = conn.Close()
		pm.mu.Lock()
		// 只清理仍指向自己的条目：避免误删后续重连建立的新连接
		//（旧写法会把别人的条目删掉，disconnect 从此关不掉真连接）
		if cur, ok := pm.conns[peerID]; ok && cur == connObj {
			delete(pm.conns, peerID)
		}
		if m, ok := pm.mappings[peerID]; ok {
			if m.PlaySystemAudioLocal || m.PlayMicAudioLocal || m.ExecuteMouseLocal || m.ExecuteKeyboardLocal {
				m.ConnectionStatus = "disconnected"
			}
			m.RxSystemAudioKBps = 0
			m.RxMicAudioKBps = 0
		}
		pm.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msgType, data, err := conn.ReadMessage()
		if err != nil {
			pm.mu.Lock()
			if m, ok := pm.mappings[peerID]; ok {
				m.ConnectionStatus = "error"
				m.LastError = fmt.Sprintf("连接中断: %v", err)
			}
			pm.mu.Unlock()
			return
		}

		pm.mu.RLock()
		curMapping := *pm.mappings[peerID]
		pm.mu.RUnlock()

		if msgType == websocket.BinaryMessage {
			if len(data) >= 4 {
				tag := string(data[:4])
				// 音频链路诊断：帧到达/转发统计（ANY_DEVICE_AUDIO_DEBUG=1）
				if util.DebugAudio() {
					audioFwdFrames++
					audioFwdBytes += len(data)
					now := time.Now()
					if now.Sub(audioFwdLastLog) >= 2*time.Second {
						util.Logf("[音频诊断] 转发端: 收到对端音频 %d 帧/%d KB（最新 tag=%s, 映射开=%v/%v/%v）\n",
							audioFwdFrames-audioFwdLastFrames, (audioFwdBytes-audioFwdLastBytes)/1024, tag,
							curMapping.PlaySystemAudioLocal, curMapping.PlayMicAudioLocal, curMapping.PlayAudioLocal)
						audioFwdLastFrames = audioFwdFrames
						audioFwdLastBytes = audioFwdBytes
						audioFwdLastLog = now
					}
				}
				if tag == "AUPS" {
					if curMapping.PlaySystemAudioLocal {
						rxSysBytes += int64(len(data))
						pm.forwardPeerAudio(connObj, data)
					}
				} else if tag == "AUPM" {
					if curMapping.PlayMicAudioLocal {
						rxMicBytes += int64(len(data))
						pm.forwardPeerAudio(connObj, data)
					}
				} else if tag == "AUDS" {
					if curMapping.PlaySystemAudioLocal {
						rxSysBytes += int64(len(data))
						pm.hub.BroadcastAudio(data)
					}
				} else if tag == "AUDM" {
					if curMapping.PlayMicAudioLocal {
						rxMicBytes += int64(len(data))
						pm.hub.BroadcastAudio(data)
					}
				} else {
					// Fallback for legacy untagged stream
					if curMapping.PlaySystemAudioLocal || curMapping.PlayMicAudioLocal {
						pm.hub.BroadcastAudio(data)
					}
				}
			}
		} else if msgType == websocket.TextMessage {
			var baseEvt struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(data, &baseEvt)

			if (baseEvt.Type == "" || baseEvt.Type == "mouse") && curMapping.ExecuteMouseLocal {
				var evt mouse.MouseEvent
				if err := json.Unmarshal(data, &evt); err == nil {
					// Execute remote mouse movements & clicks directly on local OS!
					if util.DebugKeys() && evt.Action != "move" {
						util.Logf("[鼠标链路] 收到对端鼠标 %s (%.2f, %.2f)\n", evt.Action, evt.X, evt.Y)
					}
					if evt.Action == "wheel" {
						// 滚轮同步：正 = 向上；限幅防异常/恶意对端发超大增量
						delta := evt.WheelDelta
						if delta > maxWheelDelta {
							delta = maxWheelDelta
						} else if delta < -maxWheelDelta {
							delta = -maxWheelDelta
						}
						mouse.SimulateScroll(delta)
					} else {
						mouse.SimulateAbsolute(clampUnit(evt.X), clampUnit(evt.Y))
						if evt.Action == "down" || evt.Action == "up" {
							btn := evt.Button
							if btn == "" {
								// 兼容旧对端：up 时按上一次按下的按钮回退，或按当前 down 状态推断
								btn = "left"
								if evt.RightDown {
									btn = "right"
								} else if evt.MiddleDown {
									btn = "middle"
								} else if evt.Action == "up" && lastMouseDownBtn != "" {
									btn = lastMouseDownBtn
								}
							}
							if evt.Action == "down" {
								lastMouseDownBtn = btn
							}
							mouse.SimulateButton(btn, evt.Action == "down")
						}
					}
				}

				// Also forward with source metadata so local UI can visualize cursor position
				var obj map[string]interface{}
				if err := json.Unmarshal(data, &obj); err == nil {
					obj["source_peer"] = target
					obj["peer_id"] = peerID
					if enriched, err := json.Marshal(obj); err == nil {
						data = enriched
					}
				}
				pm.hub.BroadcastMouse(data)
			} else if baseEvt.Type == "keyboard" && curMapping.ExecuteKeyboardLocal {
				var evt keyboard.KeyEvent
				if err := json.Unmarshal(data, &evt); err == nil {
					// Execute remote physical keyboard events directly on local OS!
					if util.DebugKeys() {
						util.Logf("[键盘链路] 收到对端按键 %s %s (键码 %d)\n", evt.Key, evt.Action, evt.KeyCode)
					}
					keyboard.SimulateKey(evt.Key, evt.Action == "down")
				}

				// Also forward with source metadata so local UI can visualize active key telemetry
				var obj map[string]interface{}
				if err := json.Unmarshal(data, &obj); err == nil {
					obj["source_peer"] = target
					obj["peer_id"] = peerID
					if enriched, err := json.Marshal(obj); err == nil {
						data = enriched
					}
				}
				pm.hub.BroadcastKeyboard(data)
			}
		}
		// 音频持续时会由音频帧频繁触发；对端停发时也靠其它消息把旧码率衰减下来
		flushRxRates()
	}
}
