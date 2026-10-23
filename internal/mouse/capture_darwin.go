//go:build darwin

package mouse

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>
#include <pthread.h>

// 本应用注入事件的标记（kCGEventSourceUserData），事件钩子据此过滤，
// 防止“注入 → 自己捕获 → 再转发 → 再注入”的无限回环。
#define ANYD_INJECTED_MAGIC 0x414E5944

extern CGEventRef msTapCallback(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void* refcon);

static CFMachPortRef msTapPort = NULL;
static CFRunLoopSourceRef msTapSource = NULL;
static CFRunLoopRef msTapLoop = NULL;

// tap 全局状态的互斥锁：install（调用方线程）、halt（任意线程）、
// reenable（runloop 回调线程）会并发触碰这些指针；完整的资源释放（stop）
// 只在 runloop 线程自身执行，杜绝跨线程 CFRelease 与在途回调的竞争
// （此前三处并发 stop 曾实测双重释放崩溃，2026-10-24）。
static pthread_mutex_t msTapMutex = PTHREAD_MUTEX_INITIALIZER;

// msTapInstall 安装会话级只听鼠标钩子（移动/按下/抬起/滚轮）。
// 未授予「辅助功能」权限时 CGEventTapCreate 返回 NULL（返回 0 → 回退轮询）。
static int msTapInstall(void) {
    pthread_mutex_lock(&msTapMutex);
    CGEventMask mask = CGEventMaskBit(kCGEventMouseMoved)
                     | CGEventMaskBit(kCGEventLeftMouseDown)
                     | CGEventMaskBit(kCGEventLeftMouseUp)
                     | CGEventMaskBit(kCGEventRightMouseDown)
                     | CGEventMaskBit(kCGEventRightMouseUp)
                     | CGEventMaskBit(kCGEventOtherMouseDown)
                     | CGEventMaskBit(kCGEventOtherMouseUp)
                     | CGEventMaskBit(kCGEventLeftMouseDragged)
                     | CGEventMaskBit(kCGEventRightMouseDragged)
                     | CGEventMaskBit(kCGEventOtherMouseDragged)
                     | CGEventMaskBit(kCGEventScrollWheel);
    msTapPort = CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap,
                                 kCGEventTapOptionListenOnly, mask, msTapCallback, NULL);
    if (!msTapPort) { pthread_mutex_unlock(&msTapMutex); return 0; }
    msTapSource = CFMachPortCreateRunLoopSource(NULL, msTapPort, 0);
    msTapLoop = CFRunLoopGetCurrent();
    CFRunLoopAddSource(msTapLoop, msTapSource, kCFRunLoopDefaultMode);
    CGEventTapEnable(msTapPort, true);
    pthread_mutex_unlock(&msTapMutex);
    return 1;
}

static void msTapRun(void) {
    CFRunLoopRun();
}

// msTapStop 完整清理（remove source / release / stop loop）。
// 只允许 runloop 线程在 msTapRun 返回后调用：跨线程 CFRelease 可能与
// 在途回调竞争导致过度释放崩溃（2026-10-24 审查实测 SIGTRAP 复现）。
static void msTapStop(void) {
    pthread_mutex_lock(&msTapMutex);
    if (msTapSource) {
        CFRunLoopRemoveSource(msTapLoop, msTapSource, kCFRunLoopDefaultMode);
        CFRelease(msTapSource);
        msTapSource = NULL;
    }
    if (msTapPort) {
        CFRelease(msTapPort);
        msTapPort = NULL;
    }
    if (msTapLoop) {
        CFRunLoopStop(msTapLoop);
        msTapLoop = NULL;
    }
    pthread_mutex_unlock(&msTapMutex);
}

// msTapHalt 仅请求 runloop 退出、不释放任何资源，任意线程可安全调用；
// 完整清理交给 runloop 线程自己的 msTapStop。
static void msTapHalt(void) {
    pthread_mutex_lock(&msTapMutex);
    if (msTapLoop) {
        CFRunLoopStop(msTapLoop);
    }
    pthread_mutex_unlock(&msTapMutex);
}

