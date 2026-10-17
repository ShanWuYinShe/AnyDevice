package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"any_device/internal/audio"
	"any_device/internal/server"
	"any_device/internal/util"
)

const Version = "1.0.0"

func main() {
	port := flag.Int("port", 8080, "Web & WebSocket server port")
	mode := flag.String("mode", "auto", "Audio capture mode: 'auto', 'loopback', 'capture', or 'test-tone'")
	deviceIdx := flag.Int("device", -1, "Select specific audio device index (use -list to view)")
	listDevices := flag.Bool("list", false, "List all available audio devices and exit")
	installVirtual := flag.Bool("install-virtual", false, "Force automatic virtual audio device setup and exit")
	noQR := flag.Bool("no-qr", false, "Disable printing QR code to the terminal")
	flag.Parse()

	if *listDevices {
		printDeviceList()
		return
	}

	if *installVirtual {
		printBanner()
		if err := util.AutoSetupVirtualDevice(); err != nil {
			fmt.Printf("自动配置失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("自动配置成功！请重新启动 any_device。")
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
		fmt.Println("\n⚠️  未检测到可用的音频输出设备（台式机无物理音箱/耳机）。")
		fmt.Println("🤖 正在全自动为您安装并配置虚拟音频驱动...")
		if err := util.AutoSetupVirtualDevice(); err != nil {
			fmt.Printf("[AutoSetup] 自动配置未成功: %v\n", err)
			util.PrintVirtualDeviceGuide()
		} else {
			fmt.Println("[AutoSetup] 虚拟声卡已就绪！")
		}
	}

	// 1. Initialize WebSocket 1-to-N broadcast hub
	hub := server.NewHub()
	go hub.Run()

	// 2. Initialize Audio Capture Engine
	engine := audio.NewEngine(audio.CaptureMode(*mode), *deviceIdx, hub.BroadcastChunk)
	if err := engine.Start(); err != nil {
		fmt.Printf("[Fatal] Failed to start audio engine: %v\n", err)
		os.Exit(1)
	}
	defer engine.Stop()

	// 3. Network & URL Information
	primaryIP := util.GetPrimaryIP()
	primaryURL := fmt.Sprintf("http://%s:%d", primaryIP, *port)
	localURL := fmt.Sprintf("http://localhost:%d", *port)

	fmt.Printf("\n🚀 AnyDevice 服务已就绪！\n")
	fmt.Printf("├─ 操作系统: %s (%s)\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("├─ 音频来源: %s\n", engine.DeviceName())
	fmt.Printf("├─ 本地访问: %s\n", localURL)
	fmt.Printf("└─ 局域网地址: %s\n", primaryURL)

	if !*noQR {
		fmt.Printf("\n📱 手机/平板/任意设备 扫码立即播放 (无需安装 App):\n\n")
		util.PrintQRCode(primaryURL)
	}

	fmt.Printf("\n💡 提示: 局域网内任意手机、电脑、平板或电视浏览器打开上述网址即可同步出声。\n")
	fmt.Printf("按 Ctrl+C 退出程序...\n\n")

	// 4. Start HTTP & WebSocket Server
	srv := server.NewServer(*port, hub, engine)
	go func() {
		if err := srv.Start(); err != nil {
			fmt.Printf("[Fatal] HTTP server error: %v\n", err)
			os.Exit(1)
		}
	}()

	// 5. Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\n正在停止 AnyDevice 服务...")
	engine.Stop()
	fmt.Println("服务已安全退出。")
}

func printBanner() {
	fmt.Println("==================================================================")
	fmt.Printf("           AnyDevice v%s - 局域网音频全端无线扬声器\n", Version)
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
		util.PrintVirtualDeviceGuide()
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
