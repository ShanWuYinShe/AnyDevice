//go:build darwin

package keyboard

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>

static int is_key_down(int k) {
    return CGEventSourceKeyState(kCGEventSourceStateCombinedSessionState, (CGKeyCode)k);
}

// sim_key_flags 按指定修饰键位发送按键事件；
// 不带修饰位时行为与裸键码一致（受目标输入法/布局解释）。
static void sim_key_flags(int k, int down, unsigned long flags) {
    CGEventRef ev = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)k, (bool)down);
    if (!ev) return;
    if (flags != 0) CGEventSetFlags(ev, (CGEventFlags)flags);
    CGEventPost(kCGHIDEventTap, ev);
    CFRelease(ev);
}

// sim_key_full 额外携带 Unicode 字符串：字符型按键（字母/数字/标点）
// 直接以文本形式注入，绕开输入法与键盘布局对裸键码的转换，
// 这是跨端键盘同步（Synergy/Barrier 同类方案）的标准做法。
static void sim_key_full(int k, int down, unsigned long flags, const unsigned short* str, int len) {
    CGEventRef ev = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)k, (bool)down);
    if (!ev) return;
    if (len > 0) {
        CGEventKeyboardSetUnicodeString(ev, (UniCharCount)len, str);
    }
    if (flags != 0) CGEventSetFlags(ev, (CGEventFlags)flags);
    CGEventPost(kCGHIDEventTap, ev);
    CFRelease(ev);
}
*/
import "C"

import (
	"fmt"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"any_device/internal/util"
)

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
	Insert:    0x72, // macOS 无独立 Insert 键，近似映射到 fn/帮助键

	// Modifiers
	MetaLeft:     0x37, // Command
	ShiftLeft:    0x38,
	AltLeft:      0x3A, // Option
	ControlLeft:  0x3B,
	ShiftRight:   0x3C,
	AltRight:     0x3D, // Option
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

// CGEventFlags 修饰键位（kCGEventFlagMask*）
const (
	flagAlphaShift = 0x00010000 // 大写锁定
	flagShift      = 0x00020000
	flagControl    = 0x00040000
	flagAlt        = 0x00080000
	flagCommand    = 0x00100000
)

// modifierFlagByName 标准键名 → 修饰位。对端报上来的修饰键按下/抬起
// 会实时更新 activeFlags，普通键事件据此携带修饰状态（Shift+字母、
// Cmd/Ctrl 组合键等不再丢失修饰信息）。
var modifierFlagByName = map[string]uint64{
	ShiftLeft:    flagShift,
	ShiftRight:   flagShift,
	ControlLeft:  flagControl,
	ControlRight: flagControl,
	AltLeft:      flagAlt,
	AltRight:     flagAlt,
	MetaLeft:     flagCommand,
	MetaRight:    flagCommand,
	CapsLock:     flagAlphaShift,
}

var (
	simMu       sync.Mutex
	activeFlags uint64
)

// keyCharMap 字符型按键在美式布局下的字符（[无 Shift, 有 Shift]）。
// 方向键、功能键等非字符键不在此表，走纯键码 + 修饰位路径。
// Space/Tab/Enter 不在字符表内：它们是输入法的提交/候选键，
// 必须走纯键码让目标输入法按原始语义处理，不能注入字符。
var keyCharMap = map[string][2]string{
	Minus:        {"-", "_"},
	Equal:        {"=", "+"},
	BracketLeft:  {"[", "{"},
	BracketRight: {"]", "}"},
	Backslash:    {"\\", "|"},
	Semicolon:    {";", ":"},
	Quote:        {"'", "\""},
	Comma:        {",", "<"},
	Period:       {".", ">"},
	Slash:        {"/", "?"},
	Backquote:    {"`", "~"},
	Digit1:       {"1", "!"},
	Digit2:       {"2", "@"},
	Digit3:       {"3", "#"},
	Digit4:       {"4", "$"},
	Digit5:       {"5", "%"},
	Digit6:       {"6", "^"},
	Digit7:       {"7", "&"},
	Digit8:       {"8", "*"},
	Digit9:       {"9", "("},
	Digit0:       {"0", ")"},
}

// keyChar 返回字母/数字/标点键对应的字符（Shift 决定上档），非字符键返回 false。
func keyChar(key string, shift bool) (string, bool) {
	// 字母：KeyA → a / A
	if strings.HasPrefix(key, "Key") && len(key) == 4 {
		letter := strings.ToLower(key[3:])
		if shift {
			return strings.ToUpper(letter), true
		}
		return letter, true
	}
	if pair, ok := keyCharMap[key]; ok {
		if shift {
			return pair[1], true
		}
		return pair[0], true
	}
	return "", false
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

// SimulateKey 在本机模拟对端按键：
//   - 修饰键（Shift/Ctrl/Option/Cmd/CapsLock）按下与抬起实时更新修饰位；
//   - 普通键携带当前修饰位发送；字符型键额外携带 Unicode 字符，
//     解决中文输入法/非美式布局下“字母数字键无反应”的问题；
//   - 未映射的按键打印留痕，便于定位“某键无反应”类反馈。
//
// 注意：macOS 需要在「系统设置 → 隐私与安全性 → 辅助功能」中授权本应用，
// 否则 CGEventPost 会被系统静默丢弃（所有按键都无反应）。
func SimulateKey(keyName string, down bool) {
	simMu.Lock()
	defer simMu.Unlock()

	if f, isModifier := modifierFlagByName[keyName]; isModifier {
		if down {
			activeFlags |= f
		} else {
			activeFlags &^= f
		}
	}

	code, exists := darwinKeyMap[keyName]
	if !exists {
		if down {
			fmt.Printf("[键盘执行] 收到未映射按键 %s，已忽略\n", keyName)
		}
		return
	}

	if down {
		if util.DebugKeys() {
			fmt.Printf("[键盘执行] %s 按下 修饰位=%#x\n", keyName, activeFlags)
		}
		if ch, ok := keyChar(keyName, activeFlags&flagShift != 0); ok {
			encoded := utf16.Encode([]rune(ch))
			C.sim_key_full(C.int(code), 1, C.ulong(activeFlags), (*C.ushort)(unsafe.Pointer(&encoded[0])), C.int(len(encoded)))
			return
		}
	}
	C.sim_key_flags(C.int(code), boolToInt(down), C.ulong(activeFlags))
}

func boolToInt(b bool) C.int {
	if b {
		return 1
	}
	return 0
}
