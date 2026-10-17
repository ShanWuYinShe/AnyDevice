//go:build windows

package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// OpenNativeWindow opens a dedicated native desktop application window without browser chrome/address bar.
func OpenNativeWindow(title, url string, width, height int) {
	edgePaths := []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome", "Application", "chrome.exe"),
	}

	var browserPath string
	for _, p := range edgePaths {
		if p != "" {
			if _, err := os.Stat(p); err == nil {
				browserPath = p
				break
			}
		}
	}

	userDataDir := filepath.Join(os.TempDir(), "any_device_edge_ui")
	_ = os.MkdirAll(userDataDir, 0755)

	if browserPath != "" {
		cmd := exec.Command(browserPath,
			fmt.Sprintf("--app=%s", url),
			fmt.Sprintf("--window-size=%d,%d", width, height),
			fmt.Sprintf("--user-data-dir=%s", userDataDir),
			"--no-first-run",
			"--no-default-browser-check",
		)
		// Run blocks until the user closes the native application window
		_ = cmd.Run()
	} else {
		// Fallback to default browser if neither Edge nor Chrome is found
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Run()
	}
}
