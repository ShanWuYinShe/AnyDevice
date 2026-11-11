package audio

// Opus 编码器（cgo libopus 薄封装）。
//
// 浮点 PCM 以 20ms 帧编码为 Opus（默认 128kbps 立体声；发送端按负载在
// 96-160kbps 间自适应，见 adaptive.go），相比 float32 PCM 带宽降低约
// 20-24 倍（384KB/s → 12-20KB/s），并为 UDP/跨网段场景提供 FEC/PLC 能力基础。
//
// 链接：libopus 静态库随仓库 vendored（third_party/libopus/），macOS 与 Windows
// 构建/运行都不依赖 Homebrew 或任何外部安装：
//   - macOS arm64: third_party/libopus/macos-arm64/libopus.a（opus 1.6.1）
//   - macOS amd64: third_party/libopus/macos-amd64/libopus.a（opus 1.6.1，本机
//     交叉编译；供 Intel Mac 与 Universal 包使用）
//   - Windows x64: third_party/libopus/win64/libopus.a（mingw 交叉编译）
// 目录内只有 .a、没有 .dylib/.dll，链接结果是静态的——分发 .app/exe 无需用户装 opus。
//   - Linux: 静态链接系统 libopus（-Wl,-Bstatic -lopus -Wl,-Bdynamic），构建需
//     libopus-dev，运行期不再需要 libopus0（GTK/WebKitGTK 仍为动态依赖）

