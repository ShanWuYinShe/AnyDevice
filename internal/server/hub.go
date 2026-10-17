package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"any_device/internal/mouse"

	"github.com/gorilla/websocket"
)

type OutMessage struct {
	isBinary bool
	data     []byte
}

// InboundClientInfo represents public status of a connected client.
type InboundClientInfo struct {
	ID           string `json:"id"`
	RemoteAddr   string `json:"remote_addr"`
	RemoteIP     string `json:"remote_ip"`
	UserAgent    string `json:"user_agent"`
	DeviceType   string `json:"device_type"`
	ConnectedAt  string `json:"connected_at"`
	DurationSec  int64  `json:"duration_sec"`
	AudioEnabled bool   `json:"audio_enabled"`
	MouseEnabled bool   `json:"mouse_enabled"`
	AllowControl bool   `json:"allow_control"`
}

// Client represents a connected browser or receiver client.
type Client struct {
	id           string
	hub          *Hub
	conn         *websocket.Conn
	send         chan OutMessage
	remoteIP     string
	remoteAddr   string
	userAgent    string
	deviceType   string
	joinedAt     time.Time
	audioEnabled bool
	mouseEnabled bool
	allowControl bool
	mu           sync.RWMutex
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
	clients        map[*Client]bool
	audioBroadcast chan []byte
	mouseBroadcast chan []byte
	register       chan *Client
	unregister     chan *Client
	updateSubs     chan struct{}
	mu             sync.RWMutex

	totalBytesSent uint64
	clientCount    int64
	onSubsChanged  func(audioSubs, mouseSubs int)
}

