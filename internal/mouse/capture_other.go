//go:build !windows && !darwin

package mouse

func getScreenSize() (int, int) {
	return 1920, 1080
}

func getCursor() (int, int, bool, bool, bool) {
	return 960, 540, false, false, false
}

func SimulateMove(dx, dy int) {}
func SimulateAbsolute(normX, normY float64) {}
func SimulateButton(btn string, down bool) {}
func SimulateScroll(dy int) {}