static void msTapReenable(void) {
    pthread_mutex_lock(&msTapMutex);
    if (msTapPort) {
        CGEventTapEnable(msTapPort, true);
    }
    pthread_mutex_unlock(&msTapMutex);
}

static void get_mouse_info(double* x, double* y, int* lDown, int* rDown, int* mDown) {
    CGEventRef event = CGEventCreate(NULL);
    if (event) {
        CGPoint point = CGEventGetLocation(event);
        *x = point.x;
        *y = point.y;
        CFRelease(event);
    }
    *lDown = CGEventSourceButtonState(kCGEventSourceStateCombinedSessionState, kCGMouseButtonLeft);
    *rDown = CGEventSourceButtonState(kCGEventSourceStateCombinedSessionState, kCGMouseButtonRight);
    *mDown = CGEventSourceButtonState(kCGEventSourceStateCombinedSessionState, kCGMouseButtonCenter);
}

static void get_main_display_bounds(double* w, double* h) {
    CGRect bounds = CGDisplayBounds(CGMainDisplayID());
    *w = bounds.size.width;
    *h = bounds.size.height;
}

static void sim_mouse_move(int dx, int dy) {
    CGEventRef cur = CGEventCreate(NULL);
    if (!cur) return;
    CGPoint pt = CGEventGetLocation(cur);
    CFRelease(cur);

    pt.x += dx;
    pt.y += dy;

    int lDown = CGEventSourceButtonState(kCGEventSourceStateCombinedSessionState, kCGMouseButtonLeft);
    int rDown = CGEventSourceButtonState(kCGEventSourceStateCombinedSessionState, kCGMouseButtonRight);
    int mDown = CGEventSourceButtonState(kCGEventSourceStateCombinedSessionState, kCGMouseButtonCenter);

    CGEventType t = kCGEventMouseMoved;
    CGMouseButton b = kCGMouseButtonLeft;
    if (lDown) {
        t = kCGEventLeftMouseDragged;
        b = kCGMouseButtonLeft;
    } else if (rDown) {
        t = kCGEventRightMouseDragged;
        b = kCGMouseButtonRight;
    } else if (mDown) {
        t = kCGEventOtherMouseDragged;
        b = kCGMouseButtonCenter;
    }

    CGEventRef move = CGEventCreateMouseEvent(NULL, t, pt, b);
    if (move) {
        CGEventSetIntegerValueField(move, kCGEventSourceUserData, ANYD_INJECTED_MAGIC);
        CGEventPost(kCGHIDEventTap, move);
        CFRelease(move);
    }
}

static void sim_mouse_to(double x, double y) {
    CGPoint pt = CGPointMake(x, y);

    int lDown = CGEventSourceButtonState(kCGEventSourceStateCombinedSessionState, kCGMouseButtonLeft);
    int rDown = CGEventSourceButtonState(kCGEventSourceStateCombinedSessionState, kCGMouseButtonRight);
    int mDown = CGEventSourceButtonState(kCGEventSourceStateCombinedSessionState, kCGMouseButtonCenter);

    CGEventType t = kCGEventMouseMoved;
    CGMouseButton b = kCGMouseButtonLeft;
    if (lDown) {
        t = kCGEventLeftMouseDragged;
        b = kCGMouseButtonLeft;
    } else if (rDown) {
        t = kCGEventRightMouseDragged;
        b = kCGMouseButtonRight;
    } else if (mDown) {
        t = kCGEventOtherMouseDragged;
        b = kCGMouseButtonCenter;
    }

    CGEventRef move = CGEventCreateMouseEvent(NULL, t, pt, b);
    if (move) {
        CGEventSetIntegerValueField(move, kCGEventSourceUserData, ANYD_INJECTED_MAGIC);
        CGEventPost(kCGHIDEventTap, move);
        CFRelease(move);
    }
}

