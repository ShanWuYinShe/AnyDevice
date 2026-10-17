package server

import (
	"encoding/json"
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

// Client represents a connected browser or receiver client.
type Client struct {
	hub          *Hub
	conn         *websocket.Conn
	send         chan OutMessage
	remoteIP     string
	joinedAt     time.Time
	audioEnabled bool
	mouseEnabled bool
	mu           sync.RWMutex
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
