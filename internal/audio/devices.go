package audio

import (
	"fmt"
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
