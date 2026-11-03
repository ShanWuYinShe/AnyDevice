//go:build darwin || linux

package audio

import "testing"

// TestSystemAudioFallbackTwiceNoDevice 回归「按需启停后第二次 Start 误采麦克风」：
// 非 Windows 平台没有虚拟回环设备时，系统声引擎每次 Start 都必须回退为
// 「不可用/测试音」，绝不能因为上一次 Start 写入的展示名（devName 残留）而
// 跳过回退、退化成用 Capture 设备采集默认麦克风并当作系统声推流。
func TestSystemAudioFallbackTwiceNoDevice(t *testing.T) {
	e := NewEngine(KindSystem, ModeAuto, -1, func([]byte) {})

	if err := e.Start(); err != nil {
		t.Skipf("本机音频上下文不可用，跳过: %v", err)
	}
	if e.device != nil {
		e.Stop()
		t.Skip("本机安装了虚拟回环设备（系统声可正常捕获），跳过无设备回退用例")
	}
	e.Stop()

	// 第二次 Start：历史上这里会因 devName 残留而误开默认采集设备（麦克风）
	if err := e.Start(); err != nil {
		t.Fatalf("第二次 Start 失败: %v", err)
	}
	defer e.Stop()
	if e.device != nil {
		t.Fatalf("第二次 Start 不应打开任何采集设备（会把默认麦克风当作系统声采集推流）")
	}
}
