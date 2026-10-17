package mouse

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// MouseEvent represents a normalized cursor movement or button event.
type MouseEvent struct {
	Type       string  `json:"type"`        // "mouse"
	Action     string  `json:"action"`      // "move", "down", "up"
	X          float64 `json:"x"`           // Normalized X (0.0 ~ 1.0)
	Y          float64 `json:"y"`           // Normalized Y (0.0 ~ 1.0)
	RawX       int     `json:"raw_x"`       // Host screen pixel X
	RawY       int     `json:"raw_y"`       // Host screen pixel Y
	ScreenWidth int    `json:"screen_w"`    // Host screen width
	ScreenHeight int   `json:"screen_h"`    // Host screen height
	LeftDown   bool    `json:"left_down"`   // Left button state
	RightDown  bool    `json:"right_down"`  // Right button state
	MiddleDown bool    `json:"middle_down"` // Middle button state
	Timestamp  int64   `json:"timestamp"`   // Unix millisecond timestamp
}

// Engine captures local host mouse events and emits them to a callback.
type Engine struct {
	onEvent   func(MouseEvent)
	cancel    context.CancelFunc
	mu        sync.Mutex
	isRunning bool

	// Active subscriber count (when 0, polling can sleep to save CPU)
	subscribers int64

	lastX, lastY int
	lastL, lastR, lastM bool
}

func NewEngine(onEvent func(MouseEvent)) *Engine {
	return &Engine{
		onEvent: onEvent,
	}
}

// SetSubscribers updates active subscriber count.
func (e *Engine) SetSubscribers(count int64) {
	atomic.StoreInt64(&e.subscribers, count)
}

// Start begins tracking the host mouse cursor at ~60Hz.
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

// Stop stops mouse tracking.
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
	ticker := time.NewTicker(16 * time.Millisecond) // ~60 FPS
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// If no clients are listening for mouse, reduce polling frequency
			if atomic.LoadInt64(&e.subscribers) <= 0 {
				time.Sleep(100 * time.Millisecond)
				continue
			}

			screenW, screenH := getScreenSize()
			if screenW <= 0 {
				screenW = 1920
			}
			if screenH <= 0 {
				screenH = 1080
			}

			x, y, lDown, rDown, mDown := getCursor()

			// Check if anything changed
			posChanged := (x != e.lastX || y != e.lastY)
			btnChanged := (lDown != e.lastL || rDown != e.lastR || mDown != e.lastM)

			if !posChanged && !btnChanged {
				continue
			}

			action := "move"
			if btnChanged {
				if lDown || rDown || mDown {
					action = "down"
				} else {
					action = "up"
				}
			}

			// Calculate normalized coordinates clamped to 0.0 ~ 1.0
			normX := math.Max(0.0, math.Min(1.0, float64(x)/float64(screenW)))
			normY := math.Max(0.0, math.Min(1.0, float64(y)/float64(screenH)))

			evt := MouseEvent{
				Type:         "mouse",
				Action:       action,
				X:            normX,
				Y:            normY,
				RawX:         x,
				RawY:         y,
				ScreenWidth:  screenW,
				ScreenHeight: screenH,
				LeftDown:     lDown,
				RightDown:    rDown,
				MiddleDown:   mDown,
				Timestamp:    time.Now().UnixMilli(),
			}

			e.lastX, e.lastY = x, y
			e.lastL, e.lastR, e.lastM = lDown, rDown, mDown

			if e.onEvent != nil {
				e.onEvent(evt)
			}
		}
	}
}
