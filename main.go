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
	"any_device/internal/util"
)

const Version = "1.1.0"

func main() {
	port := flag.Int("port", 8080, "Web & WebSocket server port")
	mode := flag.String("mode", "auto", "Audio capture mode: 'auto', 'loopback', 'capture', or 'test-tone'")
	deviceIdx := flag.Int("device", -1, "Select specific audio device index (use -list to view)")
	listDevices := flag.Bool("list", false, "List all available audio devices and exit")
	noQR := flag.Bool("no-qr", false, "Disable printing QR code to the terminal")
	flag.Parse()

	if *listDevices {
		printDeviceList()
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

	// 3. Initialize Audio Capture Engine
	audioEngine := audio.NewEngine(audio.CaptureMode(*mode), *deviceIdx, hub.BroadcastAudio)
	if err := audioEngine.Start(); err != nil {
		fmt.Printf("[Fatal] Failed to start audio engine: %v\n", err)
		os.Exit(1)
	}
	defer audioEngine.Stop()

	// 4. Network & URL Information
	primaryIP := util.GetPrimaryIP()
	primaryURL := fmt.Sprintf("http://%s:%d", primaryIP, *port)
	localURL := fmt.Sprintf("http://localhost:%d", *port)

	fmt.Printf("\n🚀 AnyDevice 设备映射服务已就绪！\n")
	fmt.Printf("├─ 操作系统: %s (%s)\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("├─ 音频设备: %s\n", audioEngine.DeviceName())
	fmt.Printf("├─ 鼠标映射: 本地鼠标硬件挂载就绪 (支持实时光标/按键状态)\n")
	fmt.Printf("├─ 本地访问: %s\n", localURL)
	fmt.Printf("└─ 局域网地址: %s\n", primaryURL)

	if !*noQR {
		fmt.Printf("\n📱 手机/平板/电脑 扫码打开控制台 (在 UI 上自由勾选映射音频/鼠标):\n\n")
		util.PrintQRCode(primaryURL)
	}

	fmt.Printf("\n💡 提示: 访问网页后，可以在界面上按需勾选开启「🎧 音频」或「🖱️ 鼠标」映射。\n")
	fmt.Printf("按 Ctrl+C 退出程序...\n\n")

	// 5. Start HTTP & WebSocket Server
	srv := server.NewServer(*port, hub, audioEngine, mouseEngine)
	go func() {
		if err := srv.Start(); err != nil {
			fmt.Printf("[Fatal] HTTP server error: %v\n", err)
			os.Exit(1)
		}
	}()

	// 6. Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\n正在停止 AnyDevice 服务...")
	audioEngine.Stop()
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
