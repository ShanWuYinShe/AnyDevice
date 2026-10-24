# AnyDevice - 跨端双向设备映射中心 🎵🖱️🎮

[![CI](https://github.com/ShanWuYinShe/AnyDevice/actions/workflows/ci.yml/badge.svg)](https://github.com/ShanWuYinShe/AnyDevice/actions/workflows/ci.yml)

> 将台式机/电脑的声音与鼠标实时无线映射到手机、平板、另一台电脑或电视上。**自带原生独立桌面应用窗口（双击直接弹窗，无需浏览器）：既能作为提供方把本机系统声音、麦克风、鼠标与键盘共享给对端，也能作为使用方把对端能力逐项映射到本机播放或执行**。智能路由过滤虚拟机网卡，支持在 UI 上切换通信网卡与设备映射。

---

## 🌟 核心特性

- **自带原生独立桌面应用窗口（Wails 原生窗口，无需浏览器）**：
  - 桌面窗口采用 **Wails** 原生窗口（macOS WKWebView / Windows WebView2 / Linux WebKitGTK）：macOS 双击 `AnyDevice.app`、Windows 双击 `any_device.exe`、Linux 运行 `any_device_linux_amd64`，直接弹出**专属独立应用窗口**——无浏览器地址栏、无控制台黑窗、无浏览器启动器竞态，关窗即安全退出。
  - macOS 的 `.app` 由 `scripts/build.sh` 打包（`Info.plist` 带 ATS 本地网络例外与麦克风用途声明），**不依赖本机安装 Chrome/Edge**；窗口内的 WebSocket 直连 `ws://127.0.0.1:<port>`，页面与 `/api` 请求仍走内置 handler。
  - 窗口与局域网服务共用同一套 UI 与 API：窗口内的页面请求直达内置服务，局域网端口照常服务手机/平板/其他电脑。
  - **服务先于窗口就绪**：先完成端口监听再打开窗口，首屏不会因服务未就绪而白屏。
  - 也可以通过 `-no-ui` 运行于纯后台守护进程模式。
  - 仅开发调试用的无标签 `go build` 会回退为浏览器 `--app` 独立窗口模式（Chrome/Edge 独立 profile）；发布产物一律是原生窗口。
- **对等的发现与映射模型（提供方 + 使用方）**：
  - 📥 **接收侧（把对端能力映射到本机）**：
    - 🎧 **系统声音**：把对端电脑的声音串流到本机/手机播放；
    - 🎙️ **麦克风**：把对端麦克风拾音映射到本机收听；
    - 🖱️ **鼠标 / ⌨️ 键盘遥测**：把对端光标坐标、按键事件实时投射到接收端卡片显示。
  - 📤 **共享侧（把本机能力开放给对端）**：
    - 在「本机共享」设置里逐项开放系统声音、麦克风、物理鼠标与物理键盘，对端发现本机后自行决定是否映射；
    - 电脑对电脑时双方各自打开本程序、互相映射对方设备，即可实现**完整物理鼠标与键盘同步**：移动、左右/中键、滚轮、按键在对端实时还原；
    - **滚轮同步**：捕获端滚动滚轮，对端即时还原滚动方向与幅度。
    - **自动重连**：映射保持期间，对端连接失败或中断会按指数退避（1s 起步、30s 上限）自动重连，卡片显示「连接中断，自动重连中」；关闭映射即停止重连。
  - ℹ️ 说明：手机端当前为**纯接收端**（无需安装 App，扫码即用）；曾有的「手机当无线触控板反向遥控」界面已随交互重构移除，其遗留接口（`/api/peers/control`、`/api/mapping/mode`、`/api/inbound*`、`/api/outbound*`）与整套出站设备管理子系统已一并清理。
- **智能网卡与 IP 路由解析**：
  - 采用操作系统路由探测（OS Routing Table Resolution）自动定位真实物理出网网卡，**自动剔除 VMware / VirtualBox / WSL / Hyper-V / Docker 等虚拟网卡干扰**。
  - 支持 `-ip` 命令行参数随时手动指定监听发布的局域网 IP。
- **秒级设备发现与稳定在线状态**：
  - 节点启动时立即突发广播（0/400/800ms 三连发，通常 1 秒内全网可感知，不再等满一个心跳周期），并向每个物理网段发送定向广播，多网卡/虚拟机环境也能可靠互发现；
  - 手机/浏览器接入按来源归并（刷新页面不产生重复条目），断开后快速显示离线：浏览器/手机接入约 5 秒内、自动发现的对端约 7–9 秒内；
  - 手机或另一台电脑打开控制台时，设备列表**不会显示它自己**（按查看者过滤）。
- **设备改名跨重启持久化**：
  - 本机名称与对端备注保存在本地配置文件（默认用户配置目录下 `AnyDevice/config.json`，可用环境变量 `ANY_DEVICE_CONFIG` 指定其他路径），重启不再丢失；
  - 对端备注优先于对端广播名显示，且**不会**被后续发现心跳覆盖。
- **按客户端解码能力自适应（Opus ↔ float32 PCM）**：
  - 对端音频默认传 Opus（128kbps，带宽约 float32 PCM 的 1/24）；
  - 本机 UI 客户端若没有 WebCodecs `AudioDecoder`（macOS Wails 窗口的 WKWebView、Safari），服务端在转发对端音频时自动解码成 float32 PCM 再下发，**原生窗口内播放不会因浏览器内核缺解码器而无声**；
  - 有解码能力的客户端（Chrome/Edge/WebView2）仍收 Opus，局域网带宽不受影响（`/api/status` 的 `audio_pcm_clients` 可查看当前声明走 PCM 的客户端数）；
  - 设备卡片上的「接收码率」是**对端推流到本机的真实网络字节率**（服务端按对端逐条统计，`/api/peers` 的 `mapping.rx_*_kbps`，不同对端互不混淆）：Opus 约 16 KB/s，旧版对端 PCM 约 350–380 KB/s；本机把 Opus 解码成 PCM 播放的部分不计入该数字，所以原生窗口与浏览器看到的是同一个网络码率。
- **按需订阅，空闲零空转**：
  - 本机 UI 窗口默认不订阅音频/鼠标/键盘流，只有真正开启映射时才通过 `set_mapping` 动态订阅；无人订阅时音频捕获引擎静默，显著降低空闲 CPU 与网络开销。
- **纯系统原生驱动（事件驱动捕获）**：
  - 音频：Windows WASAPI Loopback / macOS CoreAudio / Linux PipeWire。
  - 鼠标/键盘捕获参照 Deskflow 的事件驱动模型：Windows 低级钩子（`WH_MOUSE_LL`/`WH_KEYBOARD_LL`）、macOS `CGEventTap`（只听模式），实时捕获移动、按键、滚轮，未授权时自动回退轮询模式；
  - 注入采用现代 API：Windows `SendInput`、macOS `CGEventPost`（字符键附带 Unicode 直通、修饰键状态实时跟踪）；注入事件自带标记并在捕获钩子中过滤，杜绝捕获/注入回环。
  - **输入鲁棒性与防卡键保障**：
    - **小键盘支持**：覆盖数字小键盘（Numpad 0-9、加减乘除、小数点、小键盘回车与 NumLock），Windows 区分小键盘回车与主回车并带扩展键标志注入；
    - **按键释放精准识别**：鼠标事件携带具体 `button` 标识，彻底解决右键/中键抬起因状态复位被误当左键抬起处理导致按键永久卡死的问题；
    - **拖拽与系统钩子自愈**：macOS 捕获完整覆盖鼠标拖拽事件（`LeftMouseDragged` 等）并在模拟端还原拖拽状态；钩子因系统超时/交互暂停时自动重新激活（`kCGEventTapDisabledByTimeout` 自愈）；
    - **断开与切换防卡键保护**：对端连接断开、本地映射切换关闭或反向控制断开时，自动释放所有被跟踪按下的物理按键、修饰键与鼠标按键，杜绝卡键残留。
- **全平台零安装接收端**：手机/平板端扫码即用，无需安装额外 App。

---

## 🔧 排查诊断

| 环境变量 | 作用 |
| :--- | :--- |
| `ANY_DEVICE_AUDIO_DEBUG=1` | 音频链路诊断：捕获端每 2 秒输出 chunk/字节速率与静音标记；转发端输出帧到达统计 |
| `ANY_DEVICE_KB_DEBUG=1` | 键鼠链路诊断：捕获端发出的事件、执行端收到的事件与模拟执行 |

**日志位置**：原生窗口没有控制台，日志同时写入文件——macOS `.app` 形态写入用户配置目录 `~/Library/Application Support/AnyDevice/any_device.log`（包内文件受代码签名保护，禁止写入）；Windows / 裸二进制写入可执行文件同目录的 `any_device.log`。界面左下角会显示日志实际路径。

**音频映射无声时的自查**：
1. `ANY_DEVICE_AUDIO_DEBUG=1` 启动捕获端与播放端——若捕获端显示「本段全零静音」或速率极低，说明捕获设备上确实没有声音在播放（WASAPI Loopback 只捕获默认输出设备上正在播放的声音）；
2. 检查捕获端设备卡片显示的系统声/麦克风设备名，确认与你实际听声的设备一致；
3. macOS 端：音频采集需要「麦克风」授权（首次运行弹窗），键鼠跨端映射需要「系统设置 → 隐私与安全性 → 辅助功能」勾选 `AnyDevice.app`（未授权时启动有警告提示）；
4. 浏览器场景可在开发者控制台执行 `window.__audioDebug()` 查看前端收到的音频字节率与 AudioContext 状态。

---

## 🚀 极简使用说明

### 1. 启动程序（电脑端）

直接双击运行：
- **macOS**：双击 `bin/AnyDevice.app`（也可先拖到「应用程序」再启动）。Apple Silicon 用 arm64 包、Intel Mac 用 amd64 包，或直接用 universal 包（一个包通吃，体积约两倍）
- **Windows 台式机**：双击运行 `any_device.exe`
- **Linux**：运行 `./any_device_linux_amd64`（Wails 原生窗口，GTK3 + WebKitGTK）。需系统安装运行库：
  `sudo apt install libgtk-3-0 libwebkit2gtk-4.0-37`（Debian/Ubuntu）、`sudo dnf install gtk3 webkit2gtk4.1`（Fedora）。
  无图形环境或缺少 GTK/WebKitGTK 时，加 `-no-ui` 以纯服务模式运行，用手机/浏览器扫码接入。

程序启动后会自动弹出**独立桌面应用窗口**：顶部为「扫码接入 / 添加设备 / 本机共享」三个入口，下方概览区显示本机名称、通信网卡与实时通道状态，点「扫码接入」即可显示连接二维码与本机局域网地址。

> 多显示器：窗口由 macOS 按系统默认策略放置，局域网服务与二维码地址不受窗口位于哪块屏幕影响；手机扫码访问的是本机局域网 IP。

### 2. 手机/另一端连接

拿起手机对着电脑屏幕上的二维码一扫，即可连接。手机上打开页面后，「对端设备」卡片里逐项决定把电脑的哪种能力映射到手机：
- **系统声音** → 手机当音箱（电脑扬声器原声）；
- **麦克风** → 手机收听电脑麦克风；
- **物理鼠标 / 物理键盘** → 手机卡片实时显示对端光标坐标与按键遥测。

电脑对电脑时，两台都打开本程序，各自在对方的设备卡片上选择要映射的能力，即可双向同步声音、鼠标与键盘。

---

## 🛠️ 常用命令行参数

| 参数 | 默认值 | 说明 |
| :--- | :--- | :--- |
| `-no-ui` | `false` | 纯后台/命令行模式运行，不弹桌面应用窗口 |
| `-port` | `8080` | 指定服务端口 |
| `-ip` | `""` | 手动指定对外广播的局域网 IP（多网卡/复杂网络环境时使用） |
| `-mode` | `auto` | 音频捕获模式：`auto` (自动)、`loopback` (系统回环)、`capture` (输入端/麦克风)、`test-tone` (测试音) |
| `-list` | `false` | 列出本机所有可用音频设备并退出 |
| `-list-nics` | `false` | 列出本机所有网卡与 IP 地址并退出 |
| `-device` | `-1` | 手动指定特定设备序号（通过 `-list` 查看） |
| `-no-qr` | `false` | 关闭终端二维码打印 |

---

## 💻 编译与构建

UI 使用 **Svelte 5 + Vite**（源码在 `frontend/`），构建产物输出到 `internal/web/dist/` 并由 `go:embed` 打进二进制，无需任何运行时前端服务。

```bash
# 一键构建（前端 + macOS 原生窗口 .app + Windows 交叉编译）
./scripts/build.sh

# macOS 架构选择（默认 arm64）：
MAC_ARCH=amd64 ./scripts/build.sh      # Intel Mac
MAC_ARCH=universal ./scripts/build.sh  # 双架构通吃（体积约两倍）

# 只改了 Go 代码、前端未动时：
#   macOS 原生窗口 -> 重新打包 .app（Info.plist 必须随包，裸二进制窗口内
#   WebSocket/麦克风会因缺少 ATS/用途声明而失败）：
go build -tags wails,desktop,production \
    -ldflags "-s -w -extldflags '-framework UniformTypeIdentifiers'" \
    -o bin/AnyDevice.app/Contents/MacOS/any_device main.go
codesign -s - -f bin/AnyDevice.app

# 无标签构建只用于快速调试（浏览器 --app 窗口回退，不是发布形态）：
go build -o any_device main.go

# Linux 原生窗口（需 libgtk-3-dev libwebkit2gtk-4.0-dev libopus-dev）：
go build -tags wails,desktop,production -o any_device_linux_amd64 main.go
```

> macOS 原生窗口必须运行在 `.app` 包内（`AnyDevice.app/Contents/MacOS/any_device`）；直接把可执行文件拷出包外会丢失 Info.plist，窗口内 WebSocket 会被 ATS 拦截。

> 依赖打包：音频 Opus 编码在 macOS arm64 / Windows x64 使用仓库内 vendored 静态库（`third_party/libopus/`），`.app` 与 `.exe` 静态链接、**运行时不需要安装 Homebrew 或 opus**；BSD-3-Clause 许可文本会随 `.app` 放进 `Contents/Resources/third_party/` 并在仓库 `third_party/libopus/COPYING` 保留。Linux 静态链接系统 libopus（构建需 `libopus-dev`），**运行期不再需要 `libopus0`**；但 GTK3/WebKitGTK 官方不提供静态库且 WebKit 的进程模型要求动态加载，故 Linux 包无法做到全静态，运行库仍需安装（见上文启动说明）。

### CI 自动构建产物

推送 main 或提交 PR 时，GitHub Actions 会跑完整质量门（gofmt/vet/test/前端检查 + 三平台构建）并上传可下载的安装包（Actions 页面 run 的 Artifacts 区，保留 14 天）：

| Artifact | 内容 |
| :--- | :--- |
| `AnyDevice_macOS` | 三个 `.app`：`_arm64`（Apple Silicon）/ `_amd64`（Intel）/ `_universal`（双架构通吃）；均含 Info.plist + 图标 + 许可文本 + ad-hoc 签名 |
| `AnyDevice_windows_amd64` | `any_device.exe`（Wails 原生窗口）+ `any_device_portable.exe`（浏览器壳回退） |
| `AnyDevice_linux_amd64` | Linux 原生窗口二进制（GTK3 + WebKitGTK） |

### 测试

```bash
go test ./...                                   # 默认测试（不含 cgo 依赖的音频链路测试）
go test -tags audiotest ./internal/audio -run RoundTrip -v
                                                # Opus 编码→解码往返保真：
                                                # 断言解出的正弦幅度/频率与输入一致
                                                # （曾因把 float32 缓冲按 int16 传入
                                                #   opus_encode 而产生满量程噪声=破音）
```

### 前端开发

```bash
cd frontend
bun install        # 安装依赖（包管理器：bun）
bun run dev        # 启动 Vite 开发服务器（默认 5173）
                   # /api 与 /ws 自动代理到本机 8080 的 AnyDevice 服务
bun run build      # 构建产物到 internal/web/dist（随后 go build 即可）
```

> 全新检出后若直接 `go build`，`internal/web/dist` 只有占位文件，页面为空白——请先执行前端构建（或使用 `scripts/build.sh`）。

> 多显示器 / 高 DPI：窗口按 macOS 逻辑坐标渲染（Retina 自动 2x），无需额外配置；窗口出现在哪块屏幕不影响服务监听、局域网地址与扫码连接。

> macOS 权限提示：`.app` 采用 ad-hoc 签名（`codesign -s -`），每次重新构建后系统签名身份会变化，「辅助功能」授权可能失效——到 系统设置 → 隐私与安全性 → 辅助功能 重新勾选 `AnyDevice.app` 即可；麦克风授权首次运行时弹窗。
