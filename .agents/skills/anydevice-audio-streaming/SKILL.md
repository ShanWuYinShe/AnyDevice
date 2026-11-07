---
name: anydevice-audio-streaming
description: AnyDevice 跨设备实时音频流（采集→传输→播放）架构与验证：WASAPI Loopback/miniaudio 捕获、float 全链防削波、AudioWorklet 自适应抖动缓冲（40–200ms）、发送端 Opus 编码档位自适应、按订阅数启停采集与系统钩子、平台解码差异（WebCodecs/WKWebView）、端到端延迟分侧测量、协议版本防呆。触发：改 internal/audio、internal/mouse、internal/keyboard 的采集与钩子；音频破音/断续/电流声或完全无声；播放端欠载或积压；游戏掉帧或空闲时 CPU 偏高；延迟统计与 /api/status 诊断；set_mapping 订阅或 audio_stats 字段变更；WKWebView/Safari 解码能力差异。
---

# AnyDevice 音频串流（采集 → 传输 → 播放）

架构：Windows 系统声/麦克风采集 → Opus 编码 → WS → macOS 播放端（Web Audio / AudioWorklet；无 WebCodecs 解码器时由接收端解成 PCM 再发）。

## 代码位置与契约（改音频/钩子前先读）

- `internal/audio/capture.go`：采集引擎与按订阅启停（`StartSupervisor`）；Windows 系统声默认走 miniaudio Loopback（`devType = malgo.Loopback`），仅 `ANY_DEVICE_NATIVE_WASAPI=1` 时走自研 WASAPI（`internal/audio/wasapi_windows.go`）。
- `internal/audio/opus_encoder.go` / `internal/audio/opus_decoder.go`：编解码，运行期参数由 `internal/audio/adaptive.go` 的档位自适应调整（96–160kbps / 复杂度 3–7，默认 128kbps / 5）；解码器按音频流各持一个实例。
- `internal/mouse/event.go` / `internal/keyboard/event.go`：系统输入钩子及按订阅启停。
- `internal/server/hub.go`：WS 订阅入口（`set_mapping` 的 `sys_audio`/`mic_audio`；旧对端由 `armLegacySubs` 500ms 宽限兜底）。
- 端点：`/api/self`（能力声明，含 `audio_format`）、`/api/status`（各引擎运行状态）、`/api/peers`（对端与 `mapping.sender_{sys,mic}_latency`、`net_rtt_ms`）。
- 延迟契约：发送端经 `audio_stats` 文本消息上报 `{chunk_ms,encode_ms,send_ms,running,bitrate,complexity,level}`；`bitrate`/`complexity` 用 omitempty、`level` 不带 omitempty（0 是合法档位）。改字段名/语义须两端同改并保留旧版兼容。

## Windows 系统声捕获（WASAPI Loopback / miniaudio）

- 用设备**默认周期**与默认缓冲，不要手动指定毫秒周期（与设备实际周期错配会产生交替性空数据）。
- **SILENT 标志必须处理**：`AUDCLNT_BUFFERFLAGS_SILENT`(0x2) 时缓冲数据未定义，当音频播 = 电流声。
  miniaudio 0.11.25/26 不处理，需 patch：fork 模块后在 capture 分支补
  `ma_zero_memory_64(pMappedBufferCapture, cap * ma_get_bytes_per_frame(fmt, ch))`
  （函数名是 `ma_zero_memory_64`，不存在 `ma_zero_memory`）。
- **DATA_DISCONTINUITY**(0x1) 包丢弃并输出静音帧（撕裂/丢弃的数据不可播，静音帧维持时间轴）。
- 捕获缓冲给足（200ms）+ 捕获线程注册 **MMCSS "Audio"** 任务
  （avrt.dll `AvSetMmThreadCharacteristicsW`）：游戏满载 CPU 时音频线程不被饿出调度。
  **仅自研 WASAPI 路径注册 MMCSS**（`internal/audio/wasapi_windows.go`）；默认 miniaudio Loopback 路径没有 MMCSS，不要据此把默认路径的断音误判为「没注册 MMCSS」。
- 捕获线程的回调/统计必须无锁或 atomic——音频实时线程阻塞会与初始化路径互等死锁。
- float32 共享模式混音样本可 >1.0（混音域不削波），解码为归一化 float 时**保留原值不钳位**。

## float 全链（防削波破音）

