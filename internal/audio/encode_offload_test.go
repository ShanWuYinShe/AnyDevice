package audio

import (
	"sync"
	"testing"
	"time"
)

// TestEncodeOffload 验证编码卸载链路：emitPCM 拷贝入队 → encodeLoop 异步
// 编码 → onChunk 收到带 tag 的 Opus 输出；队列满时丢最旧并计数。
// 不调用 Start/Stop（避免真实音频设备依赖），手动驱动生命周期。
func TestEncodeOffload(t *testing.T) {
	var mu sync.Mutex
	var got [][]byte
	e := NewEngine(KindSystem, ModeCapture, -1, func(b []byte) {
		mu.Lock()
		got = append(got, append([]byte(nil), b...))
		mu.Unlock()
	})
	e.SetSubscribers(1)

	// 手动进入运行态并启动编码消费者（等价 Start 的卸载初始化）
	e.encQueue = make(chan []byte, 8)
	e.encDone = make(chan struct{})
	e.isRunning.Store(true)
	go e.encodeLoop()
	defer func() {
		e.isRunning.Store(false)
		close(e.encDone)
	}()

	// 投 20 帧（每帧 20ms 立体声 float32）：队列容量 8，编码消费慢于投递，
	// 应出现丢最旧（dbgDropped > 0），且 onChunk 仍持续收到输出
	for i := 0; i < 20; i++ {
		e.emitPCM(make([]byte, 7680))
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	n := len(got)
	var first []byte
	if n > 0 {
		first = got[0]
	}
	mu.Unlock()
	if n < 5 {
		t.Fatalf("异步编码输出不足: got %d packets, want >= 5", n)
	}
	// 输出必须带 AUPS tag（系统声 Opus 流）
	if len(first) < 4 || string(first[:4]) != string(TagSystemAudioOpus) {
		t.Fatalf("输出首包 tag 错误: got %q", string(first[:4]))
	}
	if dropped := e.dbgDropped.Load(); dropped == 0 {
		t.Fatalf("队列满时应记录丢帧计数，实际 dbgDropped=0")
	}
}

// TestEncodeOffloadLateFrame 验证 Stop 后到达的迟到帧不会重建孤儿编码器：
// isRunning=false 时 encodeLoop 直接丢弃帧（P1 修复的对称保护）。
func TestEncodeOffloadLateFrame(t *testing.T) {
	e := NewEngine(KindSystem, ModeCapture, -1, func([]byte) {})
	e.encQueue = make(chan []byte, 8)
	e.encDone = make(chan struct{})
	e.isRunning.Store(true)
	go e.encodeLoop()

	// 模拟 Stop：先关运行闸（编码器从未创建，opus 恒 nil）
	e.isRunning.Store(false)
	for i := 0; i < 4; i++ {
		e.emitPCM(make([]byte, 7680))
	}
	close(e.encDone)

	// 给编码 goroutine 处理时间，随后确认没有孤儿编码器被创建
	time.Sleep(100 * time.Millisecond)
	e.opusMu.Lock()
	orphan := e.opus != nil
	e.opusMu.Unlock()
	if orphan {
		t.Fatalf("Stop 后迟到帧不应重建编码器（孤儿泄漏）")
	}
}
