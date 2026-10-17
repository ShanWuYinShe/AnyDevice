//go:build !windows && !darwin

package ui

import (
	"fmt"
	"os/exec"
)

// OpenNativeWindow 非阻塞打开桌面窗口（Linux 走 Chrome/Chromium --app，
// 回退系统默认浏览器），窗口进程退出时关闭返回的通道。
// 命令返回 ≠ 用户关窗（启动器进程/实例复用会提前返回），语义同 Windows 版。
func OpenNativeWindow(title, url string, width, height int) <-chan struct{} {
	closed := make(chan struct{})

	go func() {
		defer close(closed)

		browsers := []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge"}
		for _, b := range browsers {
			if path, err := exec.LookPath(b); err == nil {
				userDataDir := fmt.Sprintf("%s/any_device_linux_ui", "/tmp")
				cmd := exec.Command(path,
					fmt.Sprintf("--app=%s", url),
					fmt.Sprintf("--window-size=%d,%d", width, height),
					fmt.Sprintf("--user-data-dir=%s", userDataDir),
				)
				if err := cmd.Start(); err != nil {
					continue
				}
				_ = cmd.Wait()
				return
			}
		}
		_ = exec.Command("xdg-open", url).Start()
	}()

	return closed
}
