package audio

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// waitCond 轮询等待条件成立（supervisor 是异步的，用轮询避免依赖内部时序）。
func waitCond(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}

// TestSupervisorOnDemand 验证订阅数驱动的按需启停：
// 无人订阅不启动采集；订阅后启动；归零 idleGrace 后停止；再次订阅重新启动。
// 用 startFn/stopFn 桩替代真实音频设备，测试在无音频设备的 CI 上也可运行。
func TestSupervisorOnDemand(t *testing.T) {
	e := NewEngine(KindSystem, ModeCapture, -1, func([]byte) {})
	e.idleGrace = 80 * time.Millisecond
	e.retryDelay = 50 * time.Millisecond

	var starts, stops atomic.Int64
	var running atomic.Bool
	e.startFn = func() error {
		starts.Add(1)
		running.Store(true)
		e.isRunning.Store(true)
		return nil
	}
	e.stopFn = func() {
		stops.Add(1)
		running.Store(false)
		e.isRunning.Store(false)
	}

	e.StartSupervisor()
	defer e.StopSupervisor()

	// 无人订阅：不应启动
	time.Sleep(150 * time.Millisecond)
	if got := starts.Load(); got != 0 {
		t.Fatalf("无人订阅时不应启动采集，实际启动 %d 次", got)
	}

	// 订阅 → 启动
	e.SetSubscribers(1)
	waitCond(t, 2*time.Second, func() bool { return running.Load() && starts.Load() == 1 })

	// 取消订阅 → idleGrace 后停止
	e.SetSubscribers(0)
	waitCond(t, 2*time.Second, func() bool { return !running.Load() && stops.Load() == 1 })

	// 再次订阅 → 重新启动（验证 Stop 后设备/编码器生命周期可复用）
	e.SetSubscribers(1)
	waitCond(t, 2*time.Second, func() bool { return running.Load() && starts.Load() == 2 })

	// 快速抖动不应把「刚停又启」变成反复启停：归零后立刻恢复订阅
	e.SetSubscribers(0)
	e.SetSubscribers(1)
	time.Sleep(200 * time.Millisecond)
	if got := stops.Load(); got != 1 {
		t.Fatalf("归零后立刻恢复订阅不应触发停止，实际停止 %d 次", got)
	}
}

// TestSupervisorStartRetry：启动失败不能热循环重试，按 retryDelay 退避，
// 设备恢复后自动启动。
func TestSupervisorStartRetry(t *testing.T) {
	e := NewEngine(KindMic, ModeCapture, -1, func([]byte) {})
	e.idleGrace = 50 * time.Millisecond
	e.retryDelay = 40 * time.Millisecond

	var calls atomic.Int64
	e.startFn = func() error {
		n := calls.Add(1)
		if n < 3 {
			return errors.New("设备暂时不可用")
		}
		e.isRunning.Store(true)
		return nil
	}
	e.stopFn = func() { e.isRunning.Store(false) }

	e.StartSupervisor()
	defer e.StopSupervisor()

	e.SetSubscribers(1)
	waitCond(t, 2*time.Second, func() bool { return e.IsRunning() && calls.Load() >= 3 })

	// 失败重试期间不能出现高频重试（每 40ms 一次，300ms 内最多约 8 次）
	if got := calls.Load(); got > 10 {
		t.Fatalf("启动失败后重试过于频繁: %d 次", got)
	}
}

// TestSupervisorDisabledGate：「本机共享」关闭后立即停止采集（不等宽限期），
// 关闭状态下新订阅不会启动采集，重新打开且仍有订阅时自动恢复。
func TestSupervisorDisabledGate(t *testing.T) {
	e := NewEngine(KindMic, ModeCapture, -1, func([]byte) {})
	e.idleGrace = 50 * time.Millisecond
	e.retryDelay = 30 * time.Millisecond

	var starts, stops atomic.Int64
	e.startFn = func() error {
		starts.Add(1)
		e.isRunning.Store(true)
		return nil
	}
	e.stopFn = func() {
		stops.Add(1)
		e.isRunning.Store(false)
	}
	e.StartSupervisor()
	defer e.StopSupervisor()

	e.SetSubscribers(1)
	waitCond(t, 2*time.Second, func() bool { return e.IsRunning() })

	// 关闭共享：立即停止，不等 idleGrace
	e.SetEnabled(false)
	waitCond(t, time.Second, func() bool { return !e.IsRunning() })
	if got := stops.Load(); got != 1 {
		t.Fatalf("关闭共享应立即停止采集，停止次数=%d", got)
	}

	// 关闭状态下追加订阅也不应启动
	e.SetSubscribers(2)
	time.Sleep(150 * time.Millisecond)
	if got := starts.Load(); got != 1 {
		t.Fatalf("共享关闭时不应启动采集，启动次数=%d", got)
	}

	// 重新打开：仍有订阅 → 恢复采集
	e.SetEnabled(true)
	waitCond(t, 2*time.Second, func() bool { return e.IsRunning() && starts.Load() == 2 })
}

// TestEncodeLoopExitsOnStop：Stop 关闭 encDone 后编码 goroutine 必须退出。
// 按需启停会反复 Start/Stop，泄漏会在长时间运行中持续堆积 goroutine。
func TestEncodeLoopExitsOnStop(t *testing.T) {
	e := NewEngine(KindSystem, ModeCapture, -1, func([]byte) {})
	e.encQueue = make(chan []byte, 8)
	e.encDone = make(chan struct{})
	e.isRunning.Store(true)

	done := make(chan struct{})
	go func() {
		defer close(done)
		e.encodeLoop(e.encQueue, e.encDone)
	}()

	close(e.encDone)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("encodeLoop 未随 Stop 退出（goroutine 泄漏）")
	}
}
