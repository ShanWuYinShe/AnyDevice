//go:build audiotest

package audio

// Opus 编码保真回归测试：`go test -tags audiotest ./internal/audio -run RoundTrip -v`
//
// 背景（真实缺陷，2026-10-18 定位）：实现曾把 float32 采样缓冲直接按
// `opus_int16*` 传给 int16 入口 `opus_encode`。float 与 int16 是两套不同
// 入口、位模式不可互转——每个 float32 采样的位被当作两个 int16 读取，
// 编码结果是满量程噪声（实测 rms 0.578、峰值 2.07、相邻采样跳变 3.0），
// 解码端听感即"破音/电流声"。当时前端解码路径同时不可用，缺陷长期未暴露。
//
// 本测试做编码 → 解码往返，断言解出的正弦幅度与频率与输入一致，
// 用可判定的数值指标锁死这个缺陷类别。

import (
	"math"
	"testing"
)

func TestOpusRoundTripFidelity(t *testing.T) {
	const (
		packets   = 50
		frameSize = 960 // 20ms @ 48kHz
		channels  = 2
		freq      = 440.0
		amp       = 0.3
	)

	var encoded [][]byte
	enc, err := NewOpusEncoder(func(p []byte) {
		cp := make([]byte, len(p))
		copy(cp, p)
		encoded = append(encoded, cp)
	})
	if err != nil {
		t.Fatalf("创建编码器失败: %v", err)
	}
	defer enc.Destroy()

	phase, step := 0.0, 2*math.Pi*freq/48000
	for i := 0; i < packets; i++ {
		buf := make([]float32, frameSize*channels)
		for f := 0; f < frameSize; f++ {
			s := float32(amp * math.Sin(phase))
			phase += step
			buf[f*channels] = s
			buf[f*channels+1] = s
		}
		if err := enc.Feed(buf); err != nil {
			t.Fatalf("编码失败: %v", err)
		}
	}
	if len(encoded) != packets {
		t.Fatalf("期望 %d 个 Opus 包，实际 %d", packets, len(encoded))
	}

	// 直接使用生产解码器：Wails/Safari 客户端播放对端音频走的就是这条解码路径
	dec, err := NewOpusDecoder()
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer dec.Destroy()

	out := make([]float32, 0, packets*frameSize)
	for i, pkt := range encoded {
		samples, err := dec.Decode(pkt)
		if err != nil {
			t.Fatalf("第 %d 包解码失败: %v", i, err)
		}
		for f := 0; f < len(samples)/channels; f++ {
			out = append(out, samples[f*channels]) // 取左声道
		}
	}

	// 跳过编码器前导延迟（Opus pre-skip ≈ 6.5ms）与首个包瞬态
	const skip = 2400
	if len(out) <= skip {
		t.Fatalf("解码样本过少: %d", len(out))
	}
	sig := out[skip:]

	var sum, peak float64
	crossings := 0
	for i, v := range sig {
		f := float64(v)
		sum += f * f
		if a := math.Abs(f); a > peak {
			peak = a
		}
		if i > 0 && ((sig[i-1] < 0 && v >= 0) || (sig[i-1] > 0 && v <= 0)) {
			crossings++
		}
	}
	rms := math.Sqrt(sum / float64(len(sig)))
	estFreq := float64(crossings) / 2 / (float64(len(sig)) / 48000)
	wantRMS := amp / math.Sqrt2

	t.Logf("往返结果：rms=%.4f（期望 %.4f） peak=%.4f 估计频率=%.1fHz（输入 %.0fHz）",
		rms, wantRMS, peak, estFreq, freq)

	if rms < 0.19 || rms > 0.24 {
		t.Fatalf("幅度失真：rms=%.4f，期望约 %.4f（编码入口/格式用错会得到满量程噪声）", rms, wantRMS)
	}
	if peak > 0.5 {
		t.Fatalf("出现削波或噪声峰值：peak=%.3f（期望约 %.2f）", peak, amp)
	}
	if estFreq < 430 || estFreq > 450 {
		t.Fatalf("频率失真：估计 %.1fHz，期望 %.0fHz", estFreq, freq)
	}
}
