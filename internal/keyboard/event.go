package keyboard

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// KeyEvent represents a key press or release event.
type KeyEvent struct {
	Type      string `json:"type"`      // "keyboard"
	Action    string `json:"action"`    // "down", "up"
	Key       string `json:"key"`       // Standard key code (e.g., "KeyA", "Enter")
	Name      string `json:"name"`      // Friendly name (e.g., "A", "回车 (Enter)")
	KeyCode   int    `json:"key_code"`  // Platform native key code
	Timestamp int64  `json:"timestamp"` // Unix millisecond timestamp
}

// Engine captures local host keyboard events and emits them to a callback.
type Engine struct {
	onEvent   func(KeyEvent)
	cancel    context.CancelFunc
	mu        sync.Mutex
	isRunning bool

	subscribers int64

	// Track previously pressed keys: standardKey -> nativeCode
	activeKeys map[string]int
}

func NewEngine(onEvent func(KeyEvent)) *Engine {
	return &Engine{
		onEvent:    onEvent,
		activeKeys: make(map[string]int),
	}
}

func (e *Engine) SetSubscribers(count int64) {
	atomic.StoreInt64(&e.subscribers, count)
}

func (e *Engine) Start() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.isRunning {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.isRunning = true

	go e.loop(ctx)
}

func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.isRunning {
		return
	}

	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	e.isRunning = false
}

func (e *Engine) loop(ctx context.Context) {
	ticker := time.NewTicker(16 * time.Millisecond) // ~60 Hz
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// If no clients are listening for keyboard events, reduce polling frequency
			if atomic.LoadInt64(&e.subscribers) <= 0 {
				time.Sleep(100 * time.Millisecond)
				continue
			}

			currentPressed := getPressedKeys()
			now := time.Now().UnixMilli()

			// Check newly pressed keys (key down)
			for key, code := range currentPressed {
				if _, existed := e.activeKeys[key]; !existed {
					e.activeKeys[key] = code
					if e.onEvent != nil {
						e.onEvent(KeyEvent{
							Type:      "keyboard",
							Action:    "down",
							Key:       key,
							Name:      FriendlyKeyName(key),
							KeyCode:   code,
							Timestamp: now,
						})
					}
				}
			}

			// Check released keys (key up)
			for key, code := range e.activeKeys {
				if _, stillPressed := currentPressed[key]; !stillPressed {
					delete(e.activeKeys, key)
					if e.onEvent != nil {
						e.onEvent(KeyEvent{
							Type:      "keyboard",
							Action:    "up",
							Key:       key,
							Name:      FriendlyKeyName(key),
							KeyCode:   code,
							Timestamp: now,
						})
					}
				}
			}
		}
	}
}
