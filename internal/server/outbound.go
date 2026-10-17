package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// OutboundPeer represents an active connection to a remote AnyDevice host.
type OutboundPeer struct {
	ID           string `json:"id"`
	Target       string `json:"target"`
	Status       string `json:"status"` // "connected", "connecting", "disconnected", "error"
	LastError    string `json:"last_error,omitempty"`
	ConnectedAt  string `json:"connected_at"`
	DurationSec  int64  `json:"duration_sec"`
	ReceiveAudio bool   `json:"receive_audio"`
	ReceiveMouse bool   `json:"receive_mouse"`
	SendMouse    bool   `json:"send_mouse"`
	LatencyMs    int64  `json:"latency_ms"`

	conn      *websocket.Conn
	cancel    context.CancelFunc
	startTime time.Time
	mu        sync.RWMutex
}

// OutboundManager manages outgoing connections to other AnyDevice instances.
type OutboundManager struct {
	peers   map[string]*OutboundPeer
	hub     *Hub
	peerSeq uint64
	mu      sync.RWMutex
}

func NewOutboundManager(hub *Hub) *OutboundManager {
	return &OutboundManager{
		peers: make(map[string]*OutboundPeer),
		hub:   hub,
	}
}

// normalizeTarget formats an input like "192.168.1.5" or "192.168.1.5:8080" into ws://.../ws.
func normalizeTarget(raw string) (wsURL string, displayTarget string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("目标地址不能为空")
	}

	// Remove leading protocols if present for clean parsing
	clean := raw
	clean = strings.TrimPrefix(clean, "ws://")
	clean = strings.TrimPrefix(clean, "wss://")
	clean = strings.TrimPrefix(clean, "http://")
	clean = strings.TrimPrefix(clean, "https://")
	clean = strings.TrimSuffix(clean, "/ws")
	clean = strings.TrimSuffix(clean, "/")

	// If missing port, default to 8080
	if !strings.Contains(clean, ":") {
		clean = clean + ":8080"
	}

	u := url.URL{
		Scheme: "ws",
		Host:   clean,
		Path:   "/ws",
	}

	return u.String(), clean, nil
}

// Connect starts an outbound connection to another AnyDevice instance.
func (m *OutboundManager) Connect(rawTarget string, receiveAudio, receiveMouse, sendMouse bool) (*OutboundPeer, error) {
	wsURL, displayTarget, err := normalizeTarget(rawTarget)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	// Check if already connected or connecting to this target
	for _, p := range m.peers {
		if p.Target == displayTarget && (p.Status == "connected" || p.Status == "connecting") {
			m.mu.Unlock()
			return p, nil
		}
	}

	id := fmt.Sprintf("out-%d", atomic.AddUint64(&m.peerSeq, 1))
	ctx, cancel := context.WithCancel(context.Background())

	peer := &OutboundPeer{
		ID:           id,
		Target:       displayTarget,
		Status:       "connecting",
		ConnectedAt:  time.Now().Format("15:04:05"),
		ReceiveAudio: receiveAudio,
		ReceiveMouse: receiveMouse,
		SendMouse:    sendMouse,
		cancel:       cancel,
		startTime:    time.Now(),
	}
	m.peers[id] = peer
	m.mu.Unlock()

	go m.runPeer(ctx, peer, wsURL)

	return peer, nil
}

func (m *OutboundManager) runPeer(ctx context.Context, peer *OutboundPeer, wsURL string) {
	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		peer.mu.Lock()
		peer.Status = "error"
		peer.LastError = fmt.Sprintf("连接失败: %v", err)
		peer.mu.Unlock()
		return
	}

	peer.mu.Lock()
	peer.conn = conn
	peer.Status = "connected"
	peer.LastError = ""
	peer.startTime = time.Now()
	peer.ConnectedAt = peer.startTime.Format("15:04:05")
	peer.mu.Unlock()

	defer func() {
		conn.Close()
		peer.mu.Lock()
		if peer.Status != "disconnected" {
			peer.Status = "disconnected"
		}
		peer.mu.Unlock()
	}()

	// Send initial handshake
	handshake := map[string]interface{}{
		"action": "set_mapping",
		"audio":  peer.ReceiveAudio,
		"mouse":  peer.ReceiveMouse,
	}
	_ = conn.WriteJSON(handshake)

	// Ping loop for latency measurement
	pingTicker := time.NewTicker(4 * time.Second)
	defer pingTicker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-pingTicker.C:
				peer.mu.RLock()
				c := peer.conn
				peer.mu.RUnlock()
				if c == nil {
					return
				}
				start := time.Now()
				c.SetWriteDeadline(time.Now().Add(3 * time.Second))
				if err := c.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
				peer.mu.Lock()
				peer.LatencyMs = time.Since(start).Milliseconds()
				if peer.LatencyMs < 1 {
					peer.LatencyMs = 1
				}
				peer.mu.Unlock()
			}
		}
	}()

	conn.SetPongHandler(func(string) error {
		return nil
	})

	// Reading loop
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msgType, data, err := conn.ReadMessage()
		if err != nil {
			peer.mu.Lock()
			peer.Status = "error"
			peer.LastError = fmt.Sprintf("连接中断: %v", err)
			peer.mu.Unlock()
			return
		}

		peer.mu.RLock()
		recvAudio := peer.ReceiveAudio
		recvMouse := peer.ReceiveMouse
		peer.mu.RUnlock()

		if msgType == websocket.BinaryMessage && recvAudio {
			// Forward remote audio chunk to local Hub so local UI Web Audio hears it
			m.hub.BroadcastAudio(data)
		} else if msgType == websocket.TextMessage && recvMouse {
			var obj map[string]interface{}
			if err := json.Unmarshal(data, &obj); err == nil {
				obj["source_peer"] = peer.Target
				obj["peer_id"] = peer.ID
				if enriched, err := json.Marshal(obj); err == nil {
					data = enriched
				}
			}
			// Forward remote mouse event JSON to local Hub so local UI radar tracks it
			m.hub.BroadcastMouse(data)
		}
	}
}

