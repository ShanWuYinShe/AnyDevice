package util

import "os"

// DebugKeys 返回是否开启键盘链路端到端调试日志（环境变量 ANY_DEVICE_KB_DEBUG=1）。
// 覆盖三段：捕获端引擎发出的事件、执行端收到的事件、模拟执行的结果，
// 用于定位“按键在哪一环丢失”。
func DebugKeys() bool {
	return os.Getenv("ANY_DEVICE_KB_DEBUG") != ""
}
