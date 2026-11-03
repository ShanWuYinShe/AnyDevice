package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"any_device/internal/audio"
	"any_device/internal/keyboard"
	"any_device/internal/mouse"
	"any_device/internal/server"
	"any_device/internal/ui"
	"any_device/internal/util"
)

// Version 是应用版本号。发布构建由 CI 用 -ldflags -X main.Version=<tag>
// 注入（tag 为唯一版本源，见 .github/workflows/release.yml）；这里的默认值
// 仅用于本地 go run/go build 调试，不参与发布。
var Version = "dev"

func main() {
	port := flag.Int("port", 8080, "Web & WebSocket server port")
	mode := flag.String("mode", "auto", "Audio capture mode: only 'test-tone' is effective (requires ANY_DEVICE_TEST_TONE=1); other values are accepted for compatibility")
	customIP := flag.String("ip", "", "Manually specify the LAN IP advertised to peers (listening stays on all interfaces)")
	deviceIdx := flag.Int("device", -1, "Select system-audio device index on Windows (use -list to view)")
	listDevices := flag.Bool("list", false, "List all available audio devices and exit")
	listNics := flag.Bool("list-nics", false, "List all network interfaces and IP addresses and exit")
	noQR := flag.Bool("no-qr", false, "Disable printing QR code to the terminal")
	noUI := flag.Bool("no-ui", false, "Run in headless mode without launching native desktop window")
	flag.Parse()

	if *listDevices {
		printDeviceList()
		return
	}

	if *listNics {
		util.PrintAllInterfaces()
		return
	}

	// 日志落文件：Wails GUI 版无控制台，引擎初始化失败等关键信息
	// 写入 exe 同目录的 any_device.log（无权限回退临时目录）
	util.InitLogFile()

	printBanner()

	// Check devices before starting
	devices, err := audio.ListAllDevices()
	if err != nil {
		// 枚举失败按「未检测到设备」继续走提示流程，但必须留痕便于排查
		util.Logf("[启动] 枚举音频设备失败: %v", err)
	}
	hasPlayback := false
	for _, d := range devices {
		if d.Type == "playback" {
			hasPlayback = true
			break
		}
	}

	if !hasPlayback && *mode != "test-tone" {
		util.PrintNoDeviceHelp()
	}

	// 1. Initialize Mouse & Keyboard Capture Engines
	var hub *server.Hub
	mouseEngine := mouse.NewEngine(func(evt mouse.MouseEvent) {
		if data, err := json.Marshal(evt); err == nil {
			hub.BroadcastMouse(data)
		}
	})

	keyboardEngine := keyboard.NewEngine(func(evt keyboard.KeyEvent) {
		if data, err := json.Marshal(evt); err == nil {
			hub.BroadcastKeyboard(data)
		}
	})

	// 2. Initialize WebSocket 1-to-N broadcast hub
	// 音频引擎在 hub 之后才创建，先声明变量供下方闭包引用（与上方 hub 变量同模式）
	var systemAudioEngine *audio.Engine
	var micAudioEngine *audio.Engine
	hub = server.NewHub(func(sysAudioSubs, micAudioSubs, mouseSubs, keyboardSubs int) {
		mouseEngine.SetSubscribers(int64(mouseSubs))
		keyboardEngine.SetSubscribers(int64(keyboardSubs))
		// 系统声与麦克风分别按各自订阅数按需启停：只映射系统声时不再
		// 同时打开麦克风采集（Windows 采集 APO/降噪处理会白耗 CPU，且
		// 麦克风常驻采集是隐私问题）。
		if systemAudioEngine != nil {
			systemAudioEngine.SetSubscribers(int64(sysAudioSubs))
		}
		if micAudioEngine != nil {
			micAudioEngine.SetSubscribers(int64(micAudioSubs))
		}
	})
	go hub.Run()

	// 键鼠捕获按订阅数按需安装/卸载系统级钩子：无人映射键鼠时不常驻
	// WH_MOUSE_LL/WH_KEYBOARD_LL（常驻全局钩子会让全系统输入事件都绕经
	// 本进程，高回报率鼠标下明显增加系统开销）。
	mouseEngine.StartSupervisor()
	keyboardEngine.StartSupervisor()

	// 3. Initialize Audio Engines (Independent System Output & Microphone Input)
	// 系统声回调：占位测试音只广播给远程订阅端——本机 UI 播放测试音没有意义
	// （会听到系统提示音般的周期性"滴滴"），远程映射本机系统声时仍需测试音确认链路。
	sysDefaultName, micDefaultName := audio.DefaultDeviceNames(devices)
	systemAudioEngine = audio.NewEngine(audio.KindSystem, audio.CaptureMode(*mode), *deviceIdx, func(data []byte) {
		if systemAudioEngine != nil && systemAudioEngine.IsTestTone() {
			hub.BroadcastAudioRemoteOnly(data)
			return
		}
		hub.BroadcastAudio(data)
	})
	systemAudioEngine.SetDefaultName(sysDefaultName)
	systemAudioEngine.StartSupervisor()

	micAudioEngine = audio.NewEngine(audio.KindMic, audio.CaptureMode(*mode), -1, hub.BroadcastAudio)
	micAudioEngine.SetDefaultName(micDefaultName)
	micAudioEngine.StartSupervisor()

	// 统一收尾：先停监督 goroutine（不再有人启停采集），再释放资源。
	// 幂等；错误退出路径也会调用，避免 os.Exit 跳过 defer 留下设备/钩子。
	stopAll := func() {
		systemAudioEngine.StopSupervisor()
		systemAudioEngine.Stop()
		micAudioEngine.StopSupervisor()
		micAudioEngine.Stop()
		mouseEngine.StopSupervisor()
		mouseEngine.Stop()
		keyboardEngine.StopSupervisor()
		keyboardEngine.Stop()
	}
	defer stopAll()

	// 4. Network & URL Information (Using OS routing resolution to skip virtual adapters)
	primaryIP := util.GetPrimaryIP(*customIP)
	primaryURL := fmt.Sprintf("http://%s:%d", primaryIP, *port)
	localURL := fmt.Sprintf("http://localhost:%d", *port)

	fmt.Printf("\n🚀 AnyDevice 跨端双向设备映射服务已就绪！\n")
	fmt.Printf("├─ 操作系统: %s (%s)\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("├─ 系统声音: %s\n", systemAudioEngine.DeviceName())
	fmt.Printf("├─ 麦克风: %s\n", micAudioEngine.DeviceName())
	fmt.Printf("├─ 鼠标映射: 全桌面物理光标与按键无缝同步\n")
	fmt.Printf("├─ 键盘映射: 跨端物理按键实时同步\n")
	fmt.Printf("├─ 本地窗口: %s\n", localURL)
	fmt.Printf("└─ 局域网地址: %s\n", primaryURL)
	util.PrintNetworkInfo(primaryIP, *port)

	if !*noQR {
		fmt.Printf("\n📱 手机/平板/电脑 扫码打开控制台 (在 UI 上自由映射对端设备):\n\n")
		util.PrintQRCode(primaryURL)
	}

	// 5. HTTP & WebSocket 服务：先同步绑定端口，确认监听成功后再打开窗口，
	// 保证窗口首屏加载时服务必然就绪（此前异步启动存在竞态：窗口先起、
	// 服务未就绪，首屏 HTML 加载失败直接白屏，用户需要重开一次）
	srv := server.NewServer(*port, primaryIP, hub, systemAudioEngine, micAudioEngine, mouseEngine, keyboardEngine)
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		util.Logf("[Fatal] 端口 %d 监听失败（可能已被其他实例占用）: %v", *port, err)
		// 此时各引擎已就绪，os.Exit 会跳过 defer——手动清理再退出
		stopAll()
		os.Exit(1)
	}
	go func() {
		err := http.Serve(ln, srv.SetupRoutes())
		// 正常退出（-no-ui 收到信号后主动 Close 监听）时 Serve 返回
		// net.ErrClosed；这不是错误，不能打 [Fatal] 也不能 exit 1——否则会
		// 跳过优雅收尾、留下 Windows 下未释放的 WASAPI/COM 资源。
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			// os.Exit 会跳过所有 defer（各引擎 Stop、Windows 下 WASAPI/COM
			// 释放）——失败路径手动走一遍与正常退出相同的清理再退出
			util.Logf("[Fatal] HTTP server error: %v", err)
			stopAll()
			os.Exit(1)
		}
	}()

	// macOS 键鼠映射依赖「辅助功能」授权：未授权时系统会静默丢弃所有
	// 模拟按键与光标操作（无任何报错），表现为跨端映射完全无反应。
	// 启动即检测并给出明确指引，避免用户无从排查。
	if runtime.GOOS == "darwin" && !keyboard.AccessibilityTrusted() {
		// 同时走 util.Logf：.app 由 Finder 启动时 stdout 不可见，日志文件是唯一排查通道
		util.Logf("")
		util.Logf("⚠️  未授予「辅助功能」权限：键盘与鼠标的跨端映射将无法在本机执行！")
		util.Logf("    请打开 系统设置 → 隐私与安全性 → 辅助功能，")
		util.Logf("    添加并勾选 AnyDevice.app（若从终端直接运行包内可执行文件，还需勾选对应终端程序），然后重启本应用。")
		util.Logf("")
	}

	// 6. Desktop Window vs Headless Mode
	if !*noUI {
		// 桌面窗口：默认（-tags wails）为 Wails 原生窗口（WebView2/WebKit，
		// 无控制台黑窗、无浏览器启动器竞态，关窗即安全退出）；
		// 未启用 wails tag 的构建回退为浏览器 --app 独立窗口。
		ui.SetClientCounter(hub.ClientCount)
		fmt.Println("🖥️  正在打开桌面原生独立应用窗口...")
		ui.RunDesktop(srv.SetupRoutes(), localURL, "AnyDevice - 跨端双向设备映射中心", 1020, 680)
	} else {
		fmt.Printf("\n💡 无窗口模式运行中。按 Ctrl+C 退出程序...\n\n")
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan
		// 主动关闭监听：正在处理的 HTTP/WS 请求先收尾，避免硬断
		_ = ln.Close()
	}

	fmt.Println("\n正在停止 AnyDevice 服务...")
	stopAll()
	fmt.Println("服务已安全退出。")
}

func printBanner() {
	fmt.Println("==================================================================")
	fmt.Printf("    AnyDevice v%s - 跨端音频、鼠标与键盘多设备无线映射中心\n", Version)
	fmt.Println("==================================================================")
}

func printDeviceList() {
	printBanner()
	devices, err := audio.ListAllDevices()
	if err != nil {
		fmt.Printf("获取音频设备失败: %v\n", err)
		return
	}

	if len(devices) == 0 {
		fmt.Println("未找到任何音频设备！")
		util.PrintNoDeviceHelp()
		return
	}

	fmt.Println("可用音频设备列表：")
	fmt.Println("------------------------------------------------------------------")
	for _, d := range devices {
		tag := ""
		if d.IsVirtual {
			tag += " [虚拟设备]"
		}
		if d.IsDefault {
			tag += " (默认)"
		}
		fmt.Printf("[%s #%d] %s%s\n", d.Type, d.Index, d.Name, tag)
	}
	fmt.Println("------------------------------------------------------------------")
	fmt.Println("使用示例: any_device -device 0（Windows 系统声设备选择）")
}
