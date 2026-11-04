# any_device

macOS Wails 桌面应用（Go + 前端）。本文件只做项目特有补充，通用约束以全局 `~/AGENTS.md` 为准。

## 常用命令

```bash
bun run --cwd frontend build   # 前端产物到 internal/web/dist（随后 go build 嵌入）
./scripts/build.sh             # macOS：Wails 原生窗口 → bin/AnyDevice.app（含 Info.plist 打包）
go build -tags wails,desktop,production \
    -ldflags "-s -w -extldflags '-framework UniformTypeIdentifiers'" \
    -o any_device main.go   # 免打包的裸二进制（仅调试；macOS 缺 extldflags 会链接失败）
```

- **发布产物必须是 `.app` 包**：裸二进制没有 `Info.plist`，ATS 本地网络例外与麦克风用途声明都不生效，
  WKWebView 里的 WebSocket/麦克风会直接失败。
- **无标签 `go build` 会回退成浏览器 `--app` 独立窗口模式**（`desktop_browser.go`；macOS 无 Chrome/Edge 时回退 `webview_go`/WKWebView），只适合调试，别当发布态验证。
- macOS 架构由 `MAC_ARCH=arm64|amd64|universal` 选择；版本号取自 `git describe --tags`（tag 即版本，不用改文件）。
- 三个脚本：`build.sh`（macOS .app + Windows 交叉编译）、`build-appimage.sh`（Linux AppImage）、
  `fetch-libopus-amd64.sh`（从 Homebrew bottle 重建 macOS amd64 静态库，CI 校验可复现性）。

## skill 索引（改对应模块前先加载对应 skill）

| skill | 管什么 |
|---|---|
| `wails-macos-native` | AnyDevice.app 的 macOS Wails 原生窗口构建/打包/排障：`wails://wails` 页面里的 WebSocket 地址、WKWebView 缺 AudioDecoder（WebCodecs）时的服务端 PCM 回退等实测事实 |
| `anydevice-audio-streaming` | 音频采集/传输/播放架构与验证：按订阅启停、系统声/麦克风分离、float 全链、AudioWorklet 环形缓冲、延迟分项测量、平台解码差异 |

> 新增 skill 时同步本表——漏索引时系统仍会挂载，但 AI 很难主动想到加载它。

## 性能约束（与游戏等高负载程序共存）

- **采集与系统钩子必须按订阅数按需启停，空闲零空转**：不得在启动时无条件打开音频设备或安装系统级输入钩子；任何 WS 连接本身不得启动采集（旧对端由 500ms 宽限兜底）。
- **系统声与麦克风分开订阅**（`set_mapping` 的 `sys_audio`/`mic_audio`）；「本机共享」关闭后即使对端仍订阅也必须立即停止对应采集、卸载钩子。
- Opus 编码复杂度保持 5；采集线程保持 miniaudio 默认优先级。延迟分项字段契约（`audio_stats` / `/api/peers`）改动须两端同改并保留旧版兼容。
- 新增常驻线程/定时器/系统钩子前，先评估对游戏帧时间的影响；`/api/status` 暴露各引擎运行状态供验证。
- 完整架构、实现位置与踩坑机理见 `anydevice-audio-streaming` skill。

## 维护提醒

本文件与 skill 里的路径/命令断言，改动构建脚本或升级 Wails 后必须复核；
跑 `~/.agents/bin/skill-lint ~/work/any_device/.agents/skills` 可机器检查失效引用。
