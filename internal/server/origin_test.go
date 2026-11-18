package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"any_device/internal/util"
)

// TestOriginAllowed 覆盖 WS 与写接口共用的同源白名单。
// 重点回归：DNS rebinding 修复后，任意普通域名（即使它解析到本机地址）
// 必须被拒绝——不再做 LookupHost 比对；局域网保留域名后缀放行。
func TestOriginAllowed(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		want   bool
	}{
		{"无 Origin 非浏览器客户端", "", true},
		{"Wails 桌面窗口 macOS", "wails://wails", true},
		{"Wails 桌面窗口 Windows", "http://wails.localhost", true},
		{"Wails 子域名后缀", "http://foo.wails.localhost", true},
		{"localhost", "http://localhost:8080", true},
		{"回环地址", "http://127.0.0.1:8080", true},
		{"IPv6 回环", "http://[::1]:8080", true},
		{"mDNS 局域网保留域名", "http://device.local:8080", true},
		{"home.arpa 局域网保留域名", "http://mydevice.home.arpa:8080", true},
		// 拒绝侧：跨源攻击面
		{"外部 IP 字面量", "http://192.0.2.1:8080", false},
		{"普通公网域名（即使解析到本机也不放行）", "http://evil.com", false},
		{"普通域名子域", "http://sub.evil.example", false},
		{"非白名单 scheme", "ftp://localhost", false},
		{"自定义 scheme 伪装", "chrome-extension://abcdef", false},
		{"空 host", "http://", false},
		{"opaque origin null", "null", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/self", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if got := isAllowedOrigin(req); got != tc.want {
				t.Fatalf("Origin %q: isAllowedOrigin = %v, want %v", tc.origin, got, tc.want)
			}
		})
	}

	// 本机真实网卡 IP 字面量必须放行（手机浏览器同源访问的正常形态）
	for _, info := range util.GetAllIPs() {
		if info.IP == "" {
			continue
		}
		req := httptest.NewRequest(http.MethodPost, "/api/self", nil)
		req.Header.Set("Origin", "http://"+info.IP+":8080")
		if !isAllowedOrigin(req) {
			t.Fatalf("本机网卡地址 %s 被误拒", info.IP)
		}
		return
	}
	t.Log("本机无可用网卡地址，跳过本机 IP 放行子用例")
}

// TestWriteEndpointsCSRFGuard 回归写接口的 Origin 校验与 POST 限定：
// 恶意网页的 no-cors fetch（Origin=evil）必须 403；无 Origin 的合法
// 客户端（curl/前端同源）不受影响；GET 打写接口必须 405。
func TestWriteEndpointsCSRFGuard(t *testing.T) {
	hub := NewHub(func(a, b, m, k int) {})
	s := NewServer(8080, "127.0.0.1", hub, nil, nil, nil, nil)
	handler := s.SetupRoutes()

	cases := []struct {
		name   string
		method string
		path   string
		origin string
		want   int
	}{
		{"map 接口拒绝恶意跨源", http.MethodPost, "/api/peers/map", "http://evil.com", http.StatusForbidden},
		{"add 接口拒绝恶意跨源", http.MethodPost, "/api/peers/add", "http://evil.com", http.StatusForbidden},
		{"self 写拒绝恶意跨源", http.MethodPost, "/api/self", "http://evil.com", http.StatusForbidden},
		{"interfaces/select 拒绝恶意跨源", http.MethodPost, "/api/interfaces/select?ip=192.0.2.1", "http://evil.com", http.StatusForbidden},
		{"client-report 拒绝恶意跨源", http.MethodPost, "/api/client-report", "http://evil.com", http.StatusForbidden},
		{"add 接口无 Origin 放行（进到参数校验 400 而非 403）", http.MethodPost, "/api/peers/add", "", http.StatusBadRequest},
		{"map 接口无 Origin 放行（空 body 进到参数校验 400）", http.MethodPost, "/api/peers/map", "", http.StatusBadRequest},
		{"add 接口 GET 必须 405", http.MethodGet, "/api/peers/add", "", http.StatusMethodNotAllowed},
		{"map 接口 GET 必须 405", http.MethodGet, "/api/peers/map", "", http.StatusMethodNotAllowed},
		{"rename 接口 GET 必须 405", http.MethodGet, "/api/peers/rename", "", http.StatusMethodNotAllowed},
		// 读接口不设 Origin 门：跨源读本就被浏览器 SOP 拦截，服务端保持开放
		{"读接口 peers 不受影响", http.MethodGet, "/api/peers", "http://evil.com", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("%s %s Origin=%q: got %d, want %d", tc.method, tc.path, tc.origin, rec.Code, tc.want)
			}
		})
	}
}