// Disconnect closes an active outbound peer connection.
func (m *OutboundManager) Disconnect(id string) bool {
	m.mu.Lock()
	peer, exists := m.peers[id]
	if !exists {
		m.mu.Unlock()
		return false
	}
	delete(m.peers, id)
	m.mu.Unlock()

	peer.mu.Lock()
	peer.Status = "disconnected"
	if peer.cancel != nil {
		peer.cancel()
	}
	if peer.conn != nil {
		peer.conn.Close()
	}
	peer.mu.Unlock()
	return true
}

// Update updates stream subscription for an outbound peer.
func (m *OutboundManager) Update(id string, receiveAudio, receiveMouse, sendMouse *bool) bool {
	m.mu.RLock()
	peer, exists := m.peers[id]
	m.mu.RUnlock()

	if !exists {
		return false
	}

	peer.mu.Lock()
	if receiveAudio != nil {
		peer.ReceiveAudio = *receiveAudio
	}
	if receiveMouse != nil {
		peer.ReceiveMouse = *receiveMouse
	}
	if sendMouse != nil {
		peer.SendMouse = *sendMouse
	}
	c := peer.conn
	handshake := map[string]interface{}{
		"action": "set_mapping",
		"audio":  peer.ReceiveAudio,
		"mouse":  peer.ReceiveMouse,
	}
	peer.mu.Unlock()

	if c != nil {
		_ = c.WriteJSON(handshake)
	}
	return true
}

// ListPeers returns current snapshot of all outbound peers.
func (m *OutboundManager) ListPeers() []*OutboundPeer {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	list := make([]*OutboundPeer, 0, len(m.peers))
	for _, p := range m.peers {
		p.mu.RLock()
		item := &OutboundPeer{
			ID:           p.ID,
			Target:       p.Target,
			Status:       p.Status,
			LastError:    p.LastError,
			ConnectedAt:  p.ConnectedAt,
			DurationSec:  int64(now.Sub(p.startTime).Seconds()),
			ReceiveAudio: p.ReceiveAudio,
			ReceiveMouse: p.ReceiveMouse,
			SendMouse:    p.SendMouse,
			LatencyMs:    p.LatencyMs,
		}
		p.mu.RUnlock()
		list = append(list, item)
	}
	return list
}

// UpdateAll updates all active outbound peers.
func (m *OutboundManager) UpdateAll(receiveAudio, receiveMouse, sendMouse *bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, peer := range m.peers {
		peer.mu.Lock()
		if receiveAudio != nil {
			peer.ReceiveAudio = *receiveAudio
		}
		if receiveMouse != nil {
			peer.ReceiveMouse = *receiveMouse
		}
		if sendMouse != nil {
			peer.SendMouse = *sendMouse
		}
		c := peer.conn
		handshake := map[string]interface{}{
			"action": "set_mapping",
			"audio":  peer.ReceiveAudio,
			"mouse":  peer.ReceiveMouse,
		}
		peer.mu.Unlock()

		if c != nil {
			_ = c.WriteJSON(handshake)
		}
	}
}

// DisconnectAll closes all active outbound peers.
func (m *OutboundManager) DisconnectAll() {
	m.mu.Lock()
	peers := make([]*OutboundPeer, 0, len(m.peers))
	for _, p := range m.peers {
		peers = append(peers, p)
	}
	m.peers = make(map[string]*OutboundPeer)
	m.mu.Unlock()

	for _, peer := range peers {
		peer.mu.Lock()
		peer.Status = "disconnected"
		if peer.cancel != nil {
			peer.cancel()
		}
		if peer.conn != nil {
			peer.conn.Close()
		}
		peer.mu.Unlock()
	}
}
