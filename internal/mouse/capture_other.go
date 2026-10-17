//go:build !windows && !darwin

package mouse

func getScreenSize() (int, int) {
	return 1920, 1080
}

func getCursor() (int, int, bool, bool, bool) {
	return 960, 540, false, false, false
}
