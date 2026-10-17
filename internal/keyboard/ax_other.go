//go:build !darwin

package keyboard

// AccessibilityTrusted 非 darwin 平台无辅助功能授权门槛，恒为 true。
// （Windows 的 keybd_event/GetAsyncKeyState 不需要特殊授权。）
func AccessibilityTrusted() bool {
	return true
}
