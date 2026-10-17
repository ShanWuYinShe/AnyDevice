//go:build darwin

package keyboard

/*
#cgo LDFLAGS: -framework ApplicationServices
#include <ApplicationServices/ApplicationServices.h>
*/
import "C"

// AccessibilityTrusted 报告当前进程是否已获 macOS「辅助功能」授权。
// 键盘/鼠标的跨端注入（CGEventPost）必须持有该授权，否则系统会
// 静默丢弃所有模拟事件——表现为按键映射完全无反应且无任何报错。
func AccessibilityTrusted() bool {
	return C.AXIsProcessTrusted() != 0
}
