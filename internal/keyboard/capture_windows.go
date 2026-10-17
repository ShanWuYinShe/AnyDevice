//go:build windows

package keyboard

import (
	"syscall"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")
	procKeybdEvent       = user32.NewProc("keybd_event")
)

const (
	KEYEVENTF_EXTENDEDKEY = 0x0001
	KEYEVENTF_KEYUP       = 0x0002
)

var windowsKeyMap = map[string]int{
	// Letters
	KeyA: 0x41, KeyB: 0x42, KeyC: 0x43, KeyD: 0x44, KeyE: 0x45, KeyF: 0x46, KeyG: 0x47, KeyH: 0x48, KeyI: 0x49, KeyJ: 0x4A,
	KeyK: 0x4B, KeyL: 0x4C, KeyM: 0x4D, KeyN: 0x4E, KeyO: 0x4F, KeyP: 0x50, KeyQ: 0x51, KeyR: 0x52, KeyS: 0x53, KeyT: 0x54,
	KeyU: 0x55, KeyV: 0x56, KeyW: 0x57, KeyX: 0x58, KeyY: 0x59, KeyZ: 0x5A,

	// Digits
	Digit0: 0x30, Digit1: 0x31, Digit2: 0x32, Digit3: 0x33, Digit4: 0x34, Digit5: 0x35, Digit6: 0x36, Digit7: 0x37, Digit8: 0x38, Digit9: 0x39,

	// Controls & Editing
	Backspace: 0x08,
	Tab:       0x09,
	Enter:     0x0D,
	CapsLock:  0x14,
	Escape:    0x1B,
	Space:     0x20,

	// Modifiers
	ShiftLeft:    0xA0,
	ShiftRight:   0xA1,
	ControlLeft:  0xA2,
	ControlRight: 0xA3,
	AltLeft:      0xA4,
	AltRight:     0xA5,
	MetaLeft:     0x5B, // Windows Key Left
	MetaRight:    0x5C, // Windows Key Right

	// Navigation
	PageUp:     0x21,
	PageDown:   0x22,
	End:        0x23,
	Home:       0x24,
	ArrowLeft:  0x25,
	ArrowUp:    0x26,
	ArrowRight: 0x27,
	ArrowDown:  0x28,
	Insert:     0x2D,
	Delete:     0x2E,

	// Function Keys
	F1:  0x70,
	F2:  0x71,
	F3:  0x72,
	F4:  0x73,
	F5:  0x74,
	F6:  0x75,
	F7:  0x76,
	F8:  0x77,
	F9:  0x78,
	F10: 0x79,
	F11: 0x7A,
	F12: 0x7B,

	// Punctuation
	Semicolon:    0xBA,
	Equal:        0xBB,
	Comma:        0xBC,
	Minus:        0xBD,
	Period:       0xBE,
	Slash:        0xBF,
	Backquote:    0xC0,
	BracketLeft:  0xDB,
	Backslash:    0xDC,
	BracketRight: 0xDD,
	Quote:        0xDE,
}

func getPressedKeys() map[string]int {
	pressed := make(map[string]int)
	for key, vk := range windowsKeyMap {
		r, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
		if (r & 0x8000) != 0 {
			pressed[key] = vk
		}
	}
	return pressed
}

// SimulateKey simulates a key press or release on Windows.
func SimulateKey(keyName string, down bool) {
	vk, exists := windowsKeyMap[keyName]
	if !exists {
		return
	}
	var flags uintptr
	if !down {
		flags |= KEYEVENTF_KEYUP
	}
	if vk == 0x21 || vk == 0x22 || vk == 0x23 || vk == 0x24 || (vk >= 0x25 && vk <= 0x28) || vk == 0x2D || vk == 0x2E || vk == 0x5B || vk == 0x5C || vk == 0xA1 || vk == 0xA3 || vk == 0xA5 {
		flags |= KEYEVENTF_EXTENDEDKEY
	}
	procKeybdEvent.Call(uintptr(vk), 0, flags, 0)
}
