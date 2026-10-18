package util

import "os"

// DebugKeys 返回是否开启键盘链路端到端调试日志（环境变量 ANY_DEVICE_KB_DEBUG=1）。
// 覆盖三段：捕获端引擎发出的事件、执行端收到的事件、模拟执行的结果，
// 用于定位“按键在哪一环丢失”。
func DebugKeys() bool {
	return os.Getenv("ANY_DEVICE_KB_DEBUG") != ""
}

// DebugAudio 返回是否开启音频链路端到端调试日志（环境变量 ANY_DEVICE_AUDIO_DEBUG=1）。
// 覆盖：捕获端 chunk 生成速率与字节数、转发端收发帧统计，
// 用于定位“音频在哪一环丢失或为静音数据”。
func DebugAudio() bool {
	return os.Getenv("ANY_DEVICE_AUDIO_DEBUG") != ""
}

// EnableTestTone 返回是否启用占位测试音（环境变量 ANY_DEVICE_TEST_TONE=1）。
// 默认关闭：无虚拟声卡时系统声捕获不可用就直接静默并提示，
// 不生成任何占位声音（避免干扰用户）。仅在调试链路时手动开启。
func EnableTestTone() bool {
	return os.Getenv("ANY_DEVICE_TEST_TONE") != ""
}
