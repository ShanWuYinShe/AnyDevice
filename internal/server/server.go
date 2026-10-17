package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

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
	mu          sync.RWMutex
}

func NewServer(port int, initialIP string, hub *Hub, audioEngine *audio.Engine, mouseEngine *mouse.Engine) *Server {
	return &Server{
		port:        port,
		currentIP:   initialIP,
		hub:         hub,
		audioEngine: audioEngine,
		mouseEngine: mouseEngine,
	}
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

		client := &Client{
			hub:          s.hub,
			conn:         conn,
			send:         make(chan OutMessage, 64),
			remoteIP:     r.RemoteAddr,
			joinedAt:     time.Now(),
			audioEnabled: true,
			mouseEnabled: false,
		}

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
			"clients":      s.hub.ClientCount(),
			"bytes_sent":   s.hub.TotalBytesSent(),
			"sample_rate":  audio.SampleRate,
			"channels":     audio.Channels,
			"device_name":  s.audioEngine.DeviceName(),
			"current_ip":   s.GetCurrentIP(),
			"primary_url":  s.GetCurrentURL(),
			"mouse_active": true,
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

	// Embedded Static UI
	staticFS, err := web.GetFileSystem()
	if err != nil {
		return fmt.Errorf("failed to load embedded static filesystem: %w", err)
	}
	mux.Handle("/", http.FileServer(staticFS))

	addr := fmt.Sprintf(":%d", s.port)
	return http.ListenAndServe(addr, mux)
}