/*
#cgo darwin CFLAGS: -I${SRCDIR}/../../third_party/libopus/include
#cgo darwin,arm64 LDFLAGS: -L${SRCDIR}/../../third_party/libopus/macos-arm64 -lopus
#cgo darwin,amd64 LDFLAGS: -L${SRCDIR}/../../third_party/libopus/macos-amd64 -lopus
#cgo windows CFLAGS: -I${SRCDIR}/../../third_party/libopus/include
#cgo windows LDFLAGS: -L${SRCDIR}/../../third_party/libopus/win64 -lopus
// Linux 使用系统 libopus 的静态库（Debian/Ubuntu: apt install libopus-dev；
// Fedora: dnf install opus-devel）。系统包同时提供 libopus.a，用链接器的
// -Bstatic/-Bdynamic 把 -lopus 夹在中间强制静态链接——产物不再依赖运行期
// libopus0。GTK/WebKitGTK 无法静态化（官方不提供 .a，且 WebKit 的进程模型
// 要求动态加载），运行库仍需要。
// 注意不能用 GNU ld 的 -l:libopus.a 精确文件名写法：cgo 的安全检查只接受
// -l 后不含 ':' 的形式，否则报 invalid flag in #cgo LDFLAGS。
#cgo linux CFLAGS: -I${SRCDIR}/../../third_party/libopus/include
#cgo linux LDFLAGS: -Wl,-Bstatic -lopus -Wl,-Bdynamic
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
	"math"
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

// NewOpusEncoder 创建 48kHz 立体声音频编码器，初始参数取默认平衡档
// （128kbps / 复杂度 5，见 adaptive.go）；运行期由发送端自适应控制器
// 按负载用 SetBitrate/SetComplexity 调整。
func NewOpusEncoder(onPacket func([]byte)) (*OpusEncoder, error) {
	// accum 预分配两帧容量：采集链路按 10ms 块喂入，20ms 编码后剩余 10ms
	// 留在缓冲里；固定容量 + 原地左移可避免每帧重新分配（旧实现每次编码后
	// 用 e.accum = e.accum[N:] 前移，容量很快耗尽并反复扩容）。
	e := &OpusEncoder{
		onPacket: onPacket,
		packet:   make([]byte, opusMaxPacket),
		accum:    make([]float32, 0, opusFrameSize*opusChannels*2),
	}
	var errCode C.int
	e.enc = C.opus_encoder_create(opusSampleRate, opusChannels, opusApplication, (*C.int)(&errCode))
	if errCode != 0 || e.enc == nil {
		return nil, fmt.Errorf("opus_encoder_create 失败: %d", errCode)
	}
	// 初始参数取平衡档（128kbps / 复杂度 5）：Opus 默认复杂度偏高（libopus
	// 默认 10，此处原为 8），而实时串流只需要「够好的语音/音乐质量」；复杂度 5
	// 可明显减少编码 CPU（游戏满载时对其它程序的抢占更小），128kbps 下的听感
	// 差异基本不可辨。运行期由自适应控制器在 Low/Quality 档之间调整。
	bitrate, complexity := LevelBalanced.params()
	if rc := C.ctl_bitrate(unsafe.Pointer(e.enc), C.int(bitrate)); rc < 0 {
		C.opus_encoder_destroy(e.enc)
		return nil, fmt.Errorf("设置比特率失败: %d", int(rc))
	}
	if rc := C.ctl_complexity(unsafe.Pointer(e.enc), C.int(complexity)); rc < 0 {
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

// SetBitrate 调整编码目标比特率（bit/s）。
//
// 并发约束：libopus 编码器不是并发安全的，本方法必须与 Feed 串行化——
// 由 Engine.opusMu 保证（encodeLoop 在持有该锁时调用，Stop 销毁编码器也持锁）。
func (e *OpusEncoder) SetBitrate(bps int) error {
	if e.enc == nil {
		return fmt.Errorf("编码器未初始化")
	}
	if rc := C.ctl_bitrate(unsafe.Pointer(e.enc), C.int(bps)); rc < 0 {
		return fmt.Errorf("设置比特率失败: %d", int(rc))
	}
	return nil
}

// SetComplexity 调整编码复杂度（libopus 取值范围 0-10，数值越高音质越好、
// CPU 占用越高）。
//
// 并发约束同 SetBitrate：必须与 Feed 串行化（由 Engine.opusMu 保证）。
func (e *OpusEncoder) SetComplexity(c int) error {
	if e.enc == nil {
		return fmt.Errorf("编码器未初始化")
	}
	if rc := C.ctl_complexity(unsafe.Pointer(e.enc), C.int(c)); rc < 0 {
		return fmt.Errorf("设置复杂度失败: %d", int(rc))
	}
	return nil
}

// Feed 交错 float32 采样（长度任意，内部按 20ms 帧切分编码）。
func (e *OpusEncoder) Feed(samples []float32) error {
	if e.enc == nil || len(samples) == 0 {
		return nil
	}
	e.accum = append(e.accum, samples...)
	return e.drain()
}

// FeedF32LE 直接消费小端 float32 字节流（采集链路格式），按 20ms 帧切分编码。
// 采集回调拿到的就是该格式：直接追加转换后的样本，省掉每帧一份 []float32
// 中转分配（10ms 立体声 3.8KB，两路流约 0.75MB/s 的堆分配与一次整帧拷贝）。
// 字节序：仅支持小端平台（amd64/arm64/386 等；本项目 vendored libopus 也只
// 覆盖 darwin/windows/linux 的 x86_64 与 aarch64）。
func (e *OpusEncoder) FeedF32LE(b []byte) error {
	if e.enc == nil || len(b) < 4 {
		return nil
	}
	n := len(b) / 4
	need := len(e.accum) + n
	if need > cap(e.accum) {
		grown := make([]float32, len(e.accum), need+opusFrameSize*opusChannels)
		copy(grown, e.accum)
		e.accum = grown
	}
	start := len(e.accum)
	e.accum = e.accum[:need]
	for i := 0; i < n; i++ {
		bits := uint32(b[i*4]) | uint32(b[i*4+1])<<8 | uint32(b[i*4+2])<<16 | uint32(b[i*4+3])<<24
		e.accum[start+i] = math.Float32frombits(bits)
	}
	return e.drain()
}

// drain 把积攒的采样按 20ms 帧编码，剩余不足一帧的样本原地左移到缓冲头部
// （不重新分配：容量在构造时已按两帧预分配，正常喂入不会扩容）。
func (e *OpusEncoder) drain() error {
	frameSamples := opusFrameSize * opusChannels
	for len(e.accum) >= frameSamples {
		if err := e.encodeFrame(e.accum[:frameSamples]); err != nil {
			return err
		}
		copy(e.accum, e.accum[frameSamples:])
		e.accum = e.accum[:len(e.accum)-frameSamples]
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
