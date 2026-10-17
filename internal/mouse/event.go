package mouse

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"any_device/internal/util"
)

// MouseEvent represents a normalized cursor movement or button event.
type MouseEvent struct {
	Type         string  `json:"type"`                  // "mouse"
	Action       string  `json:"action"`                // "move", "down", "up", "wheel"
	X            float64 `json:"x"`                     // Normalized X (0.0 ~ 1.0)
	Y            float64 `json:"y"`                     // Normalized Y (0.0 ~ 1.0)
	RawX         int     `json:"raw_x"`                 // Host screen pixel X
	RawY         int     `json:"raw_y"`                 // Host screen pixel Y
	ScreenWidth  int     `json:"screen_w"`              // Host screen width
	ScreenHeight int     `json:"screen_h"`              // Host screen height
	LeftDown     bool    `json:"left_down"`             // Left button state
	RightDown    bool    `json:"right_down"`            // Right button state
	MiddleDown   bool    `json:"middle_down"`           // Middle button state
	WheelDelta   int     `json:"wheel_delta,omitempty"` // 滚轮增量（正=向上），仅 Action=="wheel" 时有效
	Timestamp    int64   `json:"timestamp"`             // Unix millisecond timestamp
}

// Engine captures local host mouse events and emits them to a callback.
// 捕获优先使用平台事件钩子（macOS CGEventTap / Windows 低级鼠标钩子，
// 参照 Deskflow 的事件驱动模型：实时、支持滚轮、空闲零开销），
// 未授权或安装失败时回退为光标位置轮询模式。
type Engine struct {
	onEvent   func(MouseEvent)
	cancel    context.CancelFunc
	mu        sync.Mutex
	isRunning bool

	// Active subscriber count (when 0, capture can sleep to save CPU)
	subscribers int64

	lastX, lastY        int
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

// Start begins tracking the host mouse cursor.
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
	// emit 统一承载订阅门控与调试日志
	emit := func(evt MouseEvent) {
		if atomic.LoadInt64(&e.subscribers) <= 0 {
			return
		}
		if util.DebugKeys() && evt.Action != "move" {
			fmt.Printf("[鼠标捕获] %s (%.3f, %.3f)\n", evt.Action, evt.X, evt.Y)
		}
		if e.onEvent != nil {
			e.onEvent(evt)
		}
	}

	// 事件驱动优先：钩子回调本身做注入回环过滤，move 事件在钩子侧已合并节流
	if runEventCapture(ctx, emit) {
		<-ctx.Done()
		return
	}

	// 回退：轮询光标位置与按钮差分（macOS 未授权时 CGEventTap 安装失败）
	ticker := time.NewTicker(16 * time.Millisecond) // ~60 FPS
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// If no clients are listening for mouse events, reduce polling frequency
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

			emit(evt)
		}
	}
}
