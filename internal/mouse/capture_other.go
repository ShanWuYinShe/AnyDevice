//go:build !windows && !darwin

package mouse

import "context"

func getScreenSize() (int, int) {
	return 1920, 1080
}

func getCursor() (int, int, bool, bool, bool) {
	return 960, 540, false, false, false
}

func SimulateMove(dx, dy int)               {}
func SimulateAbsolute(normX, normY float64) {}
func SimulateButton(btn string, down bool)  {}
func SimulateScroll(dy int)                 {}

// ReleaseAllButtons 平台无注入实现，空操作。
func ReleaseAllButtons() {}

// runEventCapture 平台无事件钩子实现，回退轮询模式。
func runEventCapture(ctx context.Context, emit func(MouseEvent)) bool {
	return false
}
