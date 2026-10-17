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
)

type point struct {
	X, Y int32
}

func getScreenSize() (int, int) {
	w, _, _ := procGetSystemMetrics.Call(0) // SM_CXSCREEN (Primary monitor width)
	h, _, _ := procGetSystemMetrics.Call(1) // SM_CYSCREEN (Primary monitor height)
	return int(w), int(h)
}

func getCursor() (int, int, bool, bool, bool) {
	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

	// VK_LBUTTON = 0x01, VK_RBUTTON = 0x02, VK_MBUTTON = 0x04
	lBtn, _, _ := procGetAsyncKeyState.Call(1)
	rBtn, _, _ := procGetAsyncKeyState.Call(2)
	mBtn, _, _ := procGetAsyncKeyState.Call(4)

	// High-order bit indicates whether the key is down
	lDown := (lBtn & 0x8000) != 0
	rDown := (rBtn & 0x8000) != 0
	mDown := (mBtn & 0x8000) != 0

	return int(pt.X), int(pt.Y), lDown, rDown, mDown
}
