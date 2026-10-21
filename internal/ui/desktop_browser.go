//go:build !wails

// 浏览器壳回退版：无 Wails 依赖的构建（默认 go build）使用，仅开发调试用；
// 发布产物（含 macOS .app）一律走 Wails 原生窗口。
// 打开 Chrome/Edge/Chromium --app 独立窗口（带独立 profile），
// 并处理“窗口命令提前返回 ≠ 用户关窗”的浏览器启动器行为：
// 命令返回后观察 5 秒，页面连接仍在线则服务驻留（等 Ctrl+C），
// 连接已断则按关窗即退退出。
package ui

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// RunDesktop 打开浏览器 --app 窗口并阻塞至应用退出
// （窗口真正关闭，或命令提前返回且页面连接断开）。
func RunDesktop(handler http.Handler, localURL, title string, width, height int) {
	_ = handler
	openAppWindow(localURL, title, width, height)

	sigChan := signalChan()

	// 窗口进程退出 ≠ 用户关窗：新版 Chrome/Edge 的启动器进程会把真实窗口
	// 派生出去后立即退出。命令返回后观察页面连接状态决定去留。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if clientCount() > 0 {
			fmt.Println("💡 窗口进程已退出，但页面连接仍在线，服务保持运行（按 Ctrl+C 退出）")
			waitSignal(sigChan)
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	fmt.Println("\n窗口已关闭，正在停止服务...")
}

func openAppWindow(url, title string, width, height int) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		edgePaths := []string{
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome", "Application", "chrome.exe"),
		}
		for _, p := range edgePaths {
			if p != "" {
				if _, err := os.Stat(p); err == nil {
					userDataDir := filepath.Join(os.TempDir(), "any_device_edge_ui")
					_ = os.MkdirAll(userDataDir, 0755)
					cmd = exec.Command(p,
						fmt.Sprintf("--app=%s", url),
						fmt.Sprintf("--window-size=%d,%d", width, height),
						fmt.Sprintf("--user-data-dir=%s", userDataDir),
						"--no-first-run",
						"--no-default-browser-check",
					)
					break
				}
			}
		}
		if cmd == nil {
			// 回退：系统默认浏览器
			_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
			return
		}
	case "darwin":
		browserPaths := []string{
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		}
		for _, p := range browserPaths {
			if _, err := os.Stat(p); err == nil {
				userDataDir := filepath.Join(os.TempDir(), "any_device_mac_ui")
				cmd = exec.Command(p,
					fmt.Sprintf("--app=%s", url),
					fmt.Sprintf("--window-size=%d,%d", width, height),
					fmt.Sprintf("--user-data-dir=%s", userDataDir),
					"--no-first-run",
				)
				break
			}
		}
		if cmd == nil {
			// 无 Chrome/Edge 时回退 webview_go（WKWebView，需主线程阻塞运行）
			runWebView(url, width, height)
			return
		}
	default: // linux
		for _, b := range []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge"} {
			if path, err := exec.LookPath(b); err == nil {
				userDataDir := filepath.Join(os.TempDir(), "any_device_linux_ui")
				cmd = exec.Command(path,
					fmt.Sprintf("--app=%s", url),
					fmt.Sprintf("--window-size=%d,%d", width, height),
					fmt.Sprintf("--user-data-dir=%s", userDataDir),
				)
				break
			}
		}
		if cmd == nil {
			_ = exec.Command("xdg-open", url).Start()
			return
		}
	}

	if err := cmd.Start(); err != nil {
		fmt.Printf("[UI] 浏览器窗口启动失败 (%v)，可用局域网地址手动访问\n", err)
		return
	}
	// 不等待窗口进程：其退出由 RunDesktop 的连接观察逻辑处理
}
