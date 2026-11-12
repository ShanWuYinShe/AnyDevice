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

- 以上构建命令只用于调试/出包（重活交 CI），不是提交前门禁；日常提交只跑下节「CI 门禁与本地验证分工」的秒级检查。
- **发布产物必须是 `.app` 包**：裸二进制没有 `Info.plist`，ATS 本地网络例外与麦克风用途声明都不生效，
  WKWebView 里的 WebSocket/麦克风会直接失败。
- **无标签 `go build` 会回退成浏览器 `--app` 独立窗口模式**（`desktop_browser.go`；macOS 无 Chrome/Edge 时回退 `webview_go`/WKWebView），只适合调试，别当发布态验证。
- macOS 架构由 `MAC_ARCH=arm64|amd64|universal` 选择；版本号取自 `git describe --tags`（tag 即版本，不用改文件）。
- 三个脚本：`build.sh`（macOS .app + Windows 交叉编译）、`build-appimage.sh`（Linux AppImage）、
  `fetch-libopus-amd64.sh`（从 Homebrew bottle 重建 macOS amd64 静态库，CI 校验可复现性）。

## CI 门禁与本地验证分工

重活（全量测试、Wails 生产构建、前端打包、三平台构建/AppImage）一律交给 GitHub Actions，
本地只跑秒级检查。完整流程见全局 skill `github-ci-flow`：worktree 内提交 → 推送分支 →
`gh pr create` → `gh pr checks --watch` 全绿 → `gh pr merge --rebase --delete-branch` →
本地 `git pull --ff-only` 并清理 worktree。

- 本地（提交前，秒级）：

  ```bash
  gofmt -l $(git ls-files '*.go')   # 应无输出（约 0.1s）
  go vet ./...                      # 约 5s
  bun run --cwd frontend check      # svelte-check，约 1.4s
  ```

- CI 覆盖（`.github/workflows/ci.yml`，5 个并行 job）：
  `macos`（gofmt / go vet / go test / 前端 svelte-check + build / Wails 生产构建 /
  vendored libopus 可复现性校验 / 三架构 .app 打包上传）、
  `windows-cross`（Linux 上交叉编译 Windows 平台层 mouse/keyboard）、
  `linux-build`（GTK3+WebKitGTK 构建 / headless 服务冒烟 / AppImage 构建与验证）、
  `linux-build-arm64`（原生 arm64 构建 + headless 冒烟）、
  `windows-runtime`（真实 Windows CGO 构建 / 全量测试 / 服务启停冒烟 / Windows 双 exe 打包）。
- CI 覆盖不到、必须本地/真机验证的：真实音频链路听感（回环设备/麦克风/系统声、LL 钩子捕获真实交互事件）、
  macOS 辅助功能与麦克风权限弹窗、真机跨端联调（Mac↔Windows↔Linux 实际键鼠与音频）、
  ad-hoc 签名后在 Gatekeeper 与系统设置里的实际体验；CI 验证的是进程内调用路径不崩溃、可反复启停。
- 发版/发布门禁不变：`release.yml` 由用户推 `v*` tag 触发（三平台构建 + GitHub Release），
  手动演练用 `gh workflow run release.yml`；本地不跑发布构建。

## skill 索引（改对应模块前先加载对应 skill）

| skill | 管什么 |
|---|---|
| `wails-macos-native` | AnyDevice.app 的 macOS Wails 原生窗口构建/打包/排障：`wails://wails` 页面里的 WebSocket 地址、WKWebView 缺 AudioDecoder（WebCodecs）时的服务端 PCM 回退等实测事实 |
| `anydevice-audio-streaming` | 音频采集/传输/播放架构与验证：按订阅启停、系统声/麦克风分离、float 全链、AudioWorklet 自适应环形缓冲、发送端编码档位自适应、延迟分项测量、平台解码差异 |

> 新增 skill 时同步本表——漏索引时系统仍会挂载，但 AI 很难主动想到加载它。

## 性能约束（与游戏等高负载程序共存）

- **采集与系统钩子必须按订阅数按需启停，空闲零空转**：不得在启动时无条件打开音频设备或安装系统级输入钩子；任何 WS 连接本身不得启动采集（旧对端由 500ms 宽限兜底）。
- **系统声与麦克风分开订阅**（`set_mapping` 的 `sys_audio`/`mic_audio`）；「本机共享」关闭后即使对端仍订阅也必须立即停止对应采集、卸载钩子。
- **Opus 编码参数按负载自适应**（默认平衡档 128kbps / 复杂度 5；编码队列积压、丢帧或编码延迟偏高时降 96kbps / 复杂度 3，持续低负载 15s 后升 160kbps / 复杂度 7）：决策必须带迟滞（冷却 8s、采样 2s），在 encodeLoop 内驱动、不得新增常驻 goroutine，`SetBitrate`/`SetComplexity` 与 `Feed` 同锁串行；Opus 帧长保持 20ms、采集设备周期不随负载改动。采集线程保持 miniaudio 默认优先级。
- **播放端预缓冲必须自适应**（40–200ms，初始 60ms；欠载快升 +30ms、稳定 5s 慢降 -5ms），不要退回固定大缓冲；Worklet `stats` 消息须带 `targetMs` 供 UI/诊断读取。
- 延迟分项字段契约（`audio_stats` / `/api/peers`）改动须两端同改并保留旧版兼容；发送端编码参数 `bitrate`/`complexity` 用 omitempty、`level` 不带 omitempty（前端以 `bitrate` 是否存在判断旧版对端）。
- 新增常驻线程/定时器/系统钩子前，先评估对游戏帧时间的影响；`/api/status` 暴露各引擎运行状态供验证。
- 完整架构、实现位置与踩坑机理见 `anydevice-audio-streaming` skill。

## 维护提醒

本文件与 skill 里的路径/命令断言，改动构建脚本或升级 Wails 后必须复核；
跑 `~/.agents/bin/skill-lint ~/work/any_device/.agents/skills` 可机器检查失效引用。
