# AnyDevice - 局域网全平台音频无线扬声器 🎵

> 将台式机/电脑的声音实时无线传输到手机、平板、另一台电脑或电视上播放。解决台式机没有物理音箱的困扰，支持全平台发送端与**任意多端同时接收（1 对多广播）**。

---

## 🌟 核心特性

- **跨平台发送端**：核心支持 **Windows** (WASAPI Loopback)、**macOS** (CoreAudio) 与 **Linux** (PulseAudio/PipeWire)。
- **全平台零安装接收端**：利用现代浏览器 HTML5 Web Audio API，**手机/平板/电脑无需安装任何 App**，微信/系统相机扫码即播。
- **任意多端同时播放**：支持 1 对多广播，多个设备可在同一个局域网内同时作为音响出声。
- **超低延迟架构**：48kHz 16-bit 双声道立体声，网络丢包抗抖动缓冲，端到端延迟低至 30~50ms，音画同步极佳。
- **无音箱场景专解**：针对 Windows 台式机未插物理音箱导致系统禁用音频的问题，提供一键虚拟声卡安装方案。
- **屏幕防休眠 (WakeLock)**：手机打开网页后自动保持屏幕常亮，防止手机锁屏休眠中断音频。
- **实时音频可视化**：网页端内置示波器动态波形，即使静音也能直观确认声音正在传输。

---

## 🚀 快速上手 (Windows 台式机)

### 第一步：解决“无音箱”问题（安装虚拟声卡）

如果台式机没有插任何耳机或音箱，Windows 会提示“未安装音频设备”并拒绝发声。请先安装免费的虚拟音频驱动（已安装过或插了耳机可跳过此步）：

**方式 A（推荐 Winget 一键安装）**：
以管理员身份打开 PowerShell 执行：
```powershell
winget install --id VB-Audio.VirtualCable --accept-package-agreements
```

**方式 B（使用本项目自带脚本）**：
右键以管理员身份运行 PowerShell：
```powershell
powershell -ExecutionPolicy Bypass -File scripts\install_virtual_audio_windows.ps1
```

> 安装完成后，在 Windows 任务栏右下角音量图标中，将默认播放设备切换为 **`CABLE Input (VB-Audio Virtual Cable)`**。

---

### 第二步：启动服务端

直接双击运行 `any_device.exe`，或者在终端运行：

```powershell
.\any_device.exe
```

程序启动后会自动：
1. 捕获系统输出声音（或虚拟声卡声音）；
2. 打印控制台 ASCII 二维码；
3. 输出本地局域网网址（如 `http://192.168.1.100:8080`）。

---

### 第三步：手机/平板/其他设备播放

1. 确保手机与台式机连接在**同一个局域网 Wi-Fi** 下；
2. 用手机相机或浏览器扫描终端中的**二维码**（或直接输入局域网 IP 网址）；
3. 打开网页后，点击大按钮 **「开启扬声器播放」**，手机立即化身无线音箱！
4. **多端同时加入**：拿多部手机、iPad 或另一台笔记本打开同一链接，所有设备会同时响声。

---

## 🛠️ 常用命令行参数

| 参数 | 默认值 | 说明 |
| :--- | :--- | :--- |
| `-port` | `8080` | 指定 HTTP 和 WebSocket 服务端口 |
| `-mode` | `auto` | 音频捕获模式：`auto` (自动)、`loopback` (系统回环)、`capture` (输入端/麦克风)、`test-tone` (生成 440Hz 测试音频) |
| `-list` | `false` | 列出本机所有音频设备（含虚拟声卡标记）并退出 |
| `-device` | `-1` | 手动指定特定设备序号（通过 `-list` 查看） |
| `-no-qr` | `false` | 关闭终端二维码打印 |

#### 实用示例：

- **查看所有声卡设备**：
  ```bash
  any_device -list
  ```
- **纯测试串流（无需任何真实声卡）**：
  ```bash
  any_device -mode test-tone
  ```
- **指定端口与特定设备**：
  ```bash
  any_device -port 9000 -mode loopback -device 1
  ```

---

## 💻 跨平台支持与编译

本项目采用 **Go 1.22+** 编写，底层依赖轻量级 `malgo` (miniaudio) 音频引擎。

### 在 macOS / Linux 上编译：
```bash
go build -o any_device main.go
```

### 交叉编译 Windows `.exe`（在 macOS 上）：
```bash
./scripts/build.sh
```

---

## 📁 目录结构

```
any_device/
├── main.go                     # 应用程序入口与命令行参数解析
├── any_device                  # macOS 编译产物
├── any_device.exe              # Windows 编译产物
├── internal/
│   ├── audio/                  # 音频引擎 (WASAPI loopback / 设备检测 / 测试音频发生器)
│   ├── server/                 # HTTP API 与 WebSocket 1对多广播 Hub
│   ├── web/                    # 静态文件嵌入与 Web Audio 播放器前端
│   └── util/                   # 局域网 IP 获取 / 终端二维码 / 虚拟声卡引导
└── scripts/
    ├── build.sh                # 跨平台构建脚本
    └── install_virtual_audio_windows.ps1 # Windows 虚拟驱动静默安装脚本
```
