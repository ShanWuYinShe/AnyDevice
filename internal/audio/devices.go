package audio

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/gen2brain/malgo"
)

// DeviceInfo holds human-readable information about an audio device.
type DeviceInfo struct {
	Index     int    `json:"index"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
	Type      string `json:"type"` // "playback" or "capture"
	IsVirtual bool   `json:"is_virtual"`
}

// ListAllDevices enumerates both playback and capture devices on the system.
func ListAllDevices() ([]DeviceInfo, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to init audio context: %w", err)
	}
	// malgo 契约：Uninit 之后必须 Free 释放 ma_context 的 C 内存
	//（defer LIFO：先执行 Uninit 再执行 Free，缺 Free 每次调用泄漏一次）
	defer ctx.Free()
	defer ctx.Uninit()

	var result []DeviceInfo

	// Playback devices (can be used for WASAPI loopback on Windows)
	playbacks, err := ctx.Devices(malgo.Playback)
	if err == nil {
		for i, d := range playbacks {
			name := d.Name()
			result = append(result, DeviceInfo{
				Index:     i,
				ID:        fmt.Sprintf("playback:%d", i),
				Name:      name,
				IsDefault: d.IsDefault > 0,
				Type:      "playback",
				IsVirtual: isVirtualDeviceName(name),
			})
		}
	}

	// Capture devices (microphones, BlackHole, CABLE Output, Monitor sources)
	captures, err := ctx.Devices(malgo.Capture)
	if err == nil {
		for i, d := range captures {
			name := d.Name()
			result = append(result, DeviceInfo{
				Index:     i,
				ID:        fmt.Sprintf("capture:%d", i),
				Name:      name,
				IsDefault: d.IsDefault > 0,
				Type:      "capture",
				IsVirtual: isVirtualDeviceName(name),
			})
		}
	}

	return result, nil
}

// DefaultDeviceNames 依据已枚举的设备列表给出「按需启动」场景下的展示名。
// 采集设备在无人订阅时不会打开，因此应用启动时需要一个人类可读的名字用于
// UI 与发现广播（启动后会被真实设备名覆盖）。
//   - 麦克风：默认采集设备名；
//   - 系统声：Windows 走默认播放设备的 WASAPI Loopback，故取默认播放设备名；
//     macOS/Linux 需要虚拟回环设备（BlackHole 等），有则显示其名称，
//     没有则如实显示「按需启用」（此时系统声捕获本身不可用）。
func DefaultDeviceNames(devices []DeviceInfo) (sysName, micName string) {
	sysName = "系统扬声器输出（按需启用）"
	micName = "麦克风输入（按需启用）"
	for _, d := range devices {
		if d.Type == "capture" && d.IsDefault && !d.IsVirtual && micName == "麦克风输入（按需启用）" {
			micName = d.Name
		}
	}
	if runtime.GOOS == "windows" {
		for _, d := range devices {
			if d.Type == "playback" && d.IsDefault {
				sysName = "系统输出 [" + d.Name + "]"
				break
			}
		}
		return sysName, micName
	}
	for _, d := range devices {
		if d.IsVirtual {
			sysName = "系统输出 [" + d.Name + "]"
			break
		}
	}
	return sysName, micName
}

// isVirtualDeviceName checks if the device name matches common virtual audio devices.
func isVirtualDeviceName(name string) bool {
	lower := strings.ToLower(name)
	keywords := []string{"cable", "virtual", "blackhole", "soundflower", "scream", "null", "loopback", "sink"}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
