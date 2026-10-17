//go:build !darwin && !windows

package keyboard

func getPressedKeys() map[string]int {
	return make(map[string]int)
}

// SimulateKey is a stub for unsupported operating systems.
func SimulateKey(keyName string, down bool) {
	// No-op fallback
}
