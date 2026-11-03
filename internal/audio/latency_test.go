package audio

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestLatencyStats 验证发送端延迟分项统计：
//   - chunkMs = 采集块时长（3840B = 10ms 立体声 float32）；
//   - encodeMs 有采集回调→编码完成的样本（EWMA，非负且不异常）。
func TestLatencyStats(t *testing.T) {
	var packets atomic.Int64
	e := NewEngine(KindSystem, ModeCapture, -1, func([]byte) { packets.Add(1) })
	e.encQueue = make(chan queuedFrame, 8)
	e.encDone = make(chan struct{})
	e.isRunning.Store(true)
	e.SetSubscribers(1)
	go e.encodeLoop(e.encQueue, e.encDone)
	defer func() {
		e.isRunning.Store(false)
		close(e.encDone)
	}()

	// 4 × 10ms chunk = 2 个 20ms Opus 包
	frame := make([]byte, 3840)
	for i := 0; i < 4; i++ {
		e.emitPCM(frame)
	}
	// 等编码完成（chunkMs 在 emitPCM 入队时就已更新，不能作为完成信号）
	waitCond(t, 2*time.Second, func() bool { return packets.Load() >= 2 })

	chunk, encode := e.LatencyStats()
	if chunk != 10 {
		t.Fatalf("采集块时长应为 10ms，实际 %dms", chunk)
	}
	// 本机编码极快时 encode 可为 0ms（EWMA 从 0 起算），只断言上界防异常值
	if encode > 500 {
		t.Fatalf("编码延迟异常: %dms", encode)
	}
	if got := packets.Load(); got < 2 {
		t.Fatalf("应产出至少 2 个 Opus 包，实际 %d", got)
	}
}
