package server

import (
	"testing"
	"time"
)

func boolPtr(v bool) *bool { return &v }

// waitSubs 轮询等待订阅计数满足条件。
func waitSubs(t *testing.T, hub *Hub, want [4]int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sys, mic, mouse, kb := hub.SubscriberCounts()
		if [4]int{sys, mic, mouse, kb} == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	sys, mic, mouse, kb := hub.SubscriberCounts()
	t.Fatalf("订阅计数未达期望 %v，实际 %v", want, [4]int{sys, mic, mouse, kb})
}

// TestLegacySubsDeferredForRemoteClient：远端新连接不能「连接即全订阅」——
// 否则每次连接都会立刻启动音频设备/安装键鼠钩子并跑满空闲宽限（5–8s）。
// 旧版对端在宽限期后仍应兜底全开（兼容）。
func TestLegacySubsDeferredForRemoteClient(t *testing.T) {
	hub := NewHub(nil)
	go hub.Run()
	client := NewClient(hub, nil, "192.168.1.50:5555", "legacy-agent", false)
	hub.register <- client

	// 宽限期内：不订阅任何流（捕获引擎/钩子不得被唤醒）
	time.Sleep(100 * time.Millisecond)
	if sys, mic, mouse, kb := hub.SubscriberCounts(); sys+mic+mouse+kb != 0 {
		t.Fatalf("宽限期内不应订阅任何流，实际 sys=%d mic=%d mouse=%d kb=%d", sys, mic, mouse, kb)
	}

	// 宽限期后：旧版对端兜底全开
	waitSubs(t, hub, [4]int{1, 1, 1, 1})
}

// TestExplicitMappingCancelsLegacyDefaults：宽限期内收到显式 set_mapping，
// 以显式声明为准，宽限到点不得再兜底全开。
func TestExplicitMappingCancelsLegacyDefaults(t *testing.T) {
	hub := NewHub(nil)
	go hub.Run()
	client := NewClient(hub, nil, "192.168.1.51:5555", "modern-agent", false)
	hub.register <- client
	// 只映射麦克风（显式新协议订阅）
	client.applyMapping(nil, boolPtr(false), boolPtr(true), boolPtr(false), boolPtr(false), nil)

	time.Sleep(legacySubsGrace + 300*time.Millisecond)
	if sys, mic, mouse, kb := hub.SubscriberCounts(); sys != 0 || mic != 1 || mouse != 0 || kb != 0 {
		t.Fatalf("显式订阅应被尊重，实际 sys=%d mic=%d mouse=%d kb=%d", sys, mic, mouse, kb)
	}
}

// TestLocalClientNeverGetsLegacyDefaults：本机 UI 客户端永远不应被兜底全开。
func TestLocalClientNeverGetsLegacyDefaults(t *testing.T) {
	hub := NewHub(nil)
	go hub.Run()
	client := NewClient(hub, nil, "127.0.0.1:5555", "local-ui", true)
	hub.register <- client

	time.Sleep(legacySubsGrace + 300*time.Millisecond)
	if sys, mic, mouse, kb := hub.SubscriberCounts(); sys+mic+mouse+kb != 0 {
		t.Fatalf("本机 UI 不应有默认订阅，实际 sys=%d mic=%d mouse=%d kb=%d", sys, mic, mouse, kb)
	}
}

// TestUnregisterStopsLegacyTimer：连接在宽限期内断开后，定时器不得再唤醒采集。
func TestUnregisterStopsLegacyTimer(t *testing.T) {
	hub := NewHub(nil)
	go hub.Run()
	client := NewClient(hub, nil, "192.168.1.52:5555", "gone-agent", false)
	hub.register <- client
	time.Sleep(50 * time.Millisecond)
	hub.unregister <- client

	time.Sleep(legacySubsGrace + 300*time.Millisecond)
	if sys, mic, mouse, kb := hub.SubscriberCounts(); sys+mic+mouse+kb != 0 {
		t.Fatalf("已注销连接不应被兜底订阅，实际 sys=%d mic=%d mouse=%d kb=%d", sys, mic, mouse, kb)
	}
}
