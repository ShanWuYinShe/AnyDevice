package audio

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
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

// ---- 量化基准：P6 编码卸载的收益验证 ----
// BenchmarkEmitPCMOverhead 量度音频实时回调线程每帧的实际开销
// （拷贝 + 入队，P6 改造后）。
func BenchmarkEmitPCMOverhead(b *testing.B) {
	var sink atomic.Int64
	e := NewEngine(KindSystem, ModeCapture, -1, func([]byte) { sink.Add(1) })
	e.SetSubscribers(1)
	e.encQueue = make(chan []byte, 8)
	e.encDone = make(chan struct{})
	e.isRunning.Store(true)
	go e.encodeLoop()
	defer func() {
		e.isRunning.Store(false)
		close(e.encDone)
	}()
	frame := make([]byte, 7680) // 20ms 立体声 float32
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.emitPCM(frame)
	}
}

// BenchmarkOpusEncodeDirect 量度旧路径（编码在调用线程直接执行，P6 之前
// 的行为）每帧开销：与 BenchmarkEmitPCMOverhead 对比即卸载在实时线程上
// 节省的时间（差额 = cgo 建编码器摊销 + 逐样本转换 + Opus Feed）。
// 逻辑内联复刻 P6 前的 emitPCM 编码段，不依赖生产代码。
func BenchmarkOpusEncodeDirect(b *testing.B) {
	var sink atomic.Int64
	e := NewEngine(KindSystem, ModeCapture, -1, func([]byte) { sink.Add(1) })
	e.SetSubscribers(1)
	frame := make([]byte, 7680)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.opusMu.Lock()
		if e.opus == nil {
			opus, err := NewOpusEncoder(func(packet []byte) {
				buf := make([]byte, 4+len(packet))
				copy(buf[:4], TagSystemAudioOpus)
				copy(buf[4:], packet)
				sink.Add(1)
			})
			if err != nil {
				e.opusMu.Unlock()
				b.Fatal(err)
			}
			e.opus = opus
		}
		f32 := make([]float32, len(frame)/4)
		for i := range f32 {
			f32[i] = float32(math.Float32frombits(*(*uint32)(unsafe.Pointer(&frame[i*4]))))
		}
		if err := e.opus.Feed(f32); err != nil {
			e.opusMu.Unlock()
			b.Fatal(err)
		}
		e.opusMu.Unlock()
	}
	// 收尾销毁，避免泄漏污染其他基准
	e.opusMu.Lock()
	if e.opus != nil {
		e.opus.Destroy()
		e.opus = nil
	}
	e.opusMu.Unlock()
}
