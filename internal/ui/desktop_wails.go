//go:build wails

// Wails 桌面壳：用 Wails 原生窗口承载现有 UI 与 API。
// macOS 走 WKWebView、Windows 走 WebView2，发布形态不依赖本机 Chrome/Edge。
// 设计要点：
//   - 窗口内容通过 AssetsHandler 直接路由到局域网服务的 handler，
//     前端与全部 /api 端点零改动复用（局域网端口照常服务手机/平板）；
//   - Wails 窗口页面 origin 是 wails://wails（macOS）或
//     http://wails.localhost（Windows），并不是本项目真正的 HTTP 服务端，
//     WebSocket 无法经 AssetHandler 升级，因此前端显式直连
//     ws://127.0.0.1:<port>（端口取自 /api/self，见 state.svelte.js）；
//   - wails.Run 阻塞至窗口关闭，返回即表示用户已关窗，调用方据此退出服务，
//     不存在浏览器启动器进程提前返回的问题；
//   - 构建方式：go build -tags wails,desktop,production。
//     macOS 还需打包成 .app（scripts/build.sh 负责）：Info.plist 提供
//     ATS 本地网络例外与麦克风用途声明，裸二进制缺少这些声明。
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
		Title:  title,
		Width:  width,
		Height: height,
		// 与界面 --bg(#070b14) 一致，避免窗口首帧白闪
		BackgroundColour: options.NewRGB(7, 11, 20),
		// 宽高下限：再小则顶栏操作区与卡片会挤压换行
		MinWidth:      780,
		MinHeight:     520,
		AssetsHandler: handler,
	})
	if err != nil {
		util.Logf("[UI] Wails 窗口异常退出: %v\n", err)
	}
}
