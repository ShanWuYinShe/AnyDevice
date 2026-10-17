package server

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Client represents a connected browser or receiver client.
type Client struct {
	hub      *Hub
	conn     *websocket.Conn
	send     chan []byte
	remoteIP string
	joinedAt time.Time
}

// Hub maintains the set of active clients and broadcasts messages to them.
type Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	mu         sync.RWMutex

	totalBytesSent uint64
	clientCount    int64
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte, 128),
		register:   make(chan *Client),
		unregister: make(chan *Client),
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

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				atomic.StoreInt64(&h.clientCount, int64(len(h.clients)))
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- message:
					atomic.AddUint64(&h.totalBytesSent, uint64(len(message)))
				default:
					// If client channel is full (network lag/slow receiver), drop chunk to avoid latency build-up
				}
			}
			h.mu.RUnlock()
		}
	}
}

// BroadcastChunk pushes an audio chunk to all connected clients.
func (h *Hub) BroadcastChunk(chunk []byte) {
	select {
	case h.broadcast <- chunk:
	default:
		// Hub broadcast buffer full, drop to prevent stalling audio capture thread
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
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if !ok {
				// The hub closed the channel
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.BinaryMessage)
			if err != nil {
				return
			}
			w.Write(message)

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

	c.conn.SetReadLimit(512)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		// Read control messages (e.g. ping/pong or client stats)
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
	}
}
