//go:build darwin

package keyboard

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>

static int is_key_down(int k) {
    return CGEventSourceKeyState(kCGEventSourceStateCombinedSessionState, (CGKeyCode)k);
}

static void sim_key(int k, int down) {
    CGEventRef ev = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)k, (bool)down);
    if (ev) {
        CGEventPost(kCGHIDEventTap, ev);
        CFRelease(ev);
    }
}
*/
import "C"

var darwinKeyMap = map[string]int{
	// Letters
	KeyA: 0x00, KeyS: 0x01, KeyD: 0x02, KeyF: 0x03, KeyH: 0x04, KeyG: 0x05, KeyZ: 0x06, KeyX: 0x07, KeyC: 0x08, KeyV: 0x09,
	KeyB: 0x0B, KeyQ: 0x0C, KeyW: 0x0D, KeyE: 0x0E, KeyR: 0x0F, KeyY: 0x10, KeyT: 0x11,
	KeyL: 0x25, KeyJ: 0x26, KeyK: 0x28, KeyN: 0x2D, KeyM: 0x2E, KeyO: 0x1F, KeyU: 0x20, KeyI: 0x22, KeyP: 0x23,

	// Digits
	Digit1: 0x12, Digit2: 0x13, Digit3: 0x14, Digit4: 0x15, Digit5: 0x17, Digit6: 0x16, Digit7: 0x1A, Digit8: 0x1C, Digit9: 0x19, Digit0: 0x1D,

	// Controls & Editing
	Enter:     0x24,
	Tab:       0x30,
	Space:     0x31,
	Backspace: 0x33,
	Escape:    0x35,
	CapsLock:  0x39,

	// Modifiers
	MetaLeft:     0x37, // Command
	ShiftLeft:    0x38,
	AltLeft:      0x3A, // Option
	ControlLeft:  0x3B,
	ShiftRight:   0x3C,
	AltRight:     0x3D,
	ControlRight: 0x3E,

	// Navigation
	ArrowLeft:  0x7B,
	ArrowRight: 0x7C,
	ArrowDown:  0x7D,
	ArrowUp:    0x7E,
	PageUp:     0x74,
	PageDown:   0x79,
	Home:       0x73,
	End:        0x77,
	Delete:     0x75,

	// Function Keys
	F1:  0x7A,
	F2:  0x78,
	F3:  0x63,
	F4:  0x76,
	F5:  0x60,
	F6:  0x61,
	F7:  0x62,
	F8:  0x64,
	F9:  0x65,
	F10: 0x6D,
	F11: 0x67,
	F12: 0x6F,

	// Punctuation
	Equal:        0x18,
	Minus:        0x1B,
	BracketRight: 0x1E,
	BracketLeft:  0x21,
	Quote:        0x27,
	Semicolon:    0x29,
	Backslash:    0x2A,
	Comma:        0x2B,
	Slash:        0x2C,
	Period:       0x2F,
	Backquote:    0x32,
}

func getPressedKeys() map[string]int {
	pressed := make(map[string]int)
	for key, code := range darwinKeyMap {
		if C.is_key_down(C.int(code)) != 0 {
			pressed[key] = code
		}
	}
	return pressed
}

// SimulateKey simulates a key press or release on macOS.
func SimulateKey(keyName string, down bool) {
	code, exists := darwinKeyMap[keyName]
	if !exists {
		return
	}
	d := 0
	if down {
		d = 1
	}
	C.sim_key(C.int(code), C.int(d))
}
