package audio

import (
	"encoding/binary"
	"math"
	"testing"
)

// encodeChunks 用指定编码路径把若干 10ms 立体声块编码成 Opus 包。
func encodeChunks(t *testing.T, useBytes bool) [][]byte {
	t.Helper()
	var packets [][]byte
	enc, err := NewOpusEncoder(func(p []byte) {
		packets = append(packets, append([]byte(nil), p...))
	})
	if err != nil {
		t.Fatalf("创建编码器失败: %v", err)
	}
	defer enc.Destroy()

	const frames = 480 // 10ms @ 48kHz（立体声 960 样本）
	samples := make([]float32, frames*2)
	for i := 0; i < frames; i++ {
		v := float32(0.3 * math.Sin(2*math.Pi*440*float64(i)/48000))
		samples[i*2] = v
		samples[i*2+1] = v
	}
	raw := make([]byte, len(samples)*4)
	for i, v := range samples {
		binary.LittleEndian.PutUint32(raw[i*4:], math.Float32bits(v))
	}
	for n := 0; n < 4; n++ { // 40ms 数据 → 2 个 20ms 帧
		if useBytes {
			if err := enc.FeedF32LE(raw); err != nil {
				t.Fatalf("FeedF32LE 失败: %v", err)
			}
		} else if err := enc.Feed(samples); err != nil {
			t.Fatalf("Feed 失败: %v", err)
		}
	}
	return packets
}

// TestFeedF32LEMatchesFeed：字节入口与 float32 入口必须产出完全相同的 Opus 包
// （同一编码器参数、同一信号，libopus 确定性输出）。
func TestFeedF32LEMatchesFeed(t *testing.T) {
	viaFloat := encodeChunks(t, false)
	viaBytes := encodeChunks(t, true)
	if len(viaFloat) != 2 || len(viaBytes) != 2 {
		t.Fatalf("期望各 2 个 Opus 包，实际 float=%d bytes=%d", len(viaFloat), len(viaBytes))
	}
	for i := range viaFloat {
		if string(viaFloat[i]) != string(viaBytes[i]) {
			t.Fatalf("第 %d 个 Opus 包不一致：float=%d 字节 bytes=%d 字节", i, len(viaFloat[i]), len(viaBytes[i]))
		}
	}
}

// TestFeedF32LENoPerChunkAllocs 防止采集编码链路回退到「每块分配一份
// []float32 中转」：固定容量缓冲 + 原地左移后，稳态每 10ms 块不应有堆分配。
func TestFeedF32LENoPerChunkAllocs(t *testing.T) {
	enc, err := NewOpusEncoder(func([]byte) {})
	if err != nil {
		t.Fatalf("创建编码器失败: %v", err)
	}
	defer enc.Destroy()
	raw := make([]byte, 480*2*4) // 10ms 立体声 float32（960 样本）
	for i := 0; i < 4; i++ {     // 预热到稳态（两帧已编码、余量在缓冲内）
		if err := enc.FeedF32LE(raw); err != nil {
			t.Fatalf("预热 FeedF32LE 失败: %v", err)
		}
	}
	allocs := testing.AllocsPerRun(200, func() {
		if err := enc.FeedF32LE(raw); err != nil {
			t.Fatalf("FeedF32LE 失败: %v", err)
		}
	})
	if allocs > 0.05 {
		t.Fatalf("稳态每 10ms 块分配数应≈0，实际 %.3f allocs/op", allocs)
	}
}

// TestSetEncoderParams 验证运行期调整码率/复杂度（发送端自适应切换）后
// 编码器仍正常出包且包可被生产解码器解出；销毁后调用应返回错误而非崩溃。
func TestSetEncoderParams(t *testing.T) {
	var packets [][]byte
	enc, err := NewOpusEncoder(func(p []byte) {
		packets = append(packets, append([]byte(nil), p...))
	})
	if err != nil {
		t.Fatalf("创建编码器失败: %v", err)
	}
	defer enc.Destroy()

	frame := make([]float32, opusFrameSize*opusChannels) // 20ms 正弦（避免静音快速路径）
	for i := 0; i < opusFrameSize; i++ {
		v := float32(0.3 * math.Sin(2*math.Pi*440*float64(i)/48000))
		frame[i*2] = v
		frame[i*2+1] = v
	}
	feed := func() {
		t.Helper()
		if err := enc.Feed(frame); err != nil {
			t.Fatalf("编码失败: %v", err)
		}
	}

	// 平衡档（默认）→ 低档 → 高档：每次切换后都应正常产出 1 个 20ms 包
	feed()
	if err := enc.SetBitrate(96000); err != nil {
		t.Fatalf("SetBitrate(96000) 失败: %v", err)
	}
	if err := enc.SetComplexity(3); err != nil {
		t.Fatalf("SetComplexity(3) 失败: %v", err)
	}
	feed()
	if err := enc.SetBitrate(160000); err != nil {
		t.Fatalf("SetBitrate(160000) 失败: %v", err)
	}
	if err := enc.SetComplexity(7); err != nil {
		t.Fatalf("SetComplexity(7) 失败: %v", err)
	}
	feed()

	if len(packets) != 3 {
		t.Fatalf("期望 3 个 Opus 包，实际 %d", len(packets))
	}
	dec, err := NewOpusDecoder()
	if err != nil {
		t.Fatalf("创建解码器失败: %v", err)
	}
	defer dec.Destroy()
	for i, pkt := range packets {
		if len(pkt) == 0 {
			t.Fatalf("第 %d 个 Opus 包为空", i)
		}
		samples, err := dec.Decode(pkt)
		if err != nil {
			t.Fatalf("第 %d 个 Opus 包解码失败: %v", i, err)
		}
		if len(samples) != opusFrameSize*opusChannels {
			t.Fatalf("第 %d 个包解码样本数错误：%d", i, len(samples))
		}
	}

	// 销毁后调用应返回错误（不 panic / 不越界访问）
	enc.Destroy()
	if err := enc.SetBitrate(128000); err == nil {
		t.Fatal("编码器销毁后 SetBitrate 应返回错误")
	}
	if err := enc.SetComplexity(5); err == nil {
		t.Fatal("编码器销毁后 SetComplexity 应返回错误")
	}
}

// BenchmarkFeedF32LE 量化采集编码链路的每块开销（含内存分配）。
func BenchmarkFeedF32LE(b *testing.B) {
	enc, err := NewOpusEncoder(func([]byte) {})
	if err != nil {
		b.Fatal(err)
	}
	defer enc.Destroy()
	raw := make([]byte, 960*2*4)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := enc.FeedF32LE(raw); err != nil {
			b.Fatal(err)
		}
	}
}
