//go:build windows

package keyboard

import (
	"context"
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procGetAsyncKeyState    = user32.NewProc("GetAsyncKeyState")
	procSendInput           = user32.NewProc("SendInput")
	procSetWindowsHooksExW  = user32.NewProc("SetWindowsHooksExW")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procPostThreadMessageW  = user32.NewProc("PostThreadMessageW")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procGetCurrentThreadId  = kernel32.NewProc("GetCurrentThreadId")
)

const (
	wH_KEYBOARD_LL  = 13
	wM_KEYDOWN      = 0x0100
	wM_KEYUP        = 0x0101
	wM_SYSKEYDOWN   = 0x0104
	wM_SYSKEYUP     = 0x0105
	wM_QUIT         = 0x0012
	llkhfInjected   = 0x10 // 低级钩子标记：该事件由本进程注入（防回环）
	keyeventfKeyup  = 0x0002
	keyeventfExtKey = 0x0001
	inputKeyboard   = 1
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
	F1: 0x70, F2: 0x71, F3: 0x72, F4: 0x73, F5: 0x74, F6: 0x75,
	F7: 0x76, F8: 0x77, F9: 0x78, F10: 0x79, F11: 0x7A, F12: 0x7B,

	// Punctuation
	Semicolon: 0xBA, Equal: 0xBB, Comma: 0xBC, Minus: 0xBD, Period: 0xBE,
	Slash: 0xBF, Backquote: 0xC0, BracketLeft: 0xDB, Backslash: 0xDC,
	BracketRight: 0xDD, Quote: 0xDE,
}

// vkToKeyName 平台虚拟键码 → 标准键名（低级钩子反查用，启动时生成）
var vkToKeyName = func() map[int]string {
	m := make(map[int]string, len(windowsKeyMap))
	for name, vk := range windowsKeyMap {
		if _, exists := m[vk]; !exists {
			m[vk] = name
		}
	}
	return m
}()

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

// —— 低级键盘钩子（事件驱动捕获，参照 Deskflow 的钩子模型） ——

type kbdllhookstruct struct {
	VkCode    uint32
	ScanCode  uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

var (
	kbEventCh     chan KeyEvent
	kbHookProcRef = syscall.NewCallback(kbHookProc) // 包级持有，防止回调被 GC 回收
	kbHookHandle  uintptr
	kbHookThread  uint32
)

// kbHookProc 低级键盘钩子回调：必须在数毫秒内返回，
// 因此只做注入过滤 + 非阻塞投递，处理逻辑在消费 goroutine。
func kbHookProc(code int32, wParam uintptr, lParam uintptr) uintptr {
	if code >= 0 {
		kb := (*kbdllhookstruct)(unsafe.Pointer(lParam))
		// 过滤本应用（或其它程序）注入的按键，防止捕获/注入无限回环
		if kb.Flags&llkhfInjected == 0 {
			if keyName, ok := vkToKeyName[int(kb.VkCode)]; ok {
				action := "up"
				if wParam == wM_KEYDOWN || wParam == wM_SYSKEYDOWN {
					action = "down"
				}
				if kbEventCh != nil {
					select {
					case kbEventCh <- KeyEvent{
						Type:      "keyboard",
						Action:    action,
						Key:       keyName,
						Name:      FriendlyKeyName(keyName),
						KeyCode:   int(kb.VkCode),
						Timestamp: time.Now().UnixMilli(),
					}:
					default:
					}
				}
			}
		}
	}
	ret, _, _ := procCallNextHookEx.Call(kbHookHandle, uintptr(code), wParam, lParam)
	return ret
}

// runEventCapture 以 WH_KEYBOARD_LL 低级钩子事件驱动捕获键盘。
// Windows 下钩子安装基本必然成功；返回 false 时调用方回退轮询。
func runEventCapture(ctx context.Context, emit func(KeyEvent)) bool {
	events := make(chan KeyEvent, 256)
	kbEventCh = events
	installed := make(chan bool, 1)

	go func() {
		// 低级钩子要求常驻消息循环，必须独占 OS 线程
		runtime.LockOSThread()
		mod, _, _ := procGetModuleHandleW.Call(0)
		hhk, _, _ := procSetWindowsHooksExW.Call(
			wH_KEYBOARD_LL, kbHookProcRef, mod, 0)
		if hhk == 0 {
			installed <- false
			return
		}
		kbHookHandle = hhk
		tid, _, _ := procGetCurrentThreadId.Call()
		kbHookThread = uint32(tid)
		installed <- true

		// 消息循环（钩子线程必需）
		var msg [7]uintptr
		for {
			ret, _, _ := procGetMessageW.Call(
				uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0)
			if ret == 0 || ret == ^uintptr(0) {
				break
			}
		}
		procUnhookWindowsHookEx.Call(hhk)
	}()

	select {
	case ok := <-installed:
		if !ok {
			return false
		}
	case <-ctx.Done():
		return true
	}

	go func() {
		<-ctx.Done()
		if kbHookThread != 0 {
			procPostThreadMessageW.Call(uintptr(kbHookThread), wM_QUIT, 0, 0)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return true
		case evt := <-events:
			emit(evt)
		}
	}
}

// —— 注入：改用现代 SendInput（keybd_event 已被平台标记为过时） ——

type keyboardInput struct {
	Wvk         uint16
	Wscan       uint16
	Dwflags     uint32
	Time        uint32
	Dwextrainfo uintptr
}

type mouseInput struct {
	Dx          int32
	Dy          int32
	MouseData   uint32
	Dwflags     uint32
	Time        uint32
	Dwextrainfo uintptr
}

type hardwareInput struct {
	Umsg   uint32
	Wparam uint16
	Lparam uint16
}

type sendInputUnion struct {
	Mi mouseInput
	Ki keyboardInput
	Hi hardwareInput
}

type sendInputRec struct {
	Type  uint32
	Union sendInputUnion
}

// simulateKeyVK 以虚拟键码注入按键（SendInput）。
func simulateKeyVK(vk int, down bool) {
	var in sendInputRec
	in.Type = inputKeyboard
	in.Union.Ki.Wvk = uint16(vk)
	if !down {
		in.Union.Ki.Dwflags = keyeventfKeyup
	}
	// 导航/修饰等扩展键需要 EXTENDEDKEY 标记
	switch vk {
	case 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x2D, 0x2E, 0x5B, 0x5C, 0xA1, 0xA3, 0xA5:
		in.Union.Ki.Dwflags |= keyeventfExtKey
	}
	procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}

// SimulateKey simulates a key press or release on Windows (SendInput).
func SimulateKey(keyName string, down bool) {
	vk, exists := windowsKeyMap[keyName]
	if !exists {
		if down {
			fmt.Printf("[键盘执行] 收到未映射按键 %s，已忽略\n", keyName)
		}
		return
	}
	simulateKeyVK(vk, down)
}
