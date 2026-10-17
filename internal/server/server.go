package server

import (
	"encoding/json"
	"fmt"
	"net/http"
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
		// Allow any origin on LAN
		return true
	},
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
	outboundMgr       *OutboundManager
	roleMode          string // "provider", "receiver", "bidirectional"
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
		Audio:       systemAudioEngine != nil,
	})
	disc.Start()

	// 恢复持久化的本机名称与对端备注（设备改名重启不再丢失）
	cfg := util.LoadNodeConfig()
	if cfg.Name != "" {
		disc.SetName(cfg.Name)
	}
	if len(cfg.Aliases) > 0 {
		disc.SetAliases(cfg.Aliases)
	}
	disc.SetPersistHook(func(c util.NodeConfig) {
		if err := util.SaveNodeConfig(c); err != nil {
			fmt.Printf("[Discovery] 配置持久化失败 (可用环境变量 ANY_DEVICE_CONFIG 指定可写路径): %v\n", err)
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
		outboundMgr:       NewOutboundManager(hub),
		roleMode:          "bidirectional",
	}

	// 入站设备在线校准：以真实 WebSocket 连接集合为准，
	// 连接断开后最多 5 秒即在列表中显示离线（此前依赖 7 秒超时且重连会堆积条目）。
	go s.reconcileInboundLoop()

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

func (s *Server) SetRoleMode(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mode == "provider" || mode == "receiver" || mode == "bidirectional" {
		s.roleMode = mode
	}
}

func (s *Server) GetRoleMode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.roleMode
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

	mux.HandleFunc("/api/peers/control", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			PeerID string `json:"peer_id"`
			Action string `json:"action"`
			Type   string `json:"type"`
			Dx     int    `json:"dx"`
			Dy     int    `json:"dy"`
			Btn    string `json:"btn"`
			Down   bool   `json:"down"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.PeerID == "" {
			http.Error(w, "缺少 peer_id", http.StatusBadRequest)
			return
		}
		body.Action = "remote_input"
		err := s.peerMgr.SendRemoteControl(body.PeerID, body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	})

	// Self node status & capabilities sharing configuration
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
			s.discovery.UpdateProvides(sysAudioVal, micAudioVal, mouseVal, keyboardVal, ctrlVal, sysDev, micDev)
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
			"current_ip":          s.GetCurrentIP(),
			"port":                s.port,
			"primary_url":         s.GetCurrentURL(),
			"provides":            s.discovery.GetProvides(),
			"interfaces":          util.GetAllIPs(),
			"system_audio_device": sysDev,
			"mic_device":          micDev,
			"audio_device":        sysDev,
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
		status := map[string]interface{}{
			"clients":             s.hub.ClientCount(),
			"inbound_count":       s.hub.ClientCount(),
			"outbound_count":      len(s.outboundMgr.ListPeers()),
			"peers_count":         len(s.peerMgr.ListPeers("")),
			"role_mode":           s.GetRoleMode(),
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

	// Inbound Clients APIs (谁连接上了我)
	mux.HandleFunc("/api/inbound", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		clients := s.hub.GetInboundClients()
		resp := map[string]interface{}{
			"clients": clients,
			"total":   len(clients),
		}
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/inbound/update", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			ID           string `json:"id"`
			Audio        *bool  `json:"audio"`
			Mouse        *bool  `json:"mouse"`
			Keyboard     *bool  `json:"keyboard"`
			AllowControl *bool  `json:"allow_control"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ok := s.hub.UpdateInboundClient(body.ID, body.Audio, body.Mouse, body.Keyboard, body.AllowControl)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": ok})
	})

	mux.HandleFunc("/api/inbound/disconnect", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := r.URL.Query().Get("id")
		if id == "" {
			var body struct {
				ID string `json:"id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			id = body.ID
		}
		ok := s.hub.DisconnectInboundClient(id)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": ok})
	})

	// Outbound Peers APIs (我连接上了谁)
	mux.HandleFunc("/api/outbound", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		peers := s.outboundMgr.ListPeers()
		resp := map[string]interface{}{
			"peers": peers,
			"total": len(peers),
		}
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/outbound/connect", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			Target       string `json:"target"`
			ReceiveAudio bool   `json:"receive_audio"`
			ReceiveMouse bool   `json:"receive_mouse"`
			SendMouse    bool   `json:"send_mouse"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		peer, err := s.outboundMgr.Connect(body.Target, body.ReceiveAudio, body.ReceiveMouse, body.SendMouse)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "peer": peer})
	})

	mux.HandleFunc("/api/outbound/update", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			ID           string `json:"id"`
			ReceiveAudio *bool  `json:"receive_audio"`
			ReceiveMouse *bool  `json:"receive_mouse"`
			SendMouse    *bool  `json:"send_mouse"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ok := s.outboundMgr.Update(body.ID, body.ReceiveAudio, body.ReceiveMouse, body.SendMouse)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": ok})
	})

	mux.HandleFunc("/api/outbound/disconnect", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := r.URL.Query().Get("id")
		if id == "" {
			var body struct {
				ID string `json:"id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			id = body.ID
		}
		ok := s.outboundMgr.Disconnect(id)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": ok})
	})

	mux.HandleFunc("/api/inbound/batch", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			Audio        *bool `json:"audio"`
			Mouse        *bool `json:"mouse"`
			Keyboard     *bool `json:"keyboard"`
			AllowControl *bool `json:"allow_control"`
			Disconnect   bool  `json:"disconnect"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Disconnect {
			s.hub.DisconnectAllInboundClients()
		} else {
			s.hub.UpdateAllInboundClients(body.Audio, body.Mouse, body.Keyboard, body.AllowControl)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	})

	mux.HandleFunc("/api/outbound/batch", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			ReceiveAudio *bool `json:"receive_audio"`
			ReceiveMouse *bool `json:"receive_mouse"`
			SendMouse    *bool `json:"send_mouse"`
			Disconnect   bool  `json:"disconnect"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Disconnect {
			s.outboundMgr.DisconnectAll()
		} else {
			s.outboundMgr.UpdateAll(body.ReceiveAudio, body.ReceiveMouse, body.SendMouse)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	})

	// Mapping Role Mode API (接收别人的虚拟设备 / 把自己的设备映射成别人的虚拟设备)
	mux.HandleFunc("/api/mapping/mode", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var body struct {
				Mode string `json:"mode"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.Mode != "" {
				s.SetRoleMode(body.Mode)
			}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"mode": s.GetRoleMode(),
		})
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

	return mux
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
