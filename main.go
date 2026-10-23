package main

import (
	"encoding/json"
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

const Version = "1.2.0"

func main() {
	port := flag.Int("port", 8080, "Web & WebSocket server port")
	mode := flag.String("mode", "auto", "Audio capture mode: 'auto', 'loopback', 'capture', or 'test-tone'")
	customIP := flag.String("ip", "", "Manually specify primary LAN IP (useful when multiple virtual NICs exist)")
	deviceIdx := flag.Int("device", -1, "Select specific audio device index (use -list to view)")
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
	hub = server.NewHub(func(audioSubs, mouseSubs, keyboardSubs int) {
		mouseEngine.SetSubscribers(int64(mouseSubs))
		keyboardEngine.SetSubscribers(int64(keyboardSubs))
		// 将音频订阅数回传给两个音频引擎：无订阅者时引擎静默，避免空转生成 PCM
		if systemAudioEngine != nil {
			systemAudioEngine.SetSubscribers(int64(audioSubs))
		}
		if micAudioEngine != nil {
			micAudioEngine.SetSubscribers(int64(audioSubs))
		}
	})
	go hub.Run()

	mouseEngine.Start()
	defer mouseEngine.Stop()

	keyboardEngine.Start()
	defer keyboardEngine.Stop()

	// 3. Initialize Audio Engines (Independent System Output & Microphone Input)
	// 系统声回调：占位测试音只广播给远程订阅端——本机 UI 播放测试音没有意义
	// （会听到系统提示音般的周期性"滴滴"），远程映射本机系统声时仍需测试音确认链路。
	systemAudioEngine = audio.NewEngine(audio.KindSystem, audio.CaptureMode(*mode), *deviceIdx, func(data []byte) {
		if systemAudioEngine != nil && systemAudioEngine.IsTestTone() {
			hub.BroadcastAudioRemoteOnly(data)
			return
		}
		hub.BroadcastAudio(data)
	})
	go func() {
		if err := systemAudioEngine.Start(); err != nil {
			util.Logf("[Audio] 系统声音捕获初始化警告: %v", err)
		}
		util.Logf("[启动] 系统声音引擎: %s", systemAudioEngine.DeviceName())
	}()
	defer systemAudioEngine.Stop()

	micAudioEngine = audio.NewEngine(audio.KindMic, audio.CaptureMode(*mode), -1, hub.BroadcastAudio)
	go func() {
		if err := micAudioEngine.Start(); err != nil {
			util.Logf("[Audio] 麦克风输入捕获初始化警告: %v\n", err)
		}
		util.Logf("[启动] 麦克风引擎: %s", micAudioEngine.DeviceName())
	}()
	defer micAudioEngine.Stop()

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
		os.Exit(1)
	}
	go func() {
		if err := http.Serve(ln, srv.SetupRoutes()); err != nil && err != http.ErrServerClosed {
			// os.Exit 会跳过所有 defer（各引擎 Stop、Windows 下 WASAPI/COM
			// 释放）——失败路径手动走一遍与正常退出相同的清理再退出
			util.Logf("[Fatal] HTTP server error: %v", err)
			systemAudioEngine.Stop()
			micAudioEngine.Stop()
			mouseEngine.Stop()
			keyboardEngine.Stop()
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
	}

	fmt.Println("\n正在停止 AnyDevice 服务...")
	systemAudioEngine.Stop()
	micAudioEngine.Stop()
	mouseEngine.Stop()
	keyboardEngine.Stop()
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
	fmt.Println("使用示例: any_device -mode capture -device 0")
}
