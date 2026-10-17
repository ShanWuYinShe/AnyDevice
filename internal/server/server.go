package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"any_device/internal/audio"
	"any_device/internal/web"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		// Allow any origin on LAN
		return true
	},
}

type Server struct {
	port   int
	hub    *Hub
	engine *audio.Engine
}

func NewServer(port int, hub *Hub, engine *audio.Engine) *Server {
	return &Server{
		port:   port,
		hub:    hub,
		engine: engine,
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	// WebSocket audio stream endpoint
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			fmt.Printf("[WS] Upgrade error: %v\n", err)
			return
		}

		client := &Client{
			hub:      s.hub,
			conn:     conn,
			send:     make(chan []byte, 32),
			remoteIP: r.RemoteAddr,
			joinedAt: time.Now(),
		}

		s.hub.register <- client

		go client.writePump()
		go client.readPump()
	})

	// Status API
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := map[string]interface{}{
			"clients":     s.hub.ClientCount(),
			"bytes_sent":  s.hub.TotalBytesSent(),
			"sample_rate": audio.SampleRate,
			"channels":    audio.Channels,
			"device_name": s.engine.DeviceName(),
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
