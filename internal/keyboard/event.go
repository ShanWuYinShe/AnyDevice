package keyboard

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"any_device/internal/util"
)

// KeyEvent represents a key press or release event.
type KeyEvent struct {
	Type      string `json:"type"`               // "keyboard"
	Action    string `json:"action"`             // "down", "up"
	Key       string `json:"key"`                // Standard key code (e.g., "KeyA", "Enter")
	Name      string `json:"name"`               // Friendly name (e.g., "A", "回车 (Enter)")
	Friendly  string `json:"friendly,omitempty"` // 友好名称（兼容前端 msg.friendly）
	KeyCode   int    `json:"key_code"`           // Platform native key code
	Timestamp int64  `json:"timestamp"`          // Unix millisecond timestamp
}

// Engine captures local host keyboard events and emits them to a callback.
// 捕获优先使用平台事件钩子（macOS CGEventTap / Windows 低级键盘钩子，
// 参照 Deskflow 的事件驱动模型：实时、零丢键、空闲零开销），
// 未授权或安装失败时回退为轮询差分模式。
// activeSubs 是包级订阅数镜像：系统级钩子回调用它做最快路径门控。
var activeSubs int64

type Engine struct {
	onEvent   func(KeyEvent)
	cancel    context.CancelFunc
	mu        sync.Mutex
	isRunning bool
	done      chan struct{} // 捕获循环退出完成（Stop 等待，保证可安全重启）

	subscribers int64

	// enabled 对应 UI「本机共享」开关：关闭后即使仍有对端订阅也不安装钩子。
	enabled atomic.Bool

	// 按需启停：无人订阅时不安装系统级 WH_KEYBOARD_LL 钩子。
	// 与鼠标引擎同构：supervisor 独占 Start/Stop，订阅归零延迟 idleGrace 再停。
	supWake     chan struct{}
	supDone     chan struct{}
	supExited   chan struct{}
	supStarted  atomic.Bool
	supOnce     sync.Once
	supStopOnce sync.Once
	idleGrace   time.Duration

	// 轮询回退模式下的按键状态：标准键名 -> 平台键码
	activeKeys map[string]int
}

func NewEngine(onEvent func(KeyEvent)) *Engine {
	e := &Engine{
		onEvent:    onEvent,
		activeKeys: make(map[string]int),
		supWake:    make(chan struct{}, 1),
		supDone:    make(chan struct{}),
		supExited:  make(chan struct{}),
		idleGrace:  5 * time.Second,
	}
	e.enabled.Store(true)
	return e
}

// SetEnabled 由「本机共享」设置驱动：关闭后即使仍有对端订阅也不捕获键盘。
func (e *Engine) SetEnabled(v bool) {
	e.enabled.Store(v)
	select {
	case e.supWake <- struct{}{}:
	default:
	}
}

// wantActive 报告当前是否应该捕获：有订阅者且本机共享未关闭。
func (e *Engine) wantActive() bool {
	return atomic.LoadInt64(&e.subscribers) > 0 && e.enabled.Load()
}

func (e *Engine) SetSubscribers(count int64) {
	atomic.StoreInt64(&e.subscribers, count)
	atomic.StoreInt64(&activeSubs, count)
	select {
	case e.supWake <- struct{}{}:
	default:
	}
}

// StartSupervisor 启动按需启停监督 goroutine（幂等）。
func (e *Engine) StartSupervisor() {
	e.supOnce.Do(func() {
		e.supStarted.Store(true)
		go e.supervise()
	})
}

// StopSupervisor 停止监督 goroutine 并等待其退出（幂等）。
func (e *Engine) StopSupervisor() {
	if !e.supStarted.Load() {
		return
	}
	e.supStopOnce.Do(func() { close(e.supDone) })
	<-e.supExited
}

func (e *Engine) supervise() {
	defer close(e.supExited)
	var idleTimer *time.Timer
	var idleC <-chan time.Time
	stopIdle := func() {
		if idleTimer != nil {
			idleTimer.Stop()
			idleTimer, idleC = nil, nil
		}
	}
	for {
		select {
		case <-e.supDone:
			stopIdle()
			return
		case <-e.supWake:
			if e.wantActive() {
				stopIdle()
				e.Start()
				continue
			}
			// 本机共享被关闭：立即卸载钩子（隐私优先，不等宽限期）
			if !e.enabled.Load() {
				stopIdle()
				if e.IsRunning() {
					util.Logf("[键盘捕获] 本机共享已关闭，卸载系统级键盘钩子")
					e.Stop()
				}
				continue
			}
			if e.IsRunning() && idleTimer == nil {
				idleTimer = time.NewTimer(e.idleGrace)
				idleC = idleTimer.C
			}
		case <-idleC:
			idleTimer, idleC = nil, nil
			if !e.wantActive() {
				util.Logf("[键盘捕获] 无订阅者，卸载系统级键盘钩子（空闲零空转）")
				e.Stop()
			}
		}
	}
}

// IsRunning 报告捕获是否在运行（诊断用）。
func (e *Engine) IsRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.isRunning
}

func (e *Engine) Start() {
	e.mu.Lock()
	if e.isRunning {
		e.mu.Unlock()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.isRunning = true
	done := make(chan struct{})
	e.done = done
	e.mu.Unlock()

	go func() {
		defer close(done)
		e.loop(ctx)
	}()
}

// Stop 停止捕获并等待捕获循环（含钩子卸载）退出，保证可安全重启。
func (e *Engine) Stop() {
	e.mu.Lock()
	if !e.isRunning {
		e.mu.Unlock()
		return
	}
	cancel := e.cancel
	e.cancel = nil
	done := e.done
	e.done = nil
	e.isRunning = false
	e.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			util.Logf("[键盘捕获] 停止等待超时（钩子线程可能未及时退出）")
		}
	}
}

func (e *Engine) loop(ctx context.Context) {
	// emit 统一承载订阅门控与调试日志：无订阅者时事件不外发
	emit := func(evt KeyEvent) {
		if atomic.LoadInt64(&e.subscribers) <= 0 {
			return
		}
		if util.DebugKeys() {
			// 同鼠标：写日志文件，.app 窗口形态下 stdout 不可见
			util.Logf("[键盘捕获] %s %s (键码 %#x)", evt.Action, evt.Key, evt.KeyCode)
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
					friendly := FriendlyKeyName(key)
					emit(KeyEvent{
						Type:      "keyboard",
						Action:    "down",
						Key:       key,
						Name:      friendly,
						Friendly:  friendly,
						KeyCode:   code,
						Timestamp: now,
					})
				}
			}

			// Check released keys (key up)
			for key, code := range e.activeKeys {
				if _, stillPressed := currentPressed[key]; !stillPressed {
					delete(e.activeKeys, key)
					friendly := FriendlyKeyName(key)
					emit(KeyEvent{
						Type:      "keyboard",
						Action:    "up",
						Key:       key,
						Name:      friendly,
						Friendly:  friendly,
						KeyCode:   code,
						Timestamp: now,
					})
				}
			}
		}
	}
}
