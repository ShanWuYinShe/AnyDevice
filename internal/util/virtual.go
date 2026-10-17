package util

import (
	"fmt"
	"runtime"
)

// PrintVirtualDeviceGuide prints OS-specific guidance when no audio output device is detected.
func PrintVirtualDeviceGuide() {
	fmt.Println("\n==================================================================")
	fmt.Println("⚠️  未检测到活动音频输出设备 (台式机无物理音箱时常见)")
	fmt.Println("==================================================================")

	switch runtime.GOOS {
	case "windows":
		fmt.Println("Windows 说明：")
		fmt.Println("当台式机未插耳机/音箱时，Windows 系统会禁用音频子系统。")
		fmt.Println("解决方法：安装一个免费的虚拟音频驱动 (Virtual Audio Cable)。")
		fmt.Println("")
		fmt.Println("【一键安装方式 1 (推荐使用 Winget)】：")
		fmt.Println("  在 PowerShell (管理员) 中执行：")
		fmt.Println("  winget install --id VB-Audio.VirtualCable --accept-package-agreements")
		fmt.Println("")
		fmt.Println("【一键安装方式 2 (运行自带脚本)】：")
		fmt.Println("  在 PowerShell (管理员) 中执行项目下的：")
		fmt.Println("  powershell -ExecutionPolicy Bypass -File scripts/install_virtual_audio_windows.ps1")
		fmt.Println("")
		fmt.Println("安装完成后，Windows 会多出一个「CABLE Input」设备，任何软件均可正常发声！")

	case "darwin":
		fmt.Println("macOS 说明：")
		fmt.Println("推荐安装 BlackHole 开源虚拟音频驱动：")
		fmt.Println("  brew install blackhole-2ch")

	case "linux":
		fmt.Println("Linux 说明：")
		fmt.Println("无需安装驱动，直接使用 PulseAudio/PipeWire 创建虚拟 Null Sink：")
		fmt.Println("  pactl load-module module-null-sink sink_name=AnyDeviceSink sink_properties=device.description=AnyDevice_Virtual_Speaker")
	}

	fmt.Println("==================================================================\n")
}
