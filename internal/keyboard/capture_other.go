//go:build !darwin && !windows

package keyboard

import "context"

// runEventCapture 平台无事件钩子实现，回退轮询模式。
func runEventCapture(ctx context.Context, emit func(KeyEvent)) bool {
	return false
}
