//go:build windows

package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// OpenNativeWindow 非阻塞打开独立桌面应用窗口。
// 返回的通道在“窗口进程退出”时关闭。
//
// 注意：命令返回 ≠ 用户关窗——新版 Chrome/Edge 存在启动器进程（派生真实
// 窗口后立即退出），也可能复用已有实例后返回。因此调用方不得把命令退出
// 当作关窗信号终止服务，而应结合页面连接状态判断（见 main.go）。
func OpenNativeWindow(title, url string, width, height int) <-chan struct{} {
	closed := make(chan struct{})

	go func() {
		defer close(closed)

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
			if err := cmd.Start(); err != nil {
				fmt.Printf("[UI] 浏览器窗口启动失败 (%v)，改用系统默认浏览器\n", err)
			} else {
				_ = cmd.Wait()
				return
			}
		}
		// 回退：系统默认浏览器（立即返回，页面连接状态由调用方观察）
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	}()

	return closed
}