- 采集、传输、缓冲、播放**全程 float32**。任何 float→int16 的提前转换 = 破音源头
  （超界硬钳位不可逆）。带宽代价（48kHz 立体声约 384KB/s）局域网可接受。
- 软限幅放最后，**只衰减永不放大**：`if peak > 0.95 { gain = 0.95/peak }`，随后缓慢恢复到 1。

## AudioWorklet 环形缓冲（播放端标准架构）

- worklet 内 ring buffer：主线程 postMessage 写入、音频线程顺序读取（天然无缝无重叠）。
- 预缓冲水位**自适应**（40–200ms，初始 60ms，欠载快升/稳定慢降，详见下一节）；欠载后重新 priming（恢复无碎音）；溢出丢最旧（延迟恒定）。不要退回固定 100ms 大缓冲——它直接抬高端到端延迟且不随负载收放。
- 系统声/麦克风**各持独立 ring**，共用时间轴会互相推挤产生杂音。
- 静音帧也要写入 ring 推进时间轴（直接丢弃会导致时间轴漂移 → 调度重置爆音）。

## 自适应：播放端抖动缓冲与发送端编码档位（延迟 / 音质 / 性能平衡）

目标：低负载时压低延迟、提高音质；高负载时让出 CPU 保流畅——三者不互相无条件牺牲。

### 播放端环形缓冲（`frontend/src/lib/audio.js` 的 `PCMRingPlayer`）

- 预缓冲目标水位自适应：初始 60ms、范围 [40, 200]ms；目标帧数按 worklet 全局 `sampleRate` 换算。
- 欠载快升：`filled` 抽空 → `underflows++`、目标 +30ms（封顶 200）、`priming = true`；先保住不断音。
- 稳定慢降：未 `priming` 且期间无欠载 → 累计 5s 后目标 -5ms（下限 40），随后稳定计时清零。
  **不要逐量子校验「水位 >= 目标」**：20ms 数据包到达后水位峰值才等于目标、随即被消费，
  该条件只在单个量子成立，逐量子校验会让慢降永不触发（目标会一直停在 60ms）。
- `stats` 消息必须带 `targetMs`（与 `filled/cap/underflows/overflows` 同一条）；主线程
  `streamStats`/`getStreamStats`/`getBufferTargetMs` 暴露给 UI。溢出仍丢最旧、静音帧仍推进时间轴、
  sys/mic 仍各持独立 ring。
- UI：对端卡片延迟 title 显示「播放缓冲 Xms（自适应目标 Yms）」。
- 数值验证（不需要浏览器）：Node 脚本从 `audio.js` 正则提取 `WORKLET_SOURCE`，用 `new Function`
  注入假 `AudioWorkletProcessor`/`registerProcessor` 实例化真实 worklet 类，按虚拟时钟
  （每 20ms 投 960 帧、每 128 帧调 `process()`）驱动并断言：初始 60ms；理想网络约 25s 降到 40ms；
  断流 220ms 后目标 +30ms 且欠载计 1 次；连续断流封顶 200ms；±8ms 抖动下不再欠载；
  1%–5% 丢包时目标自动上调且始终在 [40,200]。

### 发送端编码参数（`internal/audio/adaptive.go` + `capture.go` 的 `encodeLoop`）

- 档位：`LevelLow`=96kbps/complexity 3、`LevelBalanced`=128kbps/5（默认）、`LevelQuality`=160kbps/7。
- 决策是纯函数 `decideAdaptive(cur, signals)`（表驱动单测）；控制器状态只在 `encodeLoop` 的单
  goroutine 内维护——不新增常驻 goroutine/定时器（按需启停会反复 Start/Stop，常驻对象容易泄漏）。
- 降档（保护优先）：队列积压 ≥4 帧或本次运行出现丢帧立即降；编码延迟（采集→编码完成 EWMA）≥35ms
  需连续 2 次采样（采样间隔 2s）确认。升档：≤28ms 且无积压/丢帧持续 ≥15s。任意切换后冷却 8s。
- `SetBitrate`/`SetComplexity` 必须在 `encodeLoop` 已持有的 `opusMu` 内调用（libopus 编码器非并发安全，
  须与 `Feed` 串行）；切换失败不 `commit`，下个采样按旧档位重判。
