package util

import (
	"net"
	"testing"
)

// TestIsLocalIPv6 验证 IsLocalIP 的本机 IPv6 判断：此前 GetAllIPs 只收集
// IPv4，IPv6 直连访问时本机地址会被误判为外部对端。
func TestIsLocalIPv6(t *testing.T) {
	// 固定行为
	fixed := []struct {
		ip   string
		want bool
	}{
		{"::1", true},
		{"::", true},               // unspecified
		{"192.0.2.1", false},       // TEST-NET，非本机
		{"fe80::dead:beef", false}, // 假链路本地地址，本机没有
	}
	for _, tc := range fixed {
		if got := IsLocalIP(tc.ip); got != tc.want {
			t.Errorf("IsLocalIP(%q) = %v, want %v", tc.ip, got, tc.want)
		}
	}

	// 本机真实 IPv6 地址（链路本地/全局任一）必须判为本机：
	// 先触发缓存刷新（IsLocalIP 内部走 GetAllIPs → refreshNetCacheLocked），
	// 再从系统接口枚举一个真实 v6 地址断言
	IsLocalIP("::1") // 触发缓存
	var found []string
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Skipf("枚举接口失败: %v", err)
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipn, ok := addr.(*net.IPNet); ok {
				s := ipn.IP.String()
				if len(s) > 0 && !ipn.IP.IsLoopback() && isV6String(s) {
					found = append(found, s)
				}
			}
		}
	}
	if len(found) == 0 {
		t.Skip("本机无 IPv6 地址，跳过真实地址断言")
	}
	for _, s := range found {
		if !IsLocalIP(s) {
			t.Errorf("本机 IPv6 地址 %s 被误判为外部", s)
		}
		// 带 zone 的形式（zone 名任意）也应判为本机：zone 归一化后比较
		if !IsLocalIP(s + "%fakezone") {
			t.Errorf("带 zone 的本机 IPv6 %s%%fakezone 被误判为外部", s)
		}
	}
}

func isV6String(s string) bool {
	for _, c := range s {
		if c == ':' {
			return true
		}
	}
	return false
}
