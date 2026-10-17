package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"any_device/internal/audio"
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

	printBanner()

	// Check devices before starting
	devices, _ := audio.ListAllDevices()
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

	// 1. Initialize Mouse Capture Engine
	var hub *server.Hub
	mouseEngine := mouse.NewEngine(func(evt mouse.MouseEvent) {
		if data, err := json.Marshal(evt); err == nil {
			hub.BroadcastMouse(data)
		}
	})

	// 2. Initialize WebSocket 1-to-N broadcast hub
	hub = server.NewHub(func(audioSubs, mouseSubs int) {
		mouseEngine.SetSubscribers(int64(mouseSubs))
	})
	go hub.Run()

	mouseEngine.Start()
	defer mouseEngine.Stop()

	// 3. Initialize Audio Engines (Independent System Output & Microphone Input)
	systemAudioEngine := audio.NewEngine(audio.KindSystem, audio.CaptureMode(*mode), *deviceIdx, hub.BroadcastAudio)
	if err := systemAudioEngine.Start(); err != nil {
		fmt.Printf("[Audio] 系统声音捕获初始化警告: %v\n", err)
	}
	defer systemAudioEngine.Stop()

	micAudioEngine := audio.NewEngine(audio.KindMic, audio.CaptureMode(*mode), -1, hub.BroadcastAudio)
	if err := micAudioEngine.Start(); err != nil {
		fmt.Printf("[Audio] 麦克风输入捕获初始化警告: %v\n", err)
	}
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
	fmt.Printf("├─ 本地窗口: %s\n", localURL)
	fmt.Printf("└─ 局域网地址: %s\n", primaryURL)
	util.PrintNetworkInfo(primaryIP, *port)

	if !*noQR {
		fmt.Printf("\n📱 手机/平板/电脑 扫码打开控制台 (在 UI 上自由映射对端设备):\n\n")
		util.PrintQRCode(primaryURL)
	}

	// 5. Start HTTP & WebSocket Server in background
	srv := server.NewServer(*port, primaryIP, hub, systemAudioEngine, micAudioEngine, mouseEngine)
	go func() {
		if err := srv.Start(); err != nil {
			fmt.Printf("[Fatal] HTTP server error: %v\n", err)
			os.Exit(1)
		}
	}()

	// 6. Desktop Window vs Headless Mode
	if !*noUI {
		fmt.Println("🖥️  正在打开桌面原生独立应用窗口...")
		ui.OpenNativeWindow("AnyDevice - 跨端双向设备映射中心", localURL, 1020, 680)
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
	fmt.Println("服务已安全退出。")
}

func printBanner() {
	fmt.Println("==================================================================")
	fmt.Printf("        AnyDevice v%s - 跨端音频与鼠标多设备无线映射中心\n", Version)
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
