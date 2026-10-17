//go:build windows

package mouse

import (
	"context"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	procGetAsyncKeyState    = user32.NewProc("GetAsyncKeyState")
	procSetCursorPos        = user32.NewProc("SetCursorPos")
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
	mOUSEEVENTF_MOVE       = 0x0001
	mOUSEEVENTF_LEFTDOWN   = 0x0002
	mOUSEEVENTF_LEFTUP     = 0x0004
	mOUSEEVENTF_RIGHTDOWN  = 0x0008
	mOUSEEVENTF_RIGHTUP    = 0x0010
	mOUSEEVENTF_MIDDLEDOWN = 0x0020
	mOUSEEVENTF_MIDDLEUP   = 0x0040
	mOUSEEVENTF_WHEEL      = 0x0800
	wH_MOUSE_LL            = 14
	llmhfInjected          = 0x1 // 低级钩子标记：该事件由本进程注入（防回环）
	wM_QUIT                = 0x0012
	inputMouse             = 0 // SendInput 的鼠标事件类型
)

// SendInput 的鼠标输入结构（x64 布局）
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

type sendInputRec struct {
	Type  uint32
	Union struct {
		Mi mouseInput
		Hi hardwareInput
	}
}

type point struct {
	X, Y int32
}

type msllhookstruct struct {
	Pt        point
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

var (
	msEventCh     chan MouseEvent
	msHookProcRef = syscall.NewCallback(msHookProc) // 包级持有，防止回调被 GC 回收
	msHookHandle  uintptr
	msHookThread  uint32
)

// msHookProc 低级鼠标钩子回调：只做注入过滤 + 非阻塞投递。
func msHookProc(code int32, wParam uintptr, lParam uintptr) uintptr {
	if code >= 0 {
		ms := (*msllhookstruct)(unsafe.Pointer(lParam))
		// 过滤注入事件，防止捕获/注入无限回环
		if ms.Flags&llmhfInjected == 0 {
			if msEventCh != nil {
				evt := MouseEvent{
					Type:      "mouse",
					RawX:      int(ms.Pt.X),
					RawY:      int(ms.Pt.Y),
					Timestamp: time.Now().UnixMilli(),
				}
				switch wParam {
				case 0x0200: // WM_MOUSEMOVE
					evt.Action = "move"
				case 0x0201: // WM_LBUTTONDOWN
					evt.Action, evt.LeftDown = "down", true
				case 0x0202: // WM_LBUTTONUP
					evt.Action = "up"
				case 0x0204: // WM_RBUTTONDOWN
					evt.Action, evt.RightDown = "down", true
				case 0x0205: // WM_RBUTTONUP
					evt.Action = "up"
				case 0x0206: // WM_MBUTTONDOWN
					evt.Action, evt.MiddleDown = "down", true
				case 0x0207: // WM_MBUTTONUP
					evt.Action = "up"
				case 0x020A: // WM_MOUSEWHEEL
					evt.Action = "wheel"
					// mouseData 高 16 位为有符号滚轮增量（正 = 向上）
					evt.WheelDelta = int(int16(uint16(ms.MouseData >> 16)))
				default:
					ret, _, _ := procCallNextHookEx.Call(msHookHandle, uintptr(code), wParam, lParam)
					return ret
				}
				select {
				case msEventCh <- evt:
				default:
				}
			}
		}
	}
	ret, _, _ := procCallNextHookEx.Call(msHookHandle, uintptr(code), wParam, lParam)
	return ret
}

const wM_MOUSEMOVE_ = 0x0200
const wM_RBUTTONDOWN_ = 0x0204
const wM_RBUTTONUP_ = 0x0205
const wM_MBUTTONDOWN_ = 0x0206
const wM_MBUTTONUP_ = 0x0207
const wM_MOUSEWHEEL_ = 0x020A

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

// runEventCapture 以 WH_MOUSE_LL 低级钩子事件驱动捕获鼠标：
// 移动合并到 ~60Hz 发送，按下/抬起/滚轮即时发送（过滤注入事件防回环）。
func runEventCapture(ctx context.Context, emit func(MouseEvent)) bool {
	events := make(chan MouseEvent, 512)
	msEventCh = events
	installed := make(chan bool, 1)

	go func() {
		// 低级钩子要求常驻消息循环，必须独占 OS 线程
		runtime.LockOSThread()
		mod, _, _ := procGetModuleHandleW.Call(0)
		hhk, _, _ := procSetWindowsHooksExW.Call(
			wH_MOUSE_LL, msHookProcRef, mod, 0)
		if hhk == 0 {
			installed <- false
			return
		}
		msHookHandle = hhk
		tid, _, _ := procGetCurrentThreadId.Call()
		msHookThread = uint32(tid)
		installed <- true

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
		if msHookThread != 0 {
			procPostThreadMessageW.Call(uintptr(msHookThread), wM_QUIT, 0, 0)
		}
	}()

	var pending *MouseEvent
	ticker := time.NewTicker(16 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return true
		case evt := <-events:
			switch evt.Action {
			case "move":
				cp := evt
				pending = &cp
			case "down", "up":
				// 依据消息类型补按钮状态（msg 语义在钩子回调里已判断；这里按坐标+消息换算）
				emit(evt)
			default:
				emit(evt)
			}
		case <-ticker.C:
			if pending != nil {
				screenW, screenH := getScreenSize()
				pending.ScreenWidth, pending.ScreenHeight = screenW, screenH
				pending.X = float64(pending.RawX) / float64(screenW)
				pending.Y = float64(pending.RawY) / float64(screenH)
				emit(*pending)
				pending = nil
			}
		}
	}
}

// SimulateMove moves the host cursor relatively by dx and dy.
func SimulateMove(dx, dy int) {
	sendMouseButton(mOUSEEVENTF_MOVE, int32(dx), int32(dy), 0)
}

// SimulateAbsolute moves the host cursor to normalized coordinates (0.0 ~ 1.0).
func SimulateAbsolute(normX, normY float64) {
	w, h := getScreenSize()
	targetX := int(normX * float64(w))
	targetY := int(normY * float64(h))
	procSetCursorPos.Call(uintptr(targetX), uintptr(targetY))
}

// SimulateButton triggers mouse button press or release (SendInput).
func SimulateButton(btn string, down bool) {
	var flag uint32
	switch btn {
	case "left":
		if down {
			flag = mOUSEEVENTF_LEFTDOWN
		} else {
			flag = mOUSEEVENTF_LEFTUP
		}
	case "right":
		if down {
			flag = mOUSEEVENTF_RIGHTDOWN
		} else {
			flag = mOUSEEVENTF_RIGHTUP
		}
	case "middle":
		if down {
			flag = mOUSEEVENTF_MIDDLEDOWN
		} else {
			flag = mOUSEEVENTF_MIDDLEUP
		}
	}
	if flag != 0 {
		sendMouseButton(flag, 0, 0, 0)
	}
}

// SimulateScroll triggers mouse wheel vertical scrolling (SendInput).
func SimulateScroll(dy int) {
	sendMouseButton(mOUSEEVENTF_WHEEL, 0, 0, uint32(dy))
}

// sendMouseButton 通过 SendInput 注入鼠标事件（keybd_event/mouse_event 已过时）。
func sendMouseButton(flags uint32, dx, dy int32, wheelData uint32) {
	var in sendInputRec
	in.Type = inputMouse
	in.Union.Mi = mouseInput{
		Dx:        dx,
		Dy:        dy,
		MouseData: wheelData,
		Dwflags:   flags,
	}
	procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}
