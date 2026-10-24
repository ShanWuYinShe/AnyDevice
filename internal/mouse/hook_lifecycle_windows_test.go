//go:build windows

package mouse

import (
	"testing"
	"time"
)

// TestHookStartStopCycles 锻炼 Windows 低级鼠标钩子的启停路径（P4 修复场景）：
//   - Start 后立即 Stop：ctx 在钩子安装完成前取消，走修复后的「等待 installed
//     结果并补发 WM_QUIT」分支，验证不 panic 且状态机复位正确；
//   - Start→运行→Stop：正常路径卸载。
//
// 若 LL 钩子在 CI 服务会话中安装失败会回退轮询模式，两种路径同样被锻炼。
// 局限：钩子线程内部的 UnhookWindowsHookEx 无法从测试观测（进程强杀才能
// 检出泄漏），本测试保证的是调用路径不崩溃、可反复启停。
func TestHookStartStopCycles(t *testing.T) {
	e := NewEngine(func(MouseEvent) {})

	// 立即 Stop：与钩子安装竞速（P4 修复分支）
	e.Start()
	e.Stop()

	// 正常运行 100ms 后 Stop
	e.Start()
	time.Sleep(100 * time.Millisecond)
	e.Stop()

	// 再次 Start/Stop：验证 isRunning 门控复位正确
	e.Start()
	time.Sleep(100 * time.Millisecond)
	e.Stop()

	// 回环多轮：反复启停不崩溃
	for i := 0; i < 5; i++ {
		e.Start()
		time.Sleep(20 * time.Millisecond)
		e.Stop()
	}
}
