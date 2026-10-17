//go:build darwin

package mouse

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>

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
    }

    CGEventRef ev = CGEventCreateMouseEvent(NULL, t, pt, b);
    if (ev) {
        CGEventPost(kCGHIDEventTap, ev);
        CFRelease(ev);
    }
}

static void sim_mouse_scroll(int dy) {
    CGEventRef scroll = CGEventCreateScrollWheelEvent(NULL, kCGScrollEventUnitLine, 1, dy);
    if (scroll) {
        CGEventPost(kCGHIDEventTap, scroll);
        CFRelease(scroll);
    }
}
*/
import "C"

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

func SimulateButton(btn string, down bool) {
	b := 1
	if btn == "right" {
		b = 2
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