static void sim_mouse_btn(int btn, int down) {
    CGEventRef cur = CGEventCreate(NULL);
    if (!cur) return;
    CGPoint pt = CGEventGetLocation(cur);
    CFRelease(cur);

    CGEventType t = down ? kCGEventLeftMouseDown : kCGEventLeftMouseUp;
    CGMouseButton b = kCGMouseButtonLeft;
    if (btn == 2) {
        t = down ? kCGEventRightMouseDown : kCGEventRightMouseUp;
        b = kCGMouseButtonRight;
    } else if (btn == 3) {
        t = down ? kCGEventOtherMouseDown : kCGEventOtherMouseUp;
        b = kCGMouseButtonCenter;
    }

    CGEventRef ev = CGEventCreateMouseEvent(NULL, t, pt, b);
    if (ev) {
        CGEventSetIntegerValueField(ev, kCGEventSourceUserData, ANYD_INJECTED_MAGIC);
        CGEventPost(kCGHIDEventTap, ev);
        CFRelease(ev);
    }
}

static void sim_mouse_scroll(int dy) {
    CGEventRef scroll = CGEventCreateScrollWheelEvent(NULL, kCGScrollEventUnitLine, 1, dy);
    if (scroll) {
        CGEventSetIntegerValueField(scroll, kCGEventSourceUserData, ANYD_INJECTED_MAGIC);
        CGEventPost(kCGHIDEventTap, scroll);
        CFRelease(scroll);
    }
}
*/
import "C"

import (
	"context"
	"fmt"
	"runtime"
	"time"
	"unsafe"
)

func getScreenSize() (int, int) {
	var w, h C.double
	C.get_main_display_bounds(&w, &h)
	return int(w), int(h)
}

func getCursor() (int, int, bool, bool, bool) {
	var x, y C.double
	var lDown, rDown, mDown C.int
	C.get_mouse_info(&x, &y, &lDown, &rDown, &mDown)
	return int(x), int(y), lDown != 0, rDown != 0, mDown != 0
}

func SimulateMove(dx, dy int) {
	C.sim_mouse_move(C.int(dx), C.int(dy))
}

// SimulateAbsolute moves the host cursor to normalized coordinates (0.0 ~ 1.0).
func SimulateAbsolute(normX, normY float64) {
	var w, h C.double
	C.get_main_display_bounds(&w, &h)
	targetX := normX * float64(w)
	targetY := normY * float64(h)
	C.sim_mouse_to(C.double(targetX), C.double(targetY))
}

func SimulateButton(btn string, down bool) {
	b := 1
	if btn == "right" {
		b = 2
	}
	if btn == "middle" {
		b = 3
	}
	d := 0
	if down {
		d = 1
	}
	C.sim_mouse_btn(C.int(b), C.int(d))
}

func SimulateScroll(dy int) {
	C.sim_mouse_scroll(C.int(dy))
}

// ReleaseAllButtons 保险性抬起全部鼠标按钮（对端连接断开时防卡键）。
func ReleaseAllButtons() {
	SimulateButton("left", false)
	SimulateButton("right", false)
	SimulateButton("middle", false)
}

// —— 事件钩子（CGEventTap）——

var msEventCh chan MouseEvent

