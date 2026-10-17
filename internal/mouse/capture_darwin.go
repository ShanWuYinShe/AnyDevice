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
