package audio

// Opus 编码器（cgo libopus 薄封装）。
//
// 浮点 PCM 以 20ms 帧编码为 Opus（128kbps 立体声），相比 float32 PCM
// 带宽降低约 24 倍（384KB/s → 16KB/s），并为 UDP/跨网段场景提供
// FEC/PLC 能力基础。
//
// 链接：macOS 用 Homebrew libopus；Windows 用仓库内交叉编译静态库
// third_party/libopus/win64/libopus.a。

/*
#cgo darwin CFLAGS: -I/opt/homebrew/opt/opus/include
#cgo darwin LDFLAGS: -L/opt/homebrew/opt/opus/lib -lopus
#cgo windows CFLAGS: -I${SRCDIR}/../../third_party/libopus/include
#cgo windows LDFLAGS: -L${SRCDIR}/../../third_party/libopus/win64 -lopus
#include <opus/opus.h>

// opus_encoder_ctl 是变参函数，cgo 无法直接调用——通过固定参数包装转发。
static int ctl_bitrate(void *enc, int bitrate) {
	return opus_encoder_ctl((OpusEncoder *)enc, OPUS_SET_BITRATE_REQUEST, bitrate);
}
static int ctl_complexity(void *enc, int complexity) {
	return opus_encoder_ctl((OpusEncoder *)enc, OPUS_SET_COMPLEXITY_REQUEST, complexity);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

const (
	opusSampleRate  = 48000
	opusChannels    = 2
	opusFrameSize   = 960  // 20ms @ 48kHz
	opusMaxPacket   = 4000 // Opus 官方推荐最大包大小
	opusApplication = 2049 // OPUS_APPLICATION_AUDIO
)

// OpusEncoder 流式编码器：Feed 任意长度交错 float32 采样，
// 内部积攒到 20ms 帧后编码，通过 onPacket 回调输出 Opus 包。
type OpusEncoder struct {
	enc *C.OpusEncoder

	accum []float32 // 采样积攒缓冲（交错 L,R）

	onPacket func([]byte)
	packet   []byte
}

// NewOpusEncoder 创建 48kHz 立体声 128kbps 音频编码器。
func NewOpusEncoder(onPacket func([]byte)) (*OpusEncoder, error) {
	e := &OpusEncoder{onPacket: onPacket, packet: make([]byte, opusMaxPacket)}
	var errCode C.int
	e.enc = C.opus_encoder_create(opusSampleRate, opusChannels, opusApplication, (*C.int)(&errCode))
	if errCode != 0 || e.enc == nil {
		return nil, fmt.Errorf("opus_encoder_create 失败: %d", errCode)
	}
	if rc := C.ctl_bitrate(unsafe.Pointer(e.enc), 128000); rc < 0 {
		C.opus_encoder_destroy(e.enc)
		return nil, fmt.Errorf("设置比特率失败: %d", int(rc))
	}
	if rc := C.ctl_complexity(unsafe.Pointer(e.enc), 8); rc < 0 {
		C.opus_encoder_destroy(e.enc)
		return nil, fmt.Errorf("设置复杂度失败: %d", int(rc))
	}
	return e, nil
}

// Destroy 释放编码器资源。
func (e *OpusEncoder) Destroy() {
	if e.enc != nil {
		C.opus_encoder_destroy(e.enc)
		e.enc = nil
	}
}

// Feed 交错 float32 采样（长度任意，内部按 20ms 帧切分编码）。
func (e *OpusEncoder) Feed(samples []float32) error {
	if e.enc == nil || len(samples) == 0 {
		return nil
	}
	e.accum = append(e.accum, samples...)
	for len(e.accum) >= opusFrameSize*opusChannels {
		frame := e.accum[:opusFrameSize*opusChannels]
		if err := e.encodeFrame(frame); err != nil {
			return err
		}
		e.accum = e.accum[opusFrameSize*opusChannels:]
	}
	return nil
}

// encodeFrame 编码一个 20ms 帧（float 输入，opus_encode 内部转 int16）。
func (e *OpusEncoder) encodeFrame(frame []float32) error {
	pcm := (*C.opus_int16)(unsafe.Pointer(&frame[0]))
	n := C.opus_encode(e.enc, pcm, opusFrameSize,
		(*C.uchar)(unsafe.Pointer(&e.packet[0])), C.opus_int32(len(e.packet)))
	if n < 0 {
		return fmt.Errorf("opus_encode 失败: %d", int(n))
	}
	if e.onPacket != nil {
		e.onPacket(e.packet[:int(n)])
	}
	return nil
}
