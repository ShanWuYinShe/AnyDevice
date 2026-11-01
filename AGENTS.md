# any_device

macOS Wails 桌面应用（Go + 前端）。本文件只做项目特有补充，通用约束以全局 `~/AGENTS.md` 为准。

## 常用命令

```bash
bun run --cwd frontend build   # 前端产物到 internal/web/dist（随后 go build 嵌入）
./scripts/build.sh             # macOS：Wails 原生窗口 → bin/AnyDevice.app（含 Info.plist 打包）
go build -tags wails,desktop,production -o any_device main.go   # 免打包的裸二进制（仅调试）
```

- **发布产物必须是 `.app` 包**：裸二进制没有 `Info.plist`，ATS 本地网络例外与麦克风用途声明都不生效，
  WKWebView 里的 WebSocket/麦克风会直接失败。
- **无标签 `go build` 会回退成浏览器 `--app` 独立窗口模式**（`desktop_browser.go`），只适合调试，别当发布态验证。
- macOS 架构由 `MAC_ARCH=arm64|amd64|universal` 选择；版本号取自 `git describe --tags`（tag 即版本，不用改文件）。
- 三个脚本：`build.sh`、`build-appimage.sh`、`fetch-libopus-*.sh`（libopus 静态库拉取）。

## skill 索引（改对应模块前先加载对应 skill）

| skill | 管什么 |
|---|---|
| `wails-macos-native` | AnyDevice.app 的 macOS Wails 原生窗口构建/打包/排障：`wails://wails` 页面里的 WebSocket 地址、WKWebView 缺失 WebCrypto 等实测事实 |

> 新增 skill 时同步本表——漏索引时系统仍会挂载，但 AI 很难主动想到加载它。

## 维护提醒

本文件与 skill 里的路径/命令断言，改动构建脚本或升级 Wails 后必须复核；
跑 `~/.agents/bin/skill-lint ~/work/any_device/.agents/skills` 可机器检查失效引用。
