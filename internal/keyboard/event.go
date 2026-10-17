package keyboard

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"any_device/internal/util"
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
// 捕获优先使用平台事件钩子（macOS CGEventTap / Windows 低级键盘钩子，
// 参照 Deskflow 的事件驱动模型：实时、零丢键、空闲零开销），
// 未授权或安装失败时回退为轮询差分模式。
type Engine struct {
	onEvent   func(KeyEvent)
	cancel    context.CancelFunc
	mu        sync.Mutex
	isRunning bool

	subscribers int64

	// 轮询回退模式下的按键状态：标准键名 -> 平台键码
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
	// emit 统一承载订阅门控与调试日志：无订阅者时事件不外发
	emit := func(evt KeyEvent) {
		if atomic.LoadInt64(&e.subscribers) <= 0 {
			return
		}
		if util.DebugKeys() {
			fmt.Printf("[键盘捕获] %s %s (键码 %#x)\n", evt.Action, evt.Key, evt.KeyCode)
		}
		if e.onEvent != nil {
			e.onEvent(evt)
		}
	}

	// 事件驱动优先：钩子回调本身做注入回环过滤（不会捕获本应用模拟的按键）
	if runEventCapture(ctx, emit) {
		<-ctx.Done()
		return
	}

	// 回退：轮询差分（macOS 未授权时 CGEventTap 安装失败；无钩子实现的平台）
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
					emit(KeyEvent{
						Type:      "keyboard",
						Action:    "down",
						Key:       key,
						Name:      FriendlyKeyName(key),
						KeyCode:   code,
						Timestamp: now,
					})
				}
			}

			// Check released keys (key up)
			for key, code := range e.activeKeys {
				if _, stillPressed := currentPressed[key]; !stillPressed {
					delete(e.activeKeys, key)
					emit(KeyEvent{
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
