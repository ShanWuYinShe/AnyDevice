//go:build darwin

package mouse

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>

// 本应用注入事件的标记（kCGEventSourceUserData），事件钩子据此过滤，
// 防止“注入 → 自己捕获 → 再转发 → 再注入”的无限回环。
#define ANYD_INJECTED_MAGIC 0x414E5944

extern CGEventRef msTapCallback(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void* refcon);

static CFMachPortRef msTapPort = NULL;
static CFRunLoopSourceRef msTapSource = NULL;
static CFRunLoopRef msTapLoop = NULL;

// msTapInstall 安装会话级只听鼠标钩子（移动/按下/抬起/滚轮）。
// 未授予「辅助功能」权限时 CGEventTapCreate 返回 NULL（返回 0 → 回退轮询）。
static int msTapInstall(void) {
    CGEventMask mask = CGEventMaskBit(kCGEventMouseMoved)
                     | CGEventMaskBit(kCGEventLeftMouseDown)
                     | CGEventMaskBit(kCGEventLeftMouseUp)
                     | CGEventMaskBit(kCGEventRightMouseDown)
                     | CGEventMaskBit(kCGEventRightMouseUp)
                     | CGEventMaskBit(kCGEventOtherMouseDown)
                     | CGEventMaskBit(kCGEventOtherMouseUp)
                     | CGEventMaskBit(kCGEventScrollWheel);
    msTapPort = CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap,
                                 kCGEventTapOptionListenOnly, mask, msTapCallback, NULL);
    if (!msTapPort) return 0;
    msTapSource = CFMachPortCreateRunLoopSource(NULL, msTapPort, 0);
    msTapLoop = CFRunLoopGetCurrent();
    CFRunLoopAddSource(msTapLoop, msTapSource, kCFRunLoopDefaultMode);
    CGEventTapEnable(msTapPort, true);
    return 1;
}

static void msTapRun(void) {
    CFRunLoopRun();
}

static void msTapStop(void) {
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
    CGEventRef move = CGEventCreateMouseEvent(NULL, kCGEventMouseMoved, pt, kCGMouseButtonLeft);
    if (move) {
        CGEventSetIntegerValueField(move, kCGEventSourceUserData, ANYD_INJECTED_MAGIC);
        CGEventPost(kCGHIDEventTap, move);
        CFRelease(move);
    }
}

static void sim_mouse_to(double x, double y) {
    CGPoint pt = CGPointMake(x, y);
    CGEventRef move = CGEventCreateMouseEvent(NULL, kCGEventMouseMoved, pt, kCGMouseButtonLeft);
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

// —— 事件钩子（CGEventTap）——

var msEventCh chan MouseEvent

//export msTapCallback
func msTapCallback(proxy C.CGEventTapProxy, etype C.CGEventType, event C.CGEventRef, refcon unsafe.Pointer) C.CGEventRef {
	// 本应用注入的事件：跳过，防止捕获/注入无限回环
	if C.CGEventGetIntegerValueField(event, C.kCGEventSourceUserData) == C.int64_t(0x414E5944) {
		return event
	}
	if msEventCh == nil {
		return event
	}

	const (
		evMove        = 5 // kCGEventMouseMoved
		evLeftDown    = 1
		evLeftUp      = 2
		evRightDown   = 3
		evRightUp     = 4
		evOtherDown   = 25
		evOtherUp     = 26
		evScrollWheel = 22
	)

	var evt MouseEvent
	evt.Type = "mouse"
	evt.Timestamp = time.Now().UnixMilli()

	var isMove bool
	switch etype {
	case C.kCGEventMouseMoved:
		isMove = true
	case C.kCGEventScrollWheel:
		evt.Action = "wheel"
		// deltaAxis1：正 = 向上
		evt.WheelDelta = int(C.CGEventGetIntegerValueField(event, C.kCGScrollWheelEventDeltaAxis1))
	case C.kCGEventLeftMouseDown, C.kCGEventRightMouseDown, C.kCGEventOtherMouseDown:
		evt.Action = "down"
	case C.kCGEventLeftMouseUp, C.kCGEventRightMouseUp, C.kCGEventOtherMouseUp:
		evt.Action = "up"
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
	if C.msTapInstall() == 0 {
		fmt.Println("[鼠标捕获] CGEventTap 安装失败（未授予辅助功能权限），回退轮询模式")
		return false
	}
	events := make(chan MouseEvent, 512)
	msEventCh = events

	go func() {
		C.msTapRun()
		C.msTapStop()
	}()

	go func() {
		<-ctx.Done()
		C.msTapStop()
	}()

	// move 合并节流：仅保留最新位置，按 ~60Hz 发送
	var pending *MouseEvent
	ticker := time.NewTicker(16 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			C.msTapStop()
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
