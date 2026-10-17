//go:build darwin

package keyboard

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation -framework ApplicationServices
#include <CoreGraphics/CoreGraphics.h>
#include <ApplicationServices/ApplicationServices.h>

// 本应用注入事件的标记（kCGEventSourceUserData），事件钩子据此过滤，
// 防止“注入 → 自己捕获 → 再转发 → 再注入”的无限回环。
#define ANYD_INJECTED_MAGIC 0x414E5944

extern CGEventRef kbTapCallback(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void* refcon);

static CFMachPortRef kbTapPort = NULL;
static CFRunLoopSourceRef kbTapSource = NULL;
static CFRunLoopRef kbTapLoop = NULL;

// kbTapInstall 安装会话级只听事件钩子（键盘按下/抬起/修饰键变化）。
// 未授予「辅助功能」权限时 CGEventTapCreate 返回 NULL（返回 0 → 调用方回退轮询）。
static int kbTapInstall(void) {
    CGEventMask mask = CGEventMaskBit(kCGEventKeyDown)
                     | CGEventMaskBit(kCGEventKeyUp)
                     | CGEventMaskBit(kCGEventFlagsChanged);
    kbTapPort = CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap,
                                 kCGEventTapOptionListenOnly, mask, kbTapCallback, NULL);
    if (!kbTapPort) return 0;
    kbTapSource = CFMachPortCreateRunLoopSource(NULL, kbTapPort, 0);
    kbTapLoop = CFRunLoopGetCurrent();
    CFRunLoopAddSource(kbTapLoop, kbTapSource, kCFRunLoopDefaultMode);
    CGEventTapEnable(kbTapPort, true);
    return 1;
}

static void kbTapRun(void) {
    CFRunLoopRun();
}

static void kbTapStop(void) {
    if (kbTapSource) {
        CFRunLoopRemoveSource(kbTapLoop, kbTapSource, kCFRunLoopDefaultMode);
        CFRelease(kbTapSource);
        kbTapSource = NULL;
    }
    if (kbTapPort) {
        CFRelease(kbTapPort);
        kbTapPort = NULL;
    }
    if (kbTapLoop) {
        CFRunLoopStop(kbTapLoop);
        kbTapLoop = NULL;
    }
}

static void kbTapReenable(void) {
    if (kbTapPort) {
        CGEventTapEnable(kbTapPort, true);
    }
}

static int is_key_down(int k) {
    return CGEventSourceKeyState(kCGEventSourceStateCombinedSessionState, (CGKeyCode)k);
}

// sim_key_flags 按指定修饰键位发送按键事件（带注入标记防回环）。
static void sim_key_flags(int k, int down, unsigned long flags) {
    CGEventRef ev = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)k, (bool)down);
    if (!ev) return;
    CGEventSetIntegerValueField(ev, kCGEventSourceUserData, ANYD_INJECTED_MAGIC);
    if (flags != 0) CGEventSetFlags(ev, (CGEventFlags)flags);
    CGEventPost(kCGHIDEventTap, ev);
    CFRelease(ev);
}

// sim_key_full 额外携带 Unicode 字符串：字符型按键（字母/数字/标点）
// 直接以文本形式注入，绕开输入法与键盘布局对裸键码的转换，
// 这是跨端键盘同步（Deskflow/Synergy 同类方案）的标准做法。
static void sim_key_full(int k, int down, unsigned long flags, const unsigned short* str, int len) {
    CGEventRef ev = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)k, (bool)down);
    if (!ev) return;
    CGEventSetIntegerValueField(ev, kCGEventSourceUserData, ANYD_INJECTED_MAGIC);
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
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
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
	MetaRight:    0x36, // Command（右）
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

	// Numpad（数字小键盘；macOS keypad 键码）
	Numpad0:        0x52,
	Numpad1:        0x53,
	Numpad2:        0x54,
	Numpad3:        0x55,
	Numpad4:        0x56,
	Numpad5:        0x57,
	Numpad6:        0x58,
	Numpad7:        0x59,
	Numpad8:        0x5B,
	Numpad9:        0x5C,
	NumpadDecimal:  0x41,
	NumpadMultiply: 0x43,
	NumpadAdd:      0x45,
	NumpadDivide:   0x4B,
	NumpadEnter:    0x4C,
	NumpadSubtract: 0x4E,
	NumpadEqual:    0x51,
	NumLock:        0x47, // kVK_ANSI_KeypadClear / NumLock
}

