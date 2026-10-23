package server

import (
	"net/http"
	"testing"

	"any_device/internal/util"
)

// isAllowedOrigin 的行为锁定测试：
// WS 连上即可订阅系统声音与键鼠事件流，Origin 白名单是安全边界。
func TestIsAllowedOrigin(t *testing.T) {
	mkReq := func(origin string) *http.Request {
		r, _ := http.NewRequest("GET", "/ws", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}

	cases := []struct {
		name   string
		origin string
		want   bool
	}{
		{"无 Origin（对端 dial / curl）", "", true},
		{"本机回环 IPv4", "http://127.0.0.1:8080", true},
		{"本机回环 IPv6", "http://[::1]:8080", true},
		{"localhost", "http://localhost:8080", true},
		{"Wails macOS 窗口", "wails://wails", true},
		{"Wails Windows 窗口", "http://wails.localhost", true},
		{"跨源恶意页面", "http://evil.example.com", false},
		{"file:// 的 null origin", "null", false},
		{"公网 TEST-NET 地址", "https://192.0.2.1:8080", false},
		{"自定义 scheme 伪装", "chrome-extension://abcdef", false},
	}
	for _, tc := range cases {
		if got := isAllowedOrigin(mkReq(tc.origin)); got != tc.want {
			t.Errorf("%s: Origin=%q 期望 %v 实得 %v", tc.name, tc.origin, tc.want, got)
		}
	}

	// 本机任一网卡地址的 Origin 必须放行（UI 同源页面：手机扫码打开的场景）
	for _, info := range util.GetAllIPs() {
		if info.IP == "" {
			continue
		}
		origin := "http://" + info.IP + ":8080"
		if !isAllowedOrigin(mkReq(origin)) {
			t.Errorf("本机网卡地址 %s 的同源 Origin=%q 被误拒", info.IP, origin)
		}
	}
}
