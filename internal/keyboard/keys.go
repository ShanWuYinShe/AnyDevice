package keyboard

// Standard W3C key code identifiers for cross-platform mapping.
const (
	// Letters
	KeyA = "KeyA"
	KeyB = "KeyB"
	KeyC = "KeyC"
	KeyD = "KeyD"
	KeyE = "KeyE"
	KeyF = "KeyF"
	KeyG = "KeyG"
	KeyH = "KeyH"
	KeyI = "KeyI"
	KeyJ = "KeyJ"
	KeyK = "KeyK"
	KeyL = "KeyL"
	KeyM = "KeyM"
	KeyN = "KeyN"
	KeyO = "KeyO"
	KeyP = "KeyP"
	KeyQ = "KeyQ"
	KeyR = "KeyR"
	KeyS = "KeyS"
	KeyT = "KeyT"
	KeyU = "KeyU"
	KeyV = "KeyV"
	KeyW = "KeyW"
	KeyX = "KeyX"
	KeyY = "KeyY"
	KeyZ = "KeyZ"

	// Digits
	Digit0 = "Digit0"
	Digit1 = "Digit1"
	Digit2 = "Digit2"
	Digit3 = "Digit3"
	Digit4 = "Digit4"
	Digit5 = "Digit5"
	Digit6 = "Digit6"
	Digit7 = "Digit7"
	Digit8 = "Digit8"
	Digit9 = "Digit9"

	// Numpad（数字小键盘；NumLock 开启时输出数字，关闭时行为同导航键）
	Numpad0        = "Numpad0"
	Numpad1        = "Numpad1"
	Numpad2        = "Numpad2"
	Numpad3        = "Numpad3"
	Numpad4        = "Numpad4"
	Numpad5        = "Numpad5"
	Numpad6        = "Numpad6"
	Numpad7        = "Numpad7"
	Numpad8        = "Numpad8"
	Numpad9        = "Numpad9"
	NumpadAdd      = "NumpadAdd"
	NumpadSubtract = "NumpadSubtract"
	NumpadMultiply = "NumpadMultiply"
	NumpadDivide   = "NumpadDivide"
	NumpadDecimal  = "NumpadDecimal"
	NumpadEnter    = "NumpadEnter"
	NumpadEqual    = "NumpadEqual"
	NumLock        = "NumLock"

	// Controls & Editing
	Enter     = "Enter"
	Escape    = "Escape"
	Backspace = "Backspace"
	Tab       = "Tab"
	Space     = "Space"
	CapsLock  = "CapsLock"

	// Modifiers
	ShiftLeft    = "ShiftLeft"
	ShiftRight   = "ShiftRight"
	ControlLeft  = "ControlLeft"
	ControlRight = "ControlRight"
	AltLeft      = "AltLeft"
	AltRight     = "AltRight"
	MetaLeft     = "MetaLeft"
	MetaRight    = "MetaRight"

	// Navigation
	ArrowUp    = "ArrowUp"
	ArrowDown  = "ArrowDown"
	ArrowLeft  = "ArrowLeft"
	ArrowRight = "ArrowRight"
	PageUp     = "PageUp"
	PageDown   = "PageDown"
	Home       = "Home"
	End        = "End"
	Insert     = "Insert"
	Delete     = "Delete"

	// Function Keys
	F1  = "F1"
	F2  = "F2"
	F3  = "F3"
	F4  = "F4"
	F5  = "F5"
	F6  = "F6"
	F7  = "F7"
	F8  = "F8"
	F9  = "F9"
	F10 = "F10"
	F11 = "F11"
	F12 = "F12"

	// Punctuation
	Minus        = "Minus"
	Equal        = "Equal"
	BracketLeft  = "BracketLeft"
	BracketRight = "BracketRight"
	Backslash    = "Backslash"
	Semicolon    = "Semicolon"
	Quote        = "Quote"
	Backquote    = "Backquote"
	Comma        = "Comma"
	Period       = "Period"
	Slash        = "Slash"
)

// FriendlyKeyName returns human-readable label for a key code.
func FriendlyKeyName(code string) string {
	switch code {
	case Enter:
		return "回车 (Enter)"
	case Space:
		return "空格 (Space)"
	case Backspace:
		return "退格 (Backspace)"
	case Tab:
		return "制表符 (Tab)"
	case Escape:
		return "Esc"
	case ShiftLeft, ShiftRight:
		return "Shift"
	case ControlLeft, ControlRight:
		return "Ctrl"
	case AltLeft, AltRight:
		return "Alt"
	case MetaLeft, MetaRight:
		return "Win / Cmd"
	case ArrowUp:
		return "↑ 上箭头"
	case ArrowDown:
		return "↓ 下箭头"
	case ArrowLeft:
		return "← 左箭头"
	case ArrowRight:
		return "→ 右箭头"
	case Numpad0:
		return "小键盘 0"
	case Numpad1:
		return "小键盘 1"
	case Numpad2:
		return "小键盘 2"
	case Numpad3:
		return "小键盘 3"
	case Numpad4:
		return "小键盘 4"
	case Numpad5:
		return "小键盘 5"
	case Numpad6:
		return "小键盘 6"
	case Numpad7:
		return "小键盘 7"
	case Numpad8:
		return "小键盘 8"
	case Numpad9:
		return "小键盘 9"
	case NumpadAdd:
		return "小键盘 +"
	case NumpadSubtract:
		return "小键盘 -"
	case NumpadMultiply:
		return "小键盘 *"
	case NumpadDivide:
		return "小键盘 /"
	case NumpadDecimal:
		return "小键盘 ."
	case NumpadEnter:
		return "小键盘 回车"
	case NumpadEqual:
		return "小键盘 ="
	case NumLock:
		return "数字锁定 (NumLock)"
	default:
		if len(code) > 3 && code[:3] == "Key" {
			return code[3:]
		}
		if len(code) > 5 && code[:5] == "Digit" {
			return code[5:]
		}
		return code
	}
}
