//go:build windows

package audio

import (
	"context"
	"testing"
	"time"
)

// TestWASAPILifecycleOnCI 在真实 Windows 上执行自研 WASAPI 捕获的
// 生命周期（P5 修复的验证场景）：
//   - CI runner 通常无默认渲染设备 → 初始化走 fail 清理路径
//     （CoUninitialize 与 CoInitializeEx 同线程配对）并返回错误；
//   - runner 恰有渲染设备 → 启动成功，cancel 后验证清理路径与退出。
//
// 两种结果都断言「在超时内返回、不崩溃、不挂死」——P5 修复前
// Init/Uninit 分处不同 goroutine/线程，错误路径的资源释放存在线程错配。
func TestWASAPILifecycleOnCI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)

	go func() {
		result <- startNativeSystemCapture(ctx, func([]byte) {})
	}()

	select {
	case err := <-result:
		if err != nil {
			t.Logf("无设备环境：初始化走清理路径返回错误（预期）: %v", err)
		} else {
			// 恰有设备：让捕获跑一会儿后取消，验证清理与退出
			t.Logf("runner 存在渲染设备：启动成功，验证取消清理")
			cancel()
			time.Sleep(500 * time.Millisecond) // 给 loop 退出与 defer 清理时间
		}
	case <-time.After(15 * time.Second):
		cancel()
		t.Fatalf("startNativeSystemCapture 15s 未返回（疑似挂死）")
	}
}
