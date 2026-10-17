//go:build !darwin && !windows

package keyboard

import "context"

// ReleaseAll 平台无注入实现，空操作。
func ReleaseAll() {}

// ReleaseAllModifiers 兼容旧接口。
func ReleaseAllModifiers() {}

// runEventCapture 平台无事件钩子实现，回退轮询模式。
func runEventCapture(ctx context.Context, emit func(KeyEvent)) bool {
	return false
}

func getPressedKeys() map[string]int {
	return nil
}

func SimulateKey(keyName string, down bool) {}
