//go:build audiotest

package audio

// 往返保真测试的 cgo 支撑（只在 `go test -tags audiotest ./internal/audio` 时构建）。
//
// 为什么不放在 _test.go：Go 明确不支持在测试文件中使用 cgo
// （`use of cgo in test ... not supported`）。把最小解码包装放在构建标签后面，
// 产品构建与默认 `go test ./...` 完全不受影响，也不会引入无用的产品接口。

/*
#include <opus/opus.h>

static int decode_float(void *dec, const unsigned char *data, int len, float *pcm, int frame_size) {
	return opus_decode_float((OpusDecoder *)dec, data, len, pcm, frame_size, 0);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// newOpusDecoderForTest 创建 48kHz 立体声解码器（测试用）。
func newOpusDecoderForTest() (unsafe.Pointer, error) {
	var errCode C.int
	dec := C.opus_decoder_create(48000, 2, &errCode)
	if errCode != 0 || dec == nil {
		return nil, fmt.Errorf("opus_decoder_create 失败: %d", int(errCode))
	}
	return unsafe.Pointer(dec), nil
}

// freeOpusDecoderForTest 释放解码器（测试用）。
func freeOpusDecoderForTest(dec unsafe.Pointer) {
	if dec != nil {
		C.opus_decoder_destroy((*C.OpusDecoder)(dec))
	}
}

// decodeOpusForTest 解码单个包到交错 float32，返回解出的帧数（测试用）。
func decodeOpusForTest(dec unsafe.Pointer, packet []byte, pcm []float32) (int, error) {
	if len(packet) == 0 || len(pcm) == 0 {
		return 0, fmt.Errorf("空输入")
	}
	n := C.decode_float(dec, (*C.uchar)(unsafe.Pointer(&packet[0])), C.int(len(packet)),
		(*C.float)(unsafe.Pointer(&pcm[0])), C.int(len(pcm)/2))
	if n < 0 {
		return 0, fmt.Errorf("opus_decode_float 失败: %d", int(n))
	}
	return int(n), nil
}