//export msTapCallback
func msTapCallback(proxy C.CGEventTapProxy, etype C.CGEventType, event C.CGEventRef, refcon unsafe.Pointer) C.CGEventRef {
	// 系统因超时或用户交互临时禁用钩子时自动恢复，防止鼠标捕获静默失效
	if etype == C.kCGEventTapDisabledByTimeout || etype == C.kCGEventTapDisabledByUserInput {
		C.msTapReenable()
		return event
	}

	// 本应用注入的事件：跳过，防止捕获/注入无限回环
	if C.CGEventGetIntegerValueField(event, C.kCGEventSourceUserData) == C.int64_t(0x414E5944) {
		return event
	}
	if msEventCh == nil {
		return event
	}

	var evt MouseEvent
	evt.Type = "mouse"
	evt.Timestamp = time.Now().UnixMilli()

	var isMove bool
	switch etype {
	case C.kCGEventMouseMoved, C.kCGEventLeftMouseDragged, C.kCGEventRightMouseDragged, C.kCGEventOtherMouseDragged:
		isMove = true
		evt.Action = "move"
	case C.kCGEventScrollWheel:
		evt.Action = "wheel"
		// deltaAxis1：正 = 向上
		evt.WheelDelta = int(C.CGEventGetIntegerValueField(event, C.kCGScrollWheelEventDeltaAxis1))
	case C.kCGEventLeftMouseDown:
		evt.Action, evt.Button = "down", "left"
	case C.kCGEventLeftMouseUp:
		evt.Action, evt.Button = "up", "left"
	case C.kCGEventRightMouseDown:
		evt.Action, evt.Button = "down", "right"
	case C.kCGEventRightMouseUp:
		evt.Action, evt.Button = "up", "right"
	case C.kCGEventOtherMouseDown:
		evt.Action, evt.Button = "down", "middle"
	case C.kCGEventOtherMouseUp:
		evt.Action, evt.Button = "up", "middle"
	default:
		return event
	}

	pt := C.CGEventGetLocation(event)
	var w, h C.double
	C.get_main_display_bounds(&w, &h)
	screenW, screenH := int(w), int(h)
	if screenW <= 0 {
		screenW = 1920
	}
	if screenH <= 0 {
		screenH = 1080
	}
	x, y := int(pt.x), int(pt.y)
	evt.RawX, evt.RawY = x, y
	evt.ScreenWidth, evt.ScreenHeight = screenW, screenH
	evt.X = float64(x) / float64(screenW)
	evt.Y = float64(y) / float64(screenH)
	evt.LeftDown = bool(C.CGEventSourceButtonState(C.kCGEventSourceStateCombinedSessionState, C.kCGMouseButtonLeft))
	evt.RightDown = bool(C.CGEventSourceButtonState(C.kCGEventSourceStateCombinedSessionState, C.kCGMouseButtonRight))
	evt.MiddleDown = bool(C.CGEventSourceButtonState(C.kCGEventSourceStateCombinedSessionState, C.kCGMouseButtonCenter))

	// move 高频事件：标记为最新位置，由消费循环按 ~60Hz 合并发送
	if isMove {
		select {
		case msEventCh <- evt:
		default:
		}
		return event
	}
	select {
	case msEventCh <- evt:
	default:
	}
	return event
}

// runEventCapture 以 CGEventTap 事件驱动捕获鼠标：移动合并到 ~60Hz 发送，
// 按下/抬起/滚轮即时发送。未授权或安装失败返回 false（调用方回退轮询）。
func runEventCapture(ctx context.Context, emit func(MouseEvent)) bool {
	events := make(chan MouseEvent, 512)
	msEventCh = events

	// install 与 run 必须在同一线程：CFRunLoopSource 挂在「安装时线程」的
	// runloop 上，而 CFRunLoopRun 运行的是「当前线程」的 runloop。此前 install
	// 在调用方 goroutine、run 另起 goroutine 且无 LockOSThread，goroutine 线程
	// 迁移后 source 与 runloop 错位——钩子装上却收不到事件且完全静默失效。
	// LockOSThread 把 install→run→stop 整个生命周期钉死在同一 OS 线程。
	installed := make(chan bool, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		ok := C.msTapInstall() == 1
		installed <- ok
		if !ok {
			return
		}
		// 安装完成后若 ctx 已取消（早停窗口），直接在同线程完成完整清理
		select {
		case <-ctx.Done():
			C.msTapStop()
			return
		default:
		}
		C.msTapRun()
		C.msTapStop()
	}()

	if !<-installed {
		fmt.Println("[鼠标捕获] CGEventTap 安装失败（未授予辅助功能权限），回退轮询模式")
		return false
	}

	go func() {
		<-ctx.Done()
		C.msTapHalt()
	}()

	// move 合并节流：仅保留最新位置，按 ~60Hz 发送
	var pending *MouseEvent
	ticker := time.NewTicker(16 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			C.msTapHalt()
			return true
		case evt := <-events:
			if evt.Action == "move" {
				cp := evt
				pending = &cp
				continue
			}
			pending = nil
			emit(evt)
		case <-ticker.C:
			if pending != nil {
				emit(*pending)
				pending = nil
			}
		}
	}
}
