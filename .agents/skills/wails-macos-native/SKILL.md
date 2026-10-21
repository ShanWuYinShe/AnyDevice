---
name: wails-macos-native
description: 本项目 AnyDevice 的 macOS Wails 原生窗口（AnyDevice.app）构建/打包/排障事实：wails://wails 页面里的 WebSocket 地址、WKWebView 缺 WebCodecs AudioDecoder 时的服务端 PCM 回退、.app 与 Info.plist 要求、包内禁止写日志（会破坏代码签名）、多屏下按窗口 ID 截图验证。触发：改桌面窗口壳 / 打包 macOS 版本 / 窗口白屏或界面已连接但无声 / 窗口内 WS 连不上 / .app 签名或权限问题 / 多显示器验证窗口。
---

# AnyDevice macOS 原生窗口（Wails v2）事实与流程

实测环境：macOS 15.8（arm64）、Wails v2.16.0、Go 1.27.3、2026-10-21。

## 架构事实（改窗口/前端 WS 前必读）

- 桌面壳：`internal/ui/desktop_wails.go`（build tag `wails`，macOS+Windows 共用）；
  无标签 `go build` 走 `desktop_browser.go` 的浏览器 --app 回退，仅调试用。
- Wails 窗口页面 origin：macOS = `wails://wails/`、Windows = `http://wails.localhost/`；
  **都不是本服务的 HTTP 端口**。相对 `fetch('/api/...')` 经 AssetHandler 直达服务 handler（可用、无 CORS），
  但 WebSocket 不能经 AssetHandler 升级（Wails 对 Upgrade 返回 501），必须由前端显式连
  `ws://127.0.0.1:<port>`，端口取自 `/api/self` 的 `port`（`frontend/src/lib/state.svelte.js` 的
  `isDesktopShell()/resolveWsBase()`）。
- 历史根因（2026-10-18 曾误判为"裸二进制窗口不稳定"）：`wails://wails/` 的
  `location.host === "wails"` 非空，旧代码拼出 `ws://wails/ws` 必然 1006 失败；
  页面本身能正常加载。判断桌面壳要按 `location.protocol`（非 http/https）或
  `location.hostname === "wails.localhost"`。

## macOS 音频无声的根因（WKWebView 无 AudioDecoder）

- WKWebView 实测：`typeof AudioDecoder === "undefined"`，`VideoDecoder` 存在，
  `isSecureContext === true`、AudioWorklet 可用。Safari 浏览器同样没有 AudioDecoder。
- 旧协议只发 Opus（AUPS/AUPM）时，这类客户端"有码率但无声"。
- 本项目的修复：前端 `set_mapping` 带 `pcm: typeof AudioDecoder === "undefined"`；
  服务端 `hub.go` 按客户端能力路由，`peer_manager.go` 在转发对端音频时用
  `internal/audio/opus_decoder.go`（cgo libopus）解码成 float32 PCM（AUDS/AUDM）
  只发给声明 pcm 的客户端；有 AudioDecoder 的客户端（Chrome/WebView2）仍收 Opus。
- 诊断：`GET /api/status` 的 `audio_pcm_clients` 显示当前走 PCM 的客户端数。

## 构建与打包

- 一律用 `./scripts/build.sh`：macOS 产出 `bin/AnyDevice.app`
  （`Contents/MacOS/any_device` + 由根目录 `Info.plist` 注入版本号的 plist），
  再 `codesign -s - -f bin/AnyDevice.app`（ad-hoc）；Windows 产出 Wails exe + portable。
- 编译标签必须 `wails,desktop,production`（缺 `production` 会在运行时直接报错），
  darwin 外部链接需要 `-extldflags '-framework UniformTypeIdentifiers'`（v2.16 用 UTType）。
- `Info.plist` 必须有：`CFBundleExecutable/Identifier/Name/Version/ShortVersion`、
  `NSHighResolutionCapable`、`NSAppTransportSecurity{NSAllowsLocalNetworking}`、
  `NSMicrophoneUsageDescription`（缺了 TCC 不授权甚至终止进程）、`NSLocalNetworkUsageDescription`。
- macOS 音频仍动态链接 Homebrew `/opt/homebrew/opt/opus/lib/libopus.0.dylib`：
  换机器运行 .app 前需 `brew install opus`（未改静态链接）。
- ad-hoc 签名身份随每次构建变化：重新构建后「辅助功能」授权可能失效，需要重新勾选 `AnyDevice.app`。

## 排障事实

- 日志路径：`.app` 形态写用户配置目录 `~/Library/Application Support/AnyDevice/any_device.log`
  （包内禁止写：签名后新增文件会让 `codesign --verify` 报 sealed resource invalid）；
  Windows/裸二进制写 exe 同目录。界面与 `/api/self` 的 `log_path` 可查实际路径。
- 窗口无控制台/DevTools：前端把 WS 连接状态 POST 到 `/api/client-report`，日志里看
  `[前端自检] ws_open ... (ws=ws://127.0.0.1:<port>/ws)` 即证明页面加载+WS 连通。
- 关窗退出：Wails 的 windowShouldClose → `wails.Run` 返回 → main 停引擎退出
  （`HideWindowOnClose` 默认 false；源码证据 darwin/WindowDelegate.m → Application.m）。
  自动化验证时 System Events 需要辅助功能权限，通常只能用 `open -n` 启动 + `kill` 收尾。

## 多显示器验证（本机 2 屏，左外接 2560×1440 + 内置 1440×900）

- 不要用全屏 `screencapture` 判断窗口是否出现（只会截主屏，容易漏掉另一块屏上的窗口）。
- 正确做法：用 CoreGraphics 列窗口拿 `kCGWindowNumber`，再按窗口 ID 截图：
  `screencapture -x -o -l <windowID> out.png`（多屏/任意屏都能精确截到窗口）。
  Swift 片段可用 `CGWindowListCopyWindowInfo(.optionAll)` + `screencapture -l`。
- 窗口默认由 macOS 放在系统主显示器（有菜单栏那块），实测与外接屏布局一致；
  局域网服务/二维码地址与窗口所在屏幕无关。
- 已知限制（未修复）：鼠标映射的坐标归一化用 `CGDisplayBounds(CGMainDisplayID())`，
  多屏时副屏坐标会越界被钳制到 0~1；需要支持副屏映射时要改成全虚拟桌面坐标。
