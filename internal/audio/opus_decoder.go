package audio

// Opus 解码器（cgo libopus 薄封装）。
//
// 为什么需要生产化解码器：本机 UI 客户端不一定有 WebCodecs AudioDecoder——
// macOS 的 Wails 窗口跑在 WKWebView（Safari 内核，当前版本只有 VideoDecoder，
// 没有 AudioDecoder），Safari 浏览器同样没有。这类客户端播放不了 AUPS/AUPM
// Opus 流。服务端在转发对端音频时用本解码器还原成交错 float32 PCM（AUDS/AUDM），
// 再按客户端能力分发（见 server/hub.go 的音频路由与 peer_manager.go 的转发）。
//
// 链接参数与 opus_encoder.go 一致：macOS Homebrew libopus；Windows 仓库静态库。

/*
#include <opus/opus.h>

// 解码入口固定用 opus_decode_float：编码端走 opus_encode_float，
// 解出后直接是 float32 交错采样，与前端 Web Audio 播放器所需格式一致。
static int decode_float(void *dec, const unsigned char *data, int len, float *pcm, int frame_size) {
	return opus_decode_float((OpusDecoder *)dec, data, len, pcm, frame_size, 0);
}
*/
import "C"

import (
	"encoding/binary"
	"fmt"
	"math"
	"unsafe"
)

// OpusDecoder 流式解码器（48kHz 立体声，与编码端一致）。
// 每个音频流（系统声/麦克风）各用一个实例：Opus 解码器是有状态的，
// 混用不同流会导致状态错乱。
type OpusDecoder struct {
	dec *C.OpusDecoder
	pcm []float32
}

// NewOpusDecoder 创建 48kHz 立体声解码器。
func NewOpusDecoder() (*OpusDecoder, error) {
	var errCode C.int
	dec := C.opus_decoder_create(opusSampleRate, opusChannels, &errCode)
	if errCode != 0 || dec == nil {
		return nil, fmt.Errorf("opus_decoder_create 失败: %d", errCode)
	}
	return &OpusDecoder{
		dec: dec,
		pcm: make([]float32, opusFrameSize*opusChannels),
	}, nil
}

// Destroy 释放解码器资源。
func (d *OpusDecoder) Destroy() {
	if d.dec != nil {
		C.opus_decoder_destroy(d.dec)
		d.dec = nil
	}
}

// Decode 解码单个 Opus 包，返回交错（L,R,L,R...）float32 采样。
// 返回切片复用内部缓冲，调用方需在下次 Decode 前使用完。
func (d *OpusDecoder) Decode(packet []byte) ([]float32, error) {
	if d.dec == nil {
		return nil, fmt.Errorf("解码器已释放")
	}
	var ptr *C.uchar
	if len(packet) > 0 {
		ptr = (*C.uchar)(unsafe.Pointer(&packet[0]))
	}
	n := C.decode_float(unsafe.Pointer(d.dec), ptr, C.int(len(packet)),
		(*C.float)(unsafe.Pointer(&d.pcm[0])), C.int(opusFrameSize))
	if n < 0 {
		return nil, fmt.Errorf("opus_decode_float 失败: %d", int(n))
	}
	return d.pcm[:int(n)*opusChannels], nil
}

// DecodeToPCM 解码并序列化为前端约定的 float32 小端字节流（AUDS/AUDM 负载）。
// 与捕获链路的 pcm 字节格式一致（Web Audio 侧按 Float32Array 读取）。
func (d *OpusDecoder) DecodeToPCM(packet []byte) ([]byte, error) {
	samples, err := d.Decode(packet)
	if err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return nil, nil
	}
	out := make([]byte, len(samples)*4)
	for i, v := range samples {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(v))
	}
	return out, nil
}
