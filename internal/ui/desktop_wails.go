//go:build wails

// Wails 桌面壳：用 Wails 原生窗口承载现有 UI 与 API。
// 设计要点：
//   - 窗口内容通过 AssetsHandler 直接路由到局域网服务的 handler，
//     前端与全部 /api、/ws 端点零改动复用（局域网端口照常服务手机端）；
//   - wails.Run 阻塞至窗口关闭，返回即表示用户已关窗，调用方据此退出服务，
//     不存在浏览器启动器进程提前返回的问题；
//   - 构建方式：go build -tags wails。

package ui

import (
	"net/http"

	wails "github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"

	"any_device/internal/util"
)

// RunDesktop 打开原生桌面窗口并阻塞至窗口关闭。
// handler 是局域网服务的根 handler，Wails 窗口内的页面请求经由它处理。
func RunDesktop(handler http.Handler, localURL, title string, width, height int) {
	err := wails.Run(&options.App{
		Title:         title,
		Width:         width,
		Height:        height,
		AssetsHandler: handler,
	})
	if err != nil {
		util.Logf("[UI] Wails 窗口异常退出: %v\n", err)
	}
}
