package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"any_device/internal/audio"
	"any_device/internal/mouse"
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
	primaryURL  string
	hub         *Hub
	audioEngine *audio.Engine
	mouseEngine *mouse.Engine
}

func NewServer(port int, primaryURL string, hub *Hub, audioEngine *audio.Engine, mouseEngine *mouse.Engine) *Server {
	return &Server{
		port:        port,
		primaryURL:  primaryURL,
		hub:         hub,
		audioEngine: audioEngine,
		mouseEngine: mouseEngine,
	}
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
			audioEnabled: true,  // Default: audio enabled
			mouseEnabled: false, // Default: mouse disabled until user toggles on UI
		}

		s.hub.register <- client

		go client.writePump()
		go client.readPump()
	})

	// QR Code PNG endpoint for native desktop UI and browser rendering
	mux.HandleFunc("/api/qrcode", func(w http.ResponseWriter, r *http.Request) {
		code, err := qr.Encode(s.primaryURL, qr.M)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(code.PNG())
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
			"primary_url":  s.primaryURL,
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
