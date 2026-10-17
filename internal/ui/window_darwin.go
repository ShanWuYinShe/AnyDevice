//go:build darwin

package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	webview "github.com/webview/webview_go"
)

// OpenNativeWindow opens a dedicated native desktop window.
func OpenNativeWindow(title, url string, width, height int) {
	// First check if Chrome or Edge exists in App mode (for fast standalone window)
	browserPaths := []string{
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	}

	for _, p := range browserPaths {
		if _, err := os.Stat(p); err == nil {
			userDataDir := filepath.Join(os.TempDir(), "any_device_mac_ui")
			cmd := exec.Command(p,
				fmt.Sprintf("--app=%s", url),
				fmt.Sprintf("--window-size=%d,%d", width, height),
				fmt.Sprintf("--user-data-dir=%s", userDataDir),
				"--no-first-run",
			)
			if err := cmd.Run(); err == nil {
				return
			}
		}
	}

	// Fallback to native WebKit window via webview_go
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle(title)
	w.SetSize(width, height, webview.HintNone)
	w.Navigate(url)
	w.Run()
}
