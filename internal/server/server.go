package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"time"

	"any_device/internal/audio"
	"any_device/internal/discovery"
	"any_device/internal/keyboard"
	"any_device/internal/mouse"
	"any_device/internal/util"
	"any_device/internal/web"

	"github.com/gorilla/websocket"
	"rsc.io/qr"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  2048,
	WriteBufferSize: 8192,
	CheckOrigin: func(r *http.Request) bool {
		return isAllowedOrigin(r)
	},
}

// isAllowedOrigin 判断 WebSocket 升级请求的 Origin 是否可信。
// 跨源 WebSocket 不受浏览器同源策略限制，全靠服务端 CheckOrigin 把关；
// 而本服务的 WS 连上即可订阅系统声音与键鼠事件流，必须白名单放行：
//   - 无 Origin：本机 peer_manager 出站 dial、curl 等非浏览器客户端；
//   - 本机地址：UI 从 http://<本机IP>:<port> 打开时的同源页面；
//   - Wails 窗口：macOS WKWebView 为 wails://，Windows WebView2 为 http://wails.localhost。
//
// 其余（任意网页跨源直连 ws://<本机IP>:8080/ws 窃听键鼠/音频）一律拒绝。
func isAllowedOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	reject := func() {
		util.Logf("[WS] 拒绝跨源 WebSocket 连接: Origin=%q RemoteAddr=%s (若桌面窗口被误拒，请把日志里的 Origin 值加入白名单)", origin, r.RemoteAddr)
	}
	u, err := url.Parse(origin)
	if err != nil {
		reject()
		return false
	}
	switch u.Scheme {
	case "wails":
		// Wails 桌面窗口（macOS WKWebView 自定义 scheme）
		return true
	case "http", "https":
		host := u.Hostname()
		if host == "" {
			reject()
			return false
		}
		// Windows WebView2 的 Wails 窗口固定以 wails.localhost 为来源
		if host == "localhost" || host == "wails.localhost" {
			return true
		}
		if ip := net.ParseIP(host); ip != nil {
			if ip.IsLoopback() || util.IsLocalIP(ip.String()) {
				return true
			}
			reject()
			return false
		}
		// 主机名（如 mDNS 名称）解析到本机地址也算同源
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if addrs, err := net.DefaultResolver.LookupHost(ctx, host); err == nil {
			for _, a := range addrs {
				if util.IsLocalIP(a) {
					return true
				}
			}
		}
		reject()
		return false
	default:
		reject()
		return false
	}
}

// isLocalInterfaceIP 判断给定 IP 字符串是否属于本机现有网卡地址。
func isLocalInterfaceIP(ipStr string) bool {
	for _, info := range util.GetAllIPs() {
		if info.IP == ipStr {
			return true
		}
	}
	return false
}

// limitBody 统一限制请求体大小：LAN 任意设备可直连本服务，无上限的 JSON
// 解码可被超大 body 耗尽内存（WS 帧另有 SetReadLimit 把关，此处管 HTTP API）。
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		next.ServeHTTP(w, r)
	})
}

type Server struct {
	port              int
	currentIP         string
	hub               *Hub
	systemAudioEngine *audio.Engine
	micAudioEngine    *audio.Engine
	mouseEngine       *mouse.Engine
	keyboardEngine    *keyboard.Engine
	discovery         *discovery.Service
	peerMgr           *PeerManager
	mu                sync.RWMutex
}