- 帧长保持 20ms、采集设备周期不随负载改动：自适应调的是 CPU 与音质，不用帧长换延迟。
- 暴露：`EncoderParams()`/`DroppedFrames()`/`QueueDepth()`（atomic）→ `/api/status` 的
  `sys_audio_bitrate`/`sys_audio_complexity`/`sys_audio_adaptive_level`/`sys_audio_dropped`（及 `mic_` 同名）；
  `audio_stats` 的 `sys`/`mic` 增加 `bitrate`(bit/s, omitempty)/`complexity`(omitempty)/`level`(无 omitempty)。
- 旧版兼容：旧对端不上报三字段 → `bitrate`/`complexity` 缺失、`level` 退化成 0；前端以 `bitrate`
  是否存在门控「对端编码…」展示行，避免把旧对端误标成省 CPU 档。
- Stop 与 Start 都要把编码参数/档位复位为默认平衡档（停止期间 `/api/status` 不应残留上次档位）。

## 协议版本防呆

- PCM 格式升级必须两端同时生效。能力广播携带 `audio_format`（如 "f32le-48000-stereo"），
  前端检测不一致时**禁用映射 + 明确提示两端更新**（防呆优先于容错）。

## 采集端低占用与按需启停（与游戏等高负载程序共存）

- **按订阅数启停采集，而不是只在回调里丢数据**：无人订阅时不打开音频设备、不安装系统级输入钩子；
  订阅归零延迟数秒再停（宽限 5–8s），避免映射开关抖动导致设备/钩子反复初始化。
  只做 emit 门控仍会占着 WASAPI 流、麦克风 APO 与全局钩子——这正是「程序开着就掉帧」的常见来源。
- **系统声与麦克风分开订阅**：只映射系统声时不要顺带打开麦克风采集（采集 APO/降噪、audiodg CPU、隐私指示）。
  信令里分 `sys_audio`/`mic_audio` 两路，旧版并集字段保留兼容。
- **本机共享开关必须门控采集**：用户关闭「共享麦克风」后，即使对端仍在订阅也要立即停止采集，不能只改发现广播。
- **采集线程优先级不要随便降**：采集线程稳态 CPU 通常 <1%，降优先级对帧率几乎无感，却会在 CPU 满载时
  拉长调度延迟导致 WASAPI overrun（断音）；保持 miniaudio 默认即可。
- Opus 编码参数按档位自适应（见「发送端编码参数」）：默认平衡档 128kbps/复杂度 5，高负载降 96kbps/3、
  持续低负载升 160kbps/7；不要写死成单一码率/复杂度。
- **按需启停的三个关键约束**（同一引擎对象反复 Start/Stop 时）：
  1. 展示名/状态字段不能兼作「本次是否找到设备」的判断——Stop 不清空展示名，第二次 Start 会误跳过
     「无虚拟回环设备」回退，退化成采集默认麦克风并当系统声推流。用函数返回值表达本次探测结果。
  2. 编码/诊断 goroutine 必须以参数接收本次的队列与退出信号（如 `go encodeLoop(queue, done)`），
     不要读引擎字段：Stop 关闭旧通道后 Start 立即重建字段，旧 goroutine 读字段就是数据竞争（`-race` 可复现）。
  3. Stop 要 join 编码 goroutine（或确保它随退出信号退出），否则每次启停泄漏一个 goroutine。
- **Windows 低级钩子 API 名**是 `SetWindowsHookExW`（user32，单 Hook）。拼写成 `SetWindowsHooksExW` 时
  `NewProc().Find()` 静默失败 → 永远走 60Hz 轮询回退（`GetAsyncKeyState` 每帧上百次系统调用，订阅时反而更耗 CPU）。
  钩子安装失败要有日志，便于区分「事件驱动」与「轮询回退」。

## 端到端延迟的分侧测量与展示（判断延迟在发送端还是接收端）

- **不要为测延迟改音频帧格式**（在 Opus 包前塞时间戳）：旧接收端遇到未知 tag 会把整包当 PCM 播放（噪声），
  等于强制两端同时升级。用**独立 WS 文本消息**上报发送端统计（如 `{"type":"audio_stats","sys":{...}}`）：
  旧前端会忽略未知文本类型、旧接收端不解析，天然向后兼容。
- 分侧公式（UI 标注「估算」，不是端到端时钟同步测量）：
  `总延迟 ≈ 发送端(采集块时长 + 采集回调→编码完成 + 广播队列→WS 写完成) + 网络 RTT/2 + 接收端(环形缓冲水位/采样率（自适应目标 40–200ms，初始 60ms）+ AudioContext baseLatency+outputLatency)`。
