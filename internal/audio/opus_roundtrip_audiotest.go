//go:build audiotest

package audio

// 往返保真测试的说明（只在 `go test -tags audiotest ./internal/audio` 时构建）。
//
// 解码逻辑已收敛到生产实现 OpusDecoder（opus_decoder.go）：它同时用于把
// 对端 Opus 流解码成 Wails/Safari 客户端可播放的 float32 PCM。测试直接复用
// 生产解码器，避免测试专用 cgo 包装与生产实现长期漂移。