// codeToKeyName 平台键码 → 标准键名（事件钩子反查用，启动时生成）
var codeToKeyName = func() map[int]string {
	m := make(map[int]string, len(darwinKeyMap))
	for name, code := range darwinKeyMap {
		if _, exists := m[code]; !exists {
			m[code] = name
		}
	}
	return m
}()

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

// macCodeFlag 修饰键平台键码 → 修饰位（FlagsChanged 事件判定按下/抬起用）
var macCodeFlag = map[int]uint64{
	0x38: flagShift,
	0x3C: flagShift,
	0x3B: flagControl,
	0x3E: flagControl,
	0x3A: flagAlt,
	0x3D: flagAlt,
	0x37: flagCommand,
	0x36: flagCommand,
	0x39: flagAlphaShift,
}

var (
	simMu       sync.Mutex
	activeFlags uint64
	activeKeys  = make(map[string]bool)
)

// keyCharMap 字符型按键在美式布局下的字符（[无 Shift, 有 Shift]）。
// Space/Tab/Enter 不在字符表内：它们是输入法的提交/候选键，
// 必须走纯键码让目标输入法按原始语义处理，不能注入字符。
var keyCharMap = map[string][2]string{
	Minus:          {"-", "_"},
	Equal:          {"=", "+"},
	BracketLeft:    {"[", "{"},
	BracketRight:   {"]", "}"},
	Backslash:      {"\\", "|"},
	Semicolon:      {";", ":"},
	Quote:          {"'", "\""},
	Comma:          {",", "<"},
	Period:         {".", ">"},
	Slash:          {"/", "?"},
	Backquote:      {"`", "~"},
	Digit1:         {"1", "!"},
	Digit2:         {"2", "@"},
	Digit3:         {"3", "#"},
	Digit4:         {"4", "$"},
	Digit5:         {"5", "%"},
	Digit6:         {"6", "^"},
	Digit7:         {"7", "&"},
	Digit8:         {"8", "*"},
	Digit9:         {"9", "("},
	Digit0:         {"0", ")"},
	Numpad0:        {"0", "0"},
	Numpad1:        {"1", "1"},
	Numpad2:        {"2", "2"},
	Numpad3:        {"3", "3"},
	Numpad4:        {"4", "4"},
	Numpad5:        {"5", "5"},
	Numpad6:        {"6", "6"},
	Numpad7:        {"7", "7"},
	Numpad8:        {"8", "8"},
	Numpad9:        {"9", "9"},
	NumpadDecimal:  {".", "."},
	NumpadAdd:      {"+", "+"},
	NumpadSubtract: {"-", "-"},
	NumpadMultiply: {"*", "*"},
	NumpadDivide:   {"/", "/"},
	NumpadEqual:    {"=", "="},
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

// kbEventCh 事件钩子 → Go 消费循环的事件通道（安装钩子时创建）
var kbEventCh chan KeyEvent

// kbTimeNow 供钩子回调取时间戳（避免在 //export 里重复 import 逻辑）
var kbTimeNow = func() int64 { return time.Now().UnixMilli() }

//export kbTapCallback
func kbTapCallback(proxy C.CGEventTapProxy, etype C.CGEventType, event C.CGEventRef, refcon unsafe.Pointer) C.CGEventRef {
	// 系统因超时或用户交互临时禁用钩子时自动恢复，防止键盘捕获静默失效
	if etype == C.kCGEventTapDisabledByTimeout || etype == C.kCGEventTapDisabledByUserInput {
		C.kbTapReenable()
		return event
	}

	// 本应用注入的事件：跳过，防止捕获/注入无限回环
	if C.CGEventGetIntegerValueField(event, C.kCGEventSourceUserData) == C.int64_t(0x414E5944) {
		return event
	}
	if kbEventCh == nil {
		return event
	}

	var action string
	switch etype {
	case C.kCGEventKeyDown:
		action = "down"
	case C.kCGEventKeyUp:
		action = "up"
	case C.kCGEventFlagsChanged:
		// 修饰键状态变化：按当前修饰位判定按下/抬起
		code := int(C.CGEventGetIntegerValueField(event, C.kCGKeyboardEventKeycode))
		f, known := macCodeFlag[code]
		if !known {
			return event
		}
		if C.CGEventGetFlags(event)&C.CGEventFlags(f) != 0 {
			action = "down"
		} else {
			action = "up"
		}
		_ = code
	default:
		return event
	}

	code := int(C.CGEventGetIntegerValueField(event, C.kCGKeyboardEventKeycode))
	keyName, ok := codeToKeyName[code]
	if !ok {
		if util.DebugKeys() {
			fmt.Printf("[键盘捕获] 收到未映射键码 %#x，已忽略\n", code)
		}
		return event
	}

	// 非阻塞投递（事件钩子回调必须快速返回，否则系统会移除钩子）
	friendly := FriendlyKeyName(keyName)
	select {
	case kbEventCh <- KeyEvent{
		Type:      "keyboard",
		Action:    action,
		Key:       keyName,
		Name:      friendly,
		Friendly:  friendly,
		KeyCode:   code,
		Timestamp: kbTimeNow(),
	}:
	default:
	}
	return event
}

// runEventCapture 以 CGEventTap 事件驱动捕获键盘（参照 Deskflow 的事件模型）。
// 返回 false 表示未授权或安装失败，调用方回退轮询差分模式。
func runEventCapture(ctx context.Context, emit func(KeyEvent)) bool {
	if C.kbTapInstall() == 0 {
		fmt.Println("[键盘捕获] CGEventTap 安装失败（未授予辅助功能权限），回退轮询模式")
		return false
	}
	events := make(chan KeyEvent, 256)
	kbEventCh = events

	go func() {
		// 专用线程运行钩子 runloop（cgo 调用天然绑定 OS 线程）
		C.kbTapRun()
		C.kbTapStop()
	}()

	go func() {
		<-ctx.Done()
		C.kbTapStop()
	}()

	for {
		select {
		case <-ctx.Done():
			C.kbTapStop()
			return true
		case evt := <-events:
			emit(evt)
		}
	}
}

// SimulateKey 在本机模拟对端按键：
//   - 修饰键（Shift/Ctrl/Option/Cmd/CapsLock）按下与抬起实时更新修饰位；
//   - 普通键携带当前修饰位发送；字符型键额外携带 Unicode 字符，
//     解决中文输入法/非美式布局下“字母数字键无反应”的问题；
//   - 注入事件带 ANYD 标记，本机事件钩子会过滤，防止捕获回环。
//
// 注意：macOS 需要在「系统设置 → 隐私与安全性 → 辅助功能」中授权本应用，
// 否则 CGEventPost 会被系统静默丢弃（所有按键都无反应）。
func SimulateKey(keyName string, down bool) {
	simMu.Lock()
	defer simMu.Unlock()

	if down {
		activeKeys[keyName] = true
	} else {
		delete(activeKeys, keyName)
	}

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

// ReleaseAll 释放全部跟踪中的按键与修饰键（对端连接断开时调用防卡键）。
func ReleaseAll() {
	simMu.Lock()
	defer simMu.Unlock()

	for keyName := range activeKeys {
		if code, ok := darwinKeyMap[keyName]; ok {
			C.sim_key_flags(C.int(code), 0, 0)
		}
	}
	clear(activeKeys)

	if activeFlags != 0 {
		flagToName := map[uint64]string{
			flagShift:      ShiftLeft,
			flagControl:    ControlLeft,
			flagAlt:        AltLeft,
			flagCommand:    MetaLeft,
			flagAlphaShift: CapsLock,
		}
		for f, name := range flagToName {
			if activeFlags&f != 0 {
				if code, ok := darwinKeyMap[name]; ok {
					C.sim_key_flags(C.int(code), 0, 0)
				}
			}
		}
		activeFlags = 0
	}
}

// ReleaseAllModifiers 兼容旧接口（实际执行全键释放）。
func ReleaseAllModifiers() {
	ReleaseAll()
}
