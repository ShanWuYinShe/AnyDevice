//go:build !windows

package audio

import (
	"context"
	"errors"
)

// startNativeSystemCapture 仅 Windows 提供原生 WASAPI Loopback 实现，
// 其余平台返回错误，调用方回退到 miniaudio 路径（macOS 用 BlackHole 等虚拟设备）。
// 退出确认 channel 与 Windows 实现对齐签名：本实现不启动 goroutine，恒返回 nil。
func startNativeSystemCapture(ctx context.Context, onPCM func([]byte)) (<-chan struct{}, error) {
	_ = ctx
	_ = onPCM
	return nil, errors.New("原生 WASAPI Loopback 仅支持 Windows")
}
