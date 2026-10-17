//go:build !windows && !darwin

package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// OpenNativeWindow opens a standalone window on Linux (Chrome/Chromium/Edge --app mode).
func OpenNativeWindow(title, url string, width, height int) {
	browsers := []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge"}
	for _, b := range browsers {
		if path, err := exec.LookPath(b); err == nil {
			userDataDir := filepath.Join(os.TempDir(), "any_device_linux_ui")
			cmd := exec.Command(path,
				fmt.Sprintf("--app=%s", url),
				fmt.Sprintf("--window-size=%d,%d", width, height),
				fmt.Sprintf("--user-data-dir=%s", userDataDir),
			)
			if err := cmd.Run(); err == nil {
				return
			}
		}
	}
	_ = exec.Command("xdg-open", url).Run()
}