- **发送端**：采集回调给每帧打时间戳入队；编码 goroutine 计算「采集→编码完成」，它包含 Opus 20ms 组帧等待
  （最容易被忽略的固定延迟）；WS 写完成时间差统计发送队列。用 EWMA 平滑避免 UI 数字乱跳；
  Stop 的统计复位移到编码锁释放之后，否则在途编码写会覆盖复位值（窄竞态）。
- **网络**：出站连接每 3s 发 WS ping，pong handler 只认自己发过的 pending ping（gorilla `WriteControl` 可与其它写并发）。
  亚毫秒 RTT 向上取整为 1ms：0 应保留给「未知」，别用 0 表示「已测到但很小」。
- **接收端**：AudioWorklet 的 `filled/48000*1000` 是当前抖动缓冲延迟，`stats.targetMs` 是自适应目标
  （稳定后应接近 40ms；持续欠载会抬高，最高 200ms）；`baseLatency+outputLatency` 是设备输出延迟
  （WKWebView/Safari 可能没有 `outputLatency` → 按 0 计并在 UI 注明，总量会偏小）。
  **underflows 持续增长 = 接收端负载高、在反复重新预缓冲**，是最直接的「延迟在接收端」证据。
- **发送端编码档位**：`/api/status` 的 `sys_audio_adaptive_level`（0 省 CPU / 1 平衡 / 2 音质优先）、
  `sys_audio_bitrate`（bit/s）、`sys_audio_dropped`（累计丢帧）可直接判断自适应是否在按负载工作。
- 判断口径：播放缓冲大 / 欠载增长 → 接收端；采集块/编码/发送分项大 → 发送端；RTT 大 → 网络。
- 手动添加 IP 的对端没有发现广播：默认 provides 是全 true 但 `audio_format` 为空，前端协议防呆会因此
  **禁用音频映射**；正确做法是 add 时异步 GET 对端 `/api/self` 补全能力声明（含 audio_format）与 OS/设备名。

## 低占用的其余来源（连接/解码/分配/基准）

- **「连接动作」本身不得启动采集**：按需启停最常见的漏网之鱼是「新连接默认订阅」——任何 WS 连接
  （手机浏览器打开 UI、对端重连）会立刻 SetSubscribers(1) → 启动音频设备 + 安装键鼠钩子，
  握手到达后再关掉，却仍要跑满 idleGrace（5–8s），Windows 上还会每次连接都 churn 音频设备、
  麦克风隐私指示闪烁。正确做法：新连接一律默认不订阅；旧版对端用「宽限兜底」（如 500ms 后仍未
  收到 `set_mapping` 才全开）兼容；显式订阅到达即取消兜底；注销时停定时器。验证：连接后 100ms 内
  订阅计数应为 0，宽限到点后旧端才变 1。
- **接收端解码门控**：把对端 Opus 解码成 PCM 只应发生在「确实有客户端需要 PCM 且订阅了该流」时
  （WKWebView/Safari 无 WebCodecs AudioDecoder 才需要）。否则每帧解码 + 每包约 16KB 分配是纯浪费
  （实测 0.15–0.21% 单核/路 + 1.6MB/s）；Windows/Chrome 客户端应继续收 Opus。
- **采集编码零分配**：`accum` 按帧容量预分配 + 编码后原地左移（`copy` 后缩长），不要用
  `accum = accum[N:]`（容量很快耗尽、反复扩容）；让编码器直接消费采集的 float32 小端字节流，
  省掉每块一份 `[]float32` 中转（10ms 立体声 ≈3.8KB/块，两路 ≈1MB/s 堆分配）。
- **网卡枚举缓存**：IPv6 地址枚举不要放在 IPv4 地址双层循环里（每遇一个 v4 就全量 `InterfaceAddrs`，
  O(N²) 系统调用 + 重复入表）；缓存切片的**读取也必须加锁拷贝**，否则与 TTL 刷新并发读写是数据竞争
  （`-race` 可复现，且旧代码常见）。
- **GUI 子系统日志**：`io.MultiWriter(os.Stdout, file)` 在 stdout 写失败时短路（Windows GUI 子系统
  stdout 是无效句柄），会导致日志文件全空、排查信息全丢；必须 file-first、stdout 尽力而为。
- **基准要用真实信号**：Opus 对全零/纯音有快速路径，静音基准会低估编码成本 2–2.8×；
  用游戏混合/白噪近似真实负载。高 load 机器上 `ns/op` 噪声大，回归判定优先看 `B/op` 与 `allocs/op`
  （确定性指标），并用 `git archive` 旧树 + `go test -overlay` 注入测试做 A/B（不改工作区源码）。

