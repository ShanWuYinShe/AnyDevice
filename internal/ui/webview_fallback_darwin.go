//go:build darwin

package ui

import (
	webview "github.com/webview/webview_go"
)

// runWebView 无 Chrome/Edge 时的兜底窗口（WKWebView，主线程阻塞运行）。
func runWebView(url string, width, height int) {
	w := webview.New(false)
	defer w.Destroy()
	w.SetSize(width, height, webview.HintNone)
	w.Navigate(url)
	w.Run()
}