func NewServer(port int, initialIP string, hub *Hub, systemAudioEngine *audio.Engine, micAudioEngine *audio.Engine, mouseEngine *mouse.Engine, keyboardEngine *keyboard.Engine) *Server {
	sysDev := "系统扬声器输出"
	if systemAudioEngine != nil {
		sysDev = systemAudioEngine.DeviceName()
	}
	micDev := "麦克风输入"
	if micAudioEngine != nil {
		micDev = micAudioEngine.DeviceName()
	}

	disc := discovery.NewService(port, initialIP, discovery.DeviceCapabilities{
		SystemAudio: systemAudioEngine != nil,
		MicAudio:    micAudioEngine != nil,
		Mouse:       true,
		Keyboard:    keyboardEngine != nil,
		Control:     true,
		AudioDevice: sysDev,
		MicDevice:   micDev,
		AudioFormat: audio.ProtocolFormatOpus,
		Audio:       systemAudioEngine != nil,
	})
	disc.Start()

	// 恢复持久化的本机名称与对端备注（设备改名重启不再丢失）
	cfg := util.LoadNodeConfig()
	if cfg.Name != "" {
		disc.SetName(cfg.Name)
	}
	if len(cfg.Aliases) > 0 || len(cfg.AliasesByIP) > 0 {
		disc.SetAliases(cfg.Aliases, cfg.AliasesByIP)
	}
	disc.SetPersistHook(func(c util.NodeConfig) {
		if err := util.SaveNodeConfig(c); err != nil {
			util.Logf("[Discovery] 配置持久化失败 (可用环境变量 ANY_DEVICE_CONFIG 指定可写路径): %v\n", err)
		}
	})

	peerMgr := NewPeerManager(disc, hub)

	s := &Server{
		port:              port,
		currentIP:         initialIP,
		hub:               hub,
		systemAudioEngine: systemAudioEngine,
		micAudioEngine:    micAudioEngine,
		mouseEngine:       mouseEngine,
		keyboardEngine:    keyboardEngine,
		discovery:         disc,
		peerMgr:           peerMgr,
	}

	// 入站设备在线校准：以真实 WebSocket 连接集合为准，
	// 连接断开后最多 5 秒即在列表中显示离线（此前依赖 7 秒超时且重连会堆积条目）。
	go s.reconcileInboundLoop()

	// 对端节点身份回调：对端主动连接时通过握手上报自己的节点 ID，
	// 据此把入站档案归并到正确的设备（跨 IP 场景按 IP 归并会失效，
	// 节点 ID 是设备唯一权威身份）。
	hub.SetNodeIdentityHandler(func(remoteIP, nodeID, nodeName string) {
		s.discovery.ClaimNodeIdentity(viewerHost(remoteIP), nodeID, nodeName)
	})

	return s
}

// reconcileInboundLoop 周期性把当前连接集合同步给发现服务。
func (s *Server) reconcileInboundLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.reconcileInbound()
	}
}

func (s *Server) reconcileInbound() {
	if s.discovery == nil {
		return
	}
	live := make(map[string]bool)
	for _, c := range s.hub.GetInboundClients() {
		ip := viewerHost(c.RemoteIP)
		if ip != "" && !util.IsLocalIP(ip) {
			live[ip] = true
		}
	}
	s.discovery.ReconcileInbound(live)
}

func (s *Server) SetCurrentIP(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentIP = ip
	if s.discovery != nil {
		s.discovery.SetCurrentIP(ip)
	}
	fmt.Printf("[Network] 用户已切换当前通信网卡 IP 为: %s (访问地址: %s)\n", ip, s.getCurrentURLUnlocked())
}

func (s *Server) GetCurrentIP() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentIP
}

func (s *Server) GetCurrentURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getCurrentURLUnlocked()
}

func (s *Server) getCurrentURLUnlocked() string {
	return fmt.Sprintf("http://%s:%d", s.currentIP, s.port)
}

