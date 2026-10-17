//go:build windows

package mouse

import (
	"syscall"
	"unsafe"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	procGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")
	procMouseEvent       = user32.NewProc("mouse_event")
	procSetCursorPos     = user32.NewProc("SetCursorPos")
)

const (
	MOUSEEVENTF_MOVE       = 0x0001
	MOUSEEVENTF_LEFTDOWN   = 0x0002
	MOUSEEVENTF_LEFTUP     = 0x0004
	MOUSEEVENTF_RIGHTDOWN  = 0x0008
	MOUSEEVENTF_RIGHTUP    = 0x0010
	MOUSEEVENTF_MIDDLEDOWN = 0x0020
	MOUSEEVENTF_MIDDLEUP   = 0x0040
	MOUSEEVENTF_WHEEL      = 0x0800
)

type point struct {
	X, Y int32
}

func getScreenSize() (int, int) {
	w, _, _ := procGetSystemMetrics.Call(0) // SM_CXSCREEN
	h, _, _ := procGetSystemMetrics.Call(1) // SM_CYSCREEN
	return int(w), int(h)
}

func getCursor() (int, int, bool, bool, bool) {
	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

	lBtn, _, _ := procGetAsyncKeyState.Call(1)
	rBtn, _, _ := procGetAsyncKeyState.Call(2)
	mBtn, _, _ := procGetAsyncKeyState.Call(4)

	lDown := (lBtn & 0x8000) != 0
	rDown := (rBtn & 0x8000) != 0
	mDown := (mBtn & 0x8000) != 0

	return int(pt.X), int(pt.Y), lDown, rDown, mDown
}

// SimulateMove moves the host cursor relatively by dx and dy.
func SimulateMove(dx, dy int) {
	procMouseEvent.Call(MOUSEEVENTF_MOVE, uintptr(dx), uintptr(dy), 0, 0)
}

// SimulateAbsolute moves the host cursor to normalized coordinates (0.0 ~ 1.0).
func SimulateAbsolute(normX, normY float64) {
	w, h := getScreenSize()
	targetX := int(normX * float64(w))
	targetY := int(normY * float64(h))
	procSetCursorPos.Call(uintptr(targetX), uintptr(targetY))
}

// SimulateButton triggers mouse button press or release.
func SimulateButton(btn string, down bool) {
	var flag uintptr
	switch btn {
	case "left":
		if down {
			flag = MOUSEEVENTF_LEFTDOWN
		} else {
			flag = MOUSEEVENTF_LEFTUP
		}
	case "right":
		if down {
			flag = MOUSEEVENTF_RIGHTDOWN
		} else {
			flag = MOUSEEVENTF_RIGHTUP
		}
	case "middle":
		if down {
			flag = MOUSEEVENTF_MIDDLEDOWN
		} else {
			flag = MOUSEEVENTF_MIDDLEUP
		}
	}
	if flag != 0 {
		procMouseEvent.Call(flag, 0, 0, 0, 0)
	}
}

// SimulateScroll triggers mouse wheel vertical scrolling.
func SimulateScroll(dy int) {
	procMouseEvent.Call(MOUSEEVENTF_WHEEL, 0, 0, uintptr(dy), 0)
}