func NewHub(onSubsChanged func(audioSubs, mouseSubs int)) *Hub {
	return &Hub{
		clients:        make(map[*Client]bool),
		audioBroadcast: make(chan []byte, 128),
		mouseBroadcast: make(chan []byte, 64),
		register:       make(chan *Client),
		unregister:     make(chan *Client),
		updateSubs:     make(chan struct{}, 8),
		onSubsChanged:  onSubsChanged,
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

		case audioChunk := <-h.audioBroadcast:
			msg := OutMessage{isBinary: true, data: audioChunk}
			h.mu.RLock()
			for client := range h.clients {
				client.mu.RLock()
				enabled := client.audioEnabled
				client.mu.RUnlock()

				if enabled {
					select {
					case client.send <- msg:
						atomic.AddUint64(&h.totalBytesSent, uint64(len(audioChunk)))
					default:
						// Drop chunk on laggy client to prevent audio latency accumulation
					}
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
		}
	}
}

func (h *Hub) notifySubscribers() {
	h.mu.RLock()
	var audioCount, mouseCount int
	for client := range h.clients {
		client.mu.RLock()
		if client.audioEnabled {
			audioCount++
		}
		if client.mouseEnabled {
			mouseCount++
		}
		client.mu.RUnlock()
	}
	h.mu.RUnlock()

	if h.onSubsChanged != nil {
		h.onSubsChanged(audioCount, mouseCount)
	}
}

// BroadcastAudio pushes an audio PCM chunk to subscribed clients.
func (h *Hub) BroadcastAudio(chunk []byte) {
	select {
	case h.audioBroadcast <- chunk:
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

// ClientCount returns current connected client count.
func (h *Hub) ClientCount() int {
	return int(atomic.LoadInt64(&h.clientCount))
}

// TotalBytesSent returns accumulated bytes sent.
func (h *Hub) TotalBytesSent() uint64 {
	return atomic.LoadUint64(&h.totalBytesSent)
}

// NewClient constructs a new connected client.
func NewClient(hub *Hub, conn *websocket.Conn, remoteAddr, userAgent string) *Client {
	remoteIP := remoteAddr
	if idx := strings.LastIndex(remoteAddr, ":"); idx != -1 {
		remoteIP = remoteAddr[:idx]
	}
	return &Client{
		id:           fmt.Sprintf("client-%d", atomic.AddUint64(&clientSeq, 1)),
		hub:          hub,
		conn:         conn,
		send:         make(chan OutMessage, 64),
		remoteIP:     remoteIP,
		remoteAddr:   remoteAddr,
		userAgent:    userAgent,
		deviceType:   ParseDeviceType(userAgent),
		joinedAt:     time.Now(),
		audioEnabled: true,
		mouseEnabled: true,
		allowControl: true,
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
			ID:           c.id,
			RemoteAddr:   c.remoteAddr,
			RemoteIP:     c.remoteIP,
			UserAgent:    c.userAgent,
			DeviceType:   c.deviceType,
			ConnectedAt:  c.joinedAt.Format("15:04:05"),
			DurationSec:  int64(now.Sub(c.joinedAt).Seconds()),
			AudioEnabled: c.audioEnabled,
			MouseEnabled: c.mouseEnabled,
			AllowControl: c.allowControl,
		}
		c.mu.RUnlock()
		result = append(result, info)
	}
	return result
}

// UpdateInboundClient updates permissions for a specific client.
func (h *Hub) UpdateInboundClient(id string, audio *bool, mouse *bool, allowControl *bool) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.clients {
		if c.id == id {
			c.mu.Lock()
			if audio != nil {
				c.audioEnabled = *audio
			}
			if mouse != nil {
				c.mouseEnabled = *mouse
			}
			if allowControl != nil {
				c.allowControl = *allowControl
			}
			c.mu.Unlock()

			select {
			case h.updateSubs <- struct{}{}:
			default:
			}
			return true
		}
	}
	return false
}

// DisconnectInboundClient kicks a client.
func (h *Hub) DisconnectInboundClient(id string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.clients {
		if c.id == id {
			if c.conn != nil {
				c.conn.Close()
			}
			return true
		}
	}
	return false
}

// UpdateAllInboundClients updates permissions across all connected inbound clients.
func (h *Hub) UpdateAllInboundClients(audio *bool, mouse *bool, allowControl *bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.clients {
		c.mu.Lock()
		if audio != nil {
			c.audioEnabled = *audio
		}
		if mouse != nil {
			c.mouseEnabled = *mouse
		}
		if allowControl != nil {
			c.allowControl = *allowControl
		}
		c.mu.Unlock()
	}

	select {
	case h.updateSubs <- struct{}{}:
	default:
	}
}

// DisconnectAllInboundClients disconnects all connected inbound clients.
func (h *Hub) DisconnectAllInboundClients() {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.clients {
		if c.conn != nil {
			c.conn.Close()
		}
	}
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
				Action string `json:"action"` // "set_mapping" or "remote_input"
				Audio  *bool  `json:"audio"`
				Mouse  *bool  `json:"mouse"`
				// Reverse input control fields
				Type string `json:"type"` // "move", "click", "button", "scroll"
				Dx   int    `json:"dx"`
				Dy   int    `json:"dy"`
				Btn  string `json:"btn"` // "left", "right", "middle"
				Down bool   `json:"down"`
			}
			if err := json.Unmarshal(p, &cmd); err != nil {
				continue
			}

			if cmd.Action == "set_mapping" {
				c.mu.Lock()
				if cmd.Audio != nil {
					c.audioEnabled = *cmd.Audio
				}
				if cmd.Mouse != nil {
					c.mouseEnabled = *cmd.Mouse
				}
				c.mu.Unlock()

				select {
				case c.hub.updateSubs <- struct{}{}:
				default:
				}
			} else if cmd.Action == "remote_input" {
				c.mu.RLock()
				allowed := c.allowControl
				c.mu.RUnlock()
				if !allowed {
					continue
				}

				// Reverse control: client controls host mouse!
				switch cmd.Type {
				case "move":
					mouse.SimulateMove(cmd.Dx, cmd.Dy)
				case "click":
					mouse.SimulateButton(cmd.Btn, true)
					time.Sleep(15 * time.Millisecond)
					mouse.SimulateButton(cmd.Btn, false)
				case "button":
					mouse.SimulateButton(cmd.Btn, cmd.Down)
				case "scroll":
					mouse.SimulateScroll(cmd.Dy)
				}
			}
		}
	}
}