## 播放端解码能力差异（WebCodecs 并非全平台）

- **Chrome/Edge/WebView2 有 `AudioDecoder`；Safari 与 WKWebView 没有**：macOS 15.8 的
  WKWebView 里 `typeof AudioDecoder === 'undefined'`（`VideoDecoder` 存在）。Safari 浏览器同理。
- 危险的自检陷阱：这类 WebView 里 `isSecureContext === true`、AudioWorklet 可用，于是
  "Web Audio 能跑"会被误判成"Opus 能解码"；表现为**WS 有音频字节流入（码率正常）但完全无声**。
- 不要假设所有客户端能解 Opus。正确做法是按客户端能力分流：
  1. 客户端连接时声明能力（如信令里带 `pcm: typeof AudioDecoder === 'undefined'`）；
  2. 源端仍只传 Opus（带宽约 PCM 的 1/24）；
  3. **接收端中继进程**把 Opus 用 libopus 解码成 float32 PCM，只发给声明 pcm 的客户端；
     有解码器的客户端继续收 Opus。可加 `/api/status` 的"pcm 客户端数"做诊断。
  4. 不要为图省事给所有客户端统一下发 PCM：手机/局域网带宽会膨胀约 24 倍。
- 服务端解码器要**按音频流各持一个实例**（Opus 解码器有状态，系统声/麦克风混用会状态错乱）；
  解码失败回退只发 Opus，保证有解码能力的客户端不受影响。
- 自绘 scheme 页面（如 `wails://wails/`）里 `location.host` 是 scheme 主机名（`wails`），
  用 `协议//location.host` 拼 WebSocket 地址会得到 `ws://wails/ws` 并必然 1006 失败；
  桌面壳内必须显式连 `ws://127.0.0.1:<port>`，端口从自有 HTTP API 读取（不要写死）。

## 排障与验证

- 两端版本一致性先确认（`/api/self` 的 `build_time`），再看数据。
- 采集启停验证：`/api/status` 的 `*_engine`/`*_capture` 布尔，无人订阅时应全 false；连接后 100ms 内订阅计数应为 0，旧端宽限到点才变 1。
- 自适应验证：播放稳定 20s 后 `window.__audioDebug().stats.sys.targetMs` 应从 60 降到 40；人为让推流停顿
  200ms 后 target 应 +30ms 并出现 underflow。发送端在游戏满载/压测时 `sys_audio_adaptive_level` 应降到 0、
  `sys_audio_bitrate` 降到 96000；空闲 15s 后应升回 1/128000，长稳后可到 2/160000。
  可用 Node 脚本按上一节方法加载真实 `WORKLET_SOURCE` 做数值仿真（无需浏览器），覆盖慢降/快升/上下限。
- `audio_stats`/新字段回归：`go test ./internal/audio -run 'Adaptive|EncoderParams|SetEncoder' -v`
  与 `go test ./internal/server -run 'Latency|Status' -v` 覆盖决策表、编码参数切换与 /api/status 字段。
- 抓前端实收 PCM：浏览器一律走 `agent-browser`（红线见用户级 `browser-automation-safety`，禁止自行 spawn Chrome、禁止写自研 CDP 管线）→ CDP `Network.enable` → `Network.webSocketFrameReceived`（payloadData 为 base64，opcode 2 是二进制）→ numpy 分析削波/超界样本/跳变分布/重复块/静音段/频谱。
- node 的 WebSocket 二进制帧默认是 Blob，必须显式 `ws.binaryType = 'arraybuffer'`（否则统计恒 0）。
- 音频特征判断：真实音频为多频成分连续波形；恒定幅度周期信号或大量 DC 分量 = 非音频内容。
- 单测：`go test ./internal/...`（音频与 WS 相关覆盖集中在 `internal/audio`、`internal/server`、`internal/mouse`、`internal/keyboard`）。
- Windows 性能对照：任务管理器/Process Explorer 拆分主进程与 WebView2 子进程的 CPU/GPU；PresentMon/CapFrameX 对比 1% low 与 P99 帧时间（只看平均 FPS 会漏掉抖动），按「只开着不映射 → 映射系统声 → 映射键鼠」逐项 A/B。
- 性能基准不要用全零 PCM：Opus 对静音走快速路径，会低估编码成本约 2 倍以上；用真实正弦/音乐信号。
