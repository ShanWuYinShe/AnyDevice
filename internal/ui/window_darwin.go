//go:build darwin

package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	webview "github.com/webview/webview_go"
)

// OpenNativeWindow 打开独立桌面应用窗口并阻塞到窗口关闭（macOS 专属语义）：
//   - 优先 Chrome --app 独立 profile 模式，cmd.Run 阻塞到窗口关闭；
//   - 回退 webview_go（WKWebView），其 Run 必须占用主线程，无法后台化。
//
// 返回 nil 通道表示“阻塞式关窗语义”：调用方只需等待系统信号即可，
// 关闭窗口后本函数返回、应用自然退出，不存在 Windows 的启动器进程问题。
func OpenNativeWindow(title, url string, width, height int) <-chan struct{} {
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
				return nil
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
	return nil
}
