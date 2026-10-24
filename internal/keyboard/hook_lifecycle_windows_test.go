//go:build windows

package keyboard

import (
	"testing"
	"time"
)

// TestHookStartStopCycles 锻炼 Windows 低级键盘钩子的启停路径（P4 修复场景），
// 与 mouse 包同名测试同模式：立即 Stop 走 P4 修复分支、正常运行走卸载路径、
// 反复启停验证状态机。LL 钩子线程内部的卸载无法从测试观测，本测试保证
// 调用路径不崩溃、可反复启停。
func TestHookStartStopCycles(t *testing.T) {
	e := NewEngine(func(KeyEvent) {})

	e.Start()
	e.Stop()

	e.Start()
	time.Sleep(100 * time.Millisecond)
	e.Stop()

	e.Start()
	time.Sleep(100 * time.Millisecond)
	e.Stop()

	for i := 0; i < 5; i++ {
		e.Start()
		time.Sleep(20 * time.Millisecond)
		e.Stop()
	}
}
