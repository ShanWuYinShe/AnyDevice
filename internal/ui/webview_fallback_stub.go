//go:build !darwin

package ui

// runWebView 非 darwin 平台无 webview 回退（浏览器探测已覆盖）。
func runWebView(url string, width, height int) {
	_ = url
	_ = width
	_ = height
}
