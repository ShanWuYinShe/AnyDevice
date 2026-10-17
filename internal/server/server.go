package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"any_device/internal/audio"
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
	port        int
	currentIP   string
	hub         *Hub
	audioEngine *audio.Engine
	mouseEngine *mouse.Engine
	outboundMgr *OutboundManager
	roleMode    string // "provider", "receiver", "bidirectional"
	mu          sync.RWMutex
}

func NewServer(port int, initialIP string, hub *Hub, audioEngine *audio.Engine, mouseEngine *mouse.Engine) *Server {
	return &Server{
		port:        port,
		currentIP:   initialIP,
		hub:         hub,
		audioEngine: audioEngine,
		mouseEngine: mouseEngine,
		outboundMgr: NewOutboundManager(hub),
		roleMode:    "bidirectional",
	}
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

func (s *Server) Start() error {
	mux := http.NewServeMux()

	// WebSocket unified stream endpoint (audio binary + mouse JSON text)
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			fmt.Printf("[WS] Upgrade error: %v\n", err)
			return
		}

		client := NewClient(s.hub, conn, r.RemoteAddr, r.UserAgent())
		s.hub.register <- client

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

	// Status API
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := map[string]interface{}{
			"clients":        s.hub.ClientCount(),
			"inbound_count":  s.hub.ClientCount(),
			"outbound_count": len(s.outboundMgr.ListPeers()),
			"role_mode":      s.GetRoleMode(),
			"bytes_sent":     s.hub.TotalBytesSent(),
			"sample_rate":    audio.SampleRate,
			"channels":       audio.Channels,
			"device_name":    s.audioEngine.DeviceName(),
			"current_ip":     s.GetCurrentIP(),
			"primary_url":    s.GetCurrentURL(),
			"mouse_active":   true,
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
			AllowControl *bool  `json:"allow_control"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ok := s.hub.UpdateInboundClient(body.ID, body.Audio, body.Mouse, body.AllowControl)
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

	// Embedded Static UI
	staticFS, err := web.GetFileSystem()
	if err != nil {
		return fmt.Errorf("failed to load embedded static filesystem: %w", err)
	}
	mux.Handle("/", http.FileServer(staticFS))

	addr := fmt.Sprintf(":%d", s.port)
	return http.ListenAndServe(addr, mux)
}
