package audio

// Opus 编码器（cgo libopus 薄封装）。
//
// 浮点 PCM 以 20ms 帧编码为 Opus（128kbps 立体声），相比 float32 PCM
// 带宽降低约 24 倍（384KB/s → 16KB/s），并为 UDP/跨网段场景提供
// FEC/PLC 能力基础。
//
// 链接：libopus 静态库随仓库 vendored（third_party/libopus/），macOS 与 Windows
// 构建/运行都不依赖 Homebrew 或任何外部安装：
//   - macOS arm64: third_party/libopus/macos-arm64/libopus.a（opus 1.6.1）
//   - macOS amd64: third_party/libopus/macos-amd64/libopus.a（opus 1.6.1，本机
//     交叉编译；供 Intel Mac 与 Universal 包使用）
//   - Windows x64: third_party/libopus/win64/libopus.a（mingw 交叉编译）
// 目录内只有 .a、没有 .dylib/.dll，链接结果是静态的——分发 .app/exe 无需用户装 opus。
//   - Linux: 静态链接系统 libopus（-l:libopus.a），构建需 libopus-dev，
//     运行期不再需要 libopus0（GTK/WebKitGTK 仍为动态依赖）

/*
#cgo darwin CFLAGS: -I${SRCDIR}/../../third_party/libopus/include
#cgo darwin,arm64 LDFLAGS: -L${SRCDIR}/../../third_party/libopus/macos-arm64 -lopus
#cgo darwin,amd64 LDFLAGS: -L${SRCDIR}/../../third_party/libopus/macos-amd64 -lopus
#cgo windows CFLAGS: -I${SRCDIR}/../../third_party/libopus/include
#cgo windows LDFLAGS: -L${SRCDIR}/../../third_party/libopus/win64 -lopus
// Linux 使用系统 libopus 的静态库（Debian/Ubuntu: apt install libopus-dev；
// Fedora: dnf install opus-devel）。系统包同时提供 libopus.a，用 -l:libopus.a
// 强制静态链接——产物不再依赖运行期 libopus0。GTK/WebKitGTK 无法静态化
//（官方不提供 .a，且 WebKit 的进程模型要求动态加载），运行库仍需要。
#cgo linux CFLAGS: -I${SRCDIR}/../../third_party/libopus/include
#cgo linux LDFLAGS: -l:libopus.a
#include <opus/opus.h>

// opus_encoder_ctl 是变参函数，cgo 无法直接调用——通过固定参数包装转发。
static int ctl_bitrate(void *enc, int bitrate) {
	return opus_encoder_ctl((OpusEncoder *)enc, OPUS_SET_BITRATE_REQUEST, bitrate);
}
static int ctl_complexity(void *enc, int complexity) {
	return opus_encoder_ctl((OpusEncoder *)enc, OPUS_SET_COMPLEXITY_REQUEST, complexity);
}

// 捕获链路的采样是 float32（[-1,1]），必须走 opus_encode_float。
// 注意：opus_encode（int16 入口）与 opus_encode_float 是两套独立入口，
// 位模式不可互转——把 float32 缓冲按 opus_int16* 传入会让每个采样的位
// 被当成两个 int16 读取，编码出来是严重失真（听感"破音/电流声"）。
static int encode_float(void *enc, const float *pcm, int frame_size,
                        unsigned char *data, int max_data_bytes) {
	return opus_encode_float((OpusEncoder *)enc, pcm, frame_size, data, max_data_bytes);
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

// encodeFrame 编码一个 20ms 帧。
// 输入 frame 是 float32 采样（[-1,1] 交错立体声），走 opus_encode_float；
// float 与 int16 是两套不同入口，位模式不可互转（曾因此产生破音）。
func (e *OpusEncoder) encodeFrame(frame []float32) error {
	n := C.encode_float(unsafe.Pointer(e.enc), (*C.float)(unsafe.Pointer(&frame[0])), opusFrameSize,
		(*C.uchar)(unsafe.Pointer(&e.packet[0])), C.opus_int32(len(e.packet)))
	if n < 0 {
		return fmt.Errorf("opus_encode_float 失败: %d", int(n))
	}
	if e.onPacket != nil {
		e.onPacket(e.packet[:int(n)])
	}
	return nil
}
