package util

import (
	"fmt"
	"runtime"
)

// PrintNoDeviceHelp prints concise guidance if no active audio device is found.
func PrintNoDeviceHelp() {
	fmt.Println("\n⚠️  未检测到活动的系统音频输出设备。")
	if runtime.GOOS == "windows" {
		fmt.Println("👉 建议在 Windows 设置中将默认输出选为显示器 (HDMI/DP) 或启用系统声卡。")
		fmt.Println("👉 或者使用 -mode test-tone 参数开启测试音频流。")
	} else if runtime.GOOS == "linux" {
		fmt.Println("👉 可执行 pactl load-module module-null-sink 快速创建虚拟音频通道。")
	}
	fmt.Println()
}