func (s *Server) SetupRoutes() http.Handler {
	mux := http.NewServeMux()

	// WebSocket unified stream endpoint (audio binary + mouse JSON text)
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			fmt.Printf("[WS] Upgrade error: %v\n", err)
			return
		}

		// 本机回环/本机 IP 打开的 UI 默认不订阅音频与鼠标/键盘流：
		// 它们从不消费这类消息，却会驱动本机捕获引擎 60Hz 空转。
		// 前端会在真正需要时发送 set_mapping 动态开启；远端保持默认全开以兼容旧版。
		local := util.IsLocalIP(r.RemoteAddr)
		client := NewClient(s.hub, conn, r.RemoteAddr, r.UserAgent(), local)
		s.hub.register <- client
		if s.discovery != nil && !local {
			s.discovery.RegisterInboundClient(client.id, r.RemoteAddr, r.UserAgent())
		}

		go client.writePump()
		go client.readPump()
	})

	// QR Code PNG endpoint (dynamically updates when user switches network card)
	mux.HandleFunc("/api/qrcode", func(w http.ResponseWriter, r *http.Request) {
		currentURL := s.GetCurrentURL()
		code, err := qr.Encode(currentURL, qr.M)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Write(code.PNG())
	})

	// Network interfaces listing API
	mux.HandleFunc("/api/interfaces", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		allIPs := util.GetAllIPs()
		resp := map[string]interface{}{
			"current_ip":  s.GetCurrentIP(),
			"current_url": s.GetCurrentURL(),
			"interfaces":  allIPs,
		}
		json.NewEncoder(w).Encode(resp)
	})

	// Network interface switch/select API
	mux.HandleFunc("/api/interfaces/select", func(w http.ResponseWriter, r *http.Request) {
		// 仅允许 POST：GET 会被网页 <img> 直接触发（CSRF 可打乱通信地址）
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		targetIP := r.URL.Query().Get("ip")
		if targetIP == "" {
			var body struct {
				IP string `json:"ip"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				targetIP = body.IP
			}
		}

		if targetIP == "" {
			http.Error(w, "missing ip parameter", http.StatusBadRequest)
			return
		}

		// 只接受本机真实网卡地址：防任意字符串打乱 QR 码/beacon 广播
		if net.ParseIP(targetIP) == nil || !isLocalInterfaceIP(targetIP) {
			http.Error(w, "invalid ip parameter", http.StatusBadRequest)
			return
		}

		s.SetCurrentIP(targetIP)

		resp := map[string]interface{}{
			"status":      "ok",
			"current_ip":  s.GetCurrentIP(),
			"current_url": s.GetCurrentURL(),
		}
		json.NewEncoder(w).Encode(resp)
	})

	// Unified Peer Discovery & Mapping APIs (发现的所有对端与自主映射)
	mux.HandleFunc("/api/peers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 关键修复：按“查看者”过滤。手机/外部浏览器接入后会被登记为入站对端，
		// 不按请求来源剔除的话，它打开 UI 时会在设备列表里看到自己。
		peers := s.peerMgr.ListPeers(viewerHost(r.RemoteAddr))
		json.NewEncoder(w).Encode(map[string]interface{}{
			"peers": peers,
			"total": len(peers),
		})
	})

	mux.HandleFunc("/api/peers/add", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			Target string `json:"target"`
			Name   string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Target == "" {
			http.Error(w, "目标地址不能为空", http.StatusBadRequest)
			return
		}
		peer, err := s.peerMgr.AddManualPeer(body.Target, body.Name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"peer":    peer,
		})
	})

	mux.HandleFunc("/api/peers/rename", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			PeerID string `json:"peer_id"`
			Name   string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.PeerID == "" || body.Name == "" {
			http.Error(w, "缺少 peer_id 或设备名称", http.StatusBadRequest)
			return
		}
		err := s.peerMgr.RenamePeer(body.PeerID, body.Name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	})

	mux.HandleFunc("/api/peers/map", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			PeerID          string `json:"peer_id"`
			PlaySystemAudio *bool  `json:"play_system_audio"`
			PlayMicAudio    *bool  `json:"play_mic_audio"`
			PlayAudio       *bool  `json:"play_audio"` // fallback
			ExecuteMouse    *bool  `json:"execute_mouse"`
			ExecuteKeyboard *bool  `json:"execute_keyboard"`
			RemoteControl   *bool  `json:"remote_control"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.PeerID == "" {
			http.Error(w, "缺少 peer_id", http.StatusBadRequest)
			return
		}
		playSys := body.PlaySystemAudio
		if playSys == nil && body.PlayAudio != nil {
			playSys = body.PlayAudio
		}
		err := s.peerMgr.SetMapping(body.PeerID, playSys, body.PlayMicAudio, body.ExecuteMouse, body.ExecuteKeyboard)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	})

	// Self node status & capabilities sharing configuration
	// 前端自检上报：页面内 JS 把 WebSocket 连接状态/错误 POST 回来，
	// 写入日志（桌面窗口无控制台/DevTools 时排查前端连接问题的唯一通道）
	mux.HandleFunc("/api/client-report", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Event  string `json:"event"`
			Detail string `json:"detail"`
			URL    string `json:"url"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		util.Logf("[前端自检] %s %s (ws=%s, ua=%s)", body.Event, body.Detail, body.URL, r.UserAgent())
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})

	mux.HandleFunc("/api/self", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var body struct {
				Name        *string `json:"name"`
				SystemAudio *bool   `json:"system_audio"`
				MicAudio    *bool   `json:"mic_audio"`
				Audio       *bool   `json:"audio"` // fallback
				Mouse       *bool   `json:"mouse"`
				Keyboard    *bool   `json:"keyboard"`
				Control     *bool   `json:"control"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			cur := s.discovery.GetProvides()
			sysAudioVal := cur.SystemAudio
			if body.SystemAudio != nil {
				sysAudioVal = *body.SystemAudio
			} else if body.Audio != nil {
				sysAudioVal = *body.Audio
			}
			micAudioVal := cur.MicAudio
			if body.MicAudio != nil {
				micAudioVal = *body.MicAudio
			}
			mouseVal := cur.Mouse
			if body.Mouse != nil {
				mouseVal = *body.Mouse
			}
			keyboardVal := cur.Keyboard
			if body.Keyboard != nil {
				keyboardVal = *body.Keyboard
			}
			ctrlVal := cur.Control
			if body.Control != nil {
				ctrlVal = *body.Control
			}
			if body.Name != nil {
				s.discovery.SetName(*body.Name)
			}
			sysDev := "系统扬声器输出"
			if s.systemAudioEngine != nil {
				sysDev = s.systemAudioEngine.DeviceName()
			}
			micDev := "麦克风输入"
			if s.micAudioEngine != nil {
				micDev = s.micAudioEngine.DeviceName()
			}
			s.discovery.UpdateProvides(sysAudioVal, micAudioVal, mouseVal, keyboardVal, ctrlVal, sysDev, micDev, audioFormatOf())
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
			return
		}

		sysDev := "未开启"
		if s.systemAudioEngine != nil {
			sysDev = s.systemAudioEngine.DeviceName()
		}
		micDev := "未开启"
		if s.micAudioEngine != nil {
			micDev = s.micAudioEngine.DeviceName()
		}
		resp := map[string]interface{}{
			"id":                  s.discovery.ID(),
			"name":                s.discovery.Name(),
			"os":                  runtime.GOOS,
			"audio_format":        audioFormatOf(),
			"current_ip":          s.GetCurrentIP(),
			"port":                s.port,
			"primary_url":         s.GetCurrentURL(),
			"provides":            s.discovery.GetProvides(),
			"interfaces":          util.GetAllIPs(),
			"system_audio_device": sysDev,
			"mic_device":          micDev,
			"audio_device":        sysDev,
			"build_time":          util.BuildTime,
			"log_path":            util.LogFilePath(),
		}
		json.NewEncoder(w).Encode(resp)
	})

	// Status API
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		sysDev := "默认音频输出"
		if s.systemAudioEngine != nil {
			sysDev = s.systemAudioEngine.DeviceName()
		}
		micDev := "麦克风输入"
		if s.micAudioEngine != nil {
			micDev = s.micAudioEngine.DeviceName()
		}
		audioSubs, mouseSubs, keyboardSubs := s.hub.SubscriberCounts()
		status := map[string]interface{}{
			"audio_subs":          audioSubs,
			"audio_pcm_clients":   s.hub.PCMAudioClientCount(),
			"mouse_subs":          mouseSubs,
			"keyboard_subs":       keyboardSubs,
			"clients":             s.hub.ClientCount(),
			"inbound_count":       s.hub.ClientCount(),
			"peers_count":         len(s.peerMgr.ListPeers("")),
			"bytes_sent":          s.hub.TotalBytesSent(),
			"sample_rate":         audio.SampleRate,
			"channels":            audio.Channels,
			"device_name":         sysDev,
			"system_audio_device": sysDev,
			"mic_device":          micDev,
			"current_ip":          s.GetCurrentIP(),
			"primary_url":         s.GetCurrentURL(),
			"mouse_active":        true,
			"keyboard_active":     true,
		}
		json.NewEncoder(w).Encode(status)
	})

	// Device List API
	mux.HandleFunc("/api/devices", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		devices, err := audio.ListAllDevices()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(devices)
	})

	// Embedded Static UI（禁缓存包装：入口 HTML 引用的 JS 带内容 hash，
	// 每次发版 hash 变化；若浏览器缓存旧 HTML，会引用已不存在的旧产物导致白屏）
	staticFS, err := web.GetFileSystem()
	if err == nil {
		mux.Handle("/", noCache(http.FileServer(staticFS)))
	}

	return limitBody(mux)
}

// noCache 为静态响应附加禁缓存头：局域网服务体量小，正确性优先于缓存收益。
func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	return http.ListenAndServe(addr, s.SetupRoutes())
}

// audioFormatOf 返回本机音频链路的协议标识（唯一事实来源在 internal/audio）。
// 捕获链路统一输出 Opus，与引擎是否已创建无关，故不再依赖引擎实例。
func audioFormatOf() string {
	return audio.ProtocolFormatOpus
}
