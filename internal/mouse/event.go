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

// activeSubs 是包级订阅数镜像：系统级钩子回调用它做最快路径门控。
// 钩子回调运行在系统输入线程进入的 Go 回调里，任何多余工作（取时间、
// 建事件、channel 操作）都会放大成全系统的输入开销。
var activeSubs int64

// MouseEvent represents a normalized cursor movement or button event.
type MouseEvent struct {
	Type         string  `json:"type"`                  // "mouse"
	Action       string  `json:"action"`                // "move", "down", "up", "wheel"
	Button       string  `json:"button,omitempty"`      // "left", "right", "middle" (仅 down/up 时有效)
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
	done      chan struct{} // 捕获循环退出完成（Stop 等待，保证可安全重启）

	// Active subscriber count (when 0, capture can sleep to save CPU)
	subscribers int64

	// enabled 对应 UI「本机共享」开关：关闭后即使仍有对端订阅也不安装钩子。
	enabled atomic.Bool

	// 按需启停：无人订阅时不安装系统级 WH_MOUSE_LL 钩子。
	// supervisor goroutine 独占 Start/Stop 调用权（钩子安装/卸载是系统调用，
	// 不能放在 hub 广播循环里）；订阅归零后延迟 idleGrace 再停，避免映射
	// 开关抖动导致钩子反复装卸。
	supWake     chan struct{}
	supDone     chan struct{}
	supExited   chan struct{}
	supStarted  atomic.Bool
	supOnce     sync.Once
	supStopOnce sync.Once
	idleGrace   time.Duration

	lastX, lastY        int
	lastL, lastR, lastM bool
}

func NewEngine(onEvent func(MouseEvent)) *Engine {
	e := &Engine{
		onEvent:   onEvent,
		supWake:   make(chan struct{}, 1),
		supDone:   make(chan struct{}),
		supExited: make(chan struct{}),
		idleGrace: 5 * time.Second,
	}
	e.enabled.Store(true)
	return e
}

// SetEnabled 由「本机共享」设置驱动：关闭后即使仍有对端订阅也不捕获鼠标，
// 重新打开且仍有订阅时自动恢复。
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

// SetSubscribers updates active subscriber count.
func (e *Engine) SetSubscribers(count int64) {
	atomic.StoreInt64(&e.subscribers, count)
	atomic.StoreInt64(&activeSubs, count)
	select {
	case e.supWake <- struct{}{}:
	default:
	}
}

// StartSupervisor 启动按需启停监督 goroutine（幂等）：
// 订阅数 > 0 才安装系统级钩子，归零 idleGrace 后卸载。
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

// supervise 是钩子安装/卸载的唯一决策者。
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
					util.Logf("[鼠标捕获] 本机共享已关闭，卸载系统级鼠标钩子")
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
				util.Logf("[鼠标捕获] 无订阅者，卸载系统级鼠标钩子（空闲零空转）")
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

// Start begins tracking the host mouse cursor.
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

// Stop stops mouse tracking.
// 会等待捕获循环（含钩子线程卸载）退出后再返回：按需启停需要保证
// 「停→再启」不会留下未卸载的旧钩子或与新钩子抢全局状态。
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
			util.Logf("[鼠标捕获] 停止等待超时（钩子线程可能未及时退出）")
		}
	}
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
			button := ""
			if btnChanged {
				if lDown != e.lastL {
					button = "left"
					if lDown {
						action = "down"
					} else {
						action = "up"
					}
				} else if rDown != e.lastR {
					button = "right"
					if rDown {
						action = "down"
					} else {
						action = "up"
					}
				} else if mDown != e.lastM {
					button = "middle"
					if mDown {
						action = "down"
					} else {
						action = "up"
					}
				}
			}

			// Calculate normalized coordinates clamped to 0.0 ~ 1.0
			normX := math.Max(0.0, math.Min(1.0, float64(x)/float64(screenW)))
			normY := math.Max(0.0, math.Min(1.0, float64(y)/float64(screenH)))

			evt := MouseEvent{
				Type:         "mouse",
				Action:       action,
				Button:       button,
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
