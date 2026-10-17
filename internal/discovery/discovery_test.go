package discovery

import (
	"testing"
)

func TestDiscoveryService(t *testing.T) {
	s1 := NewService(8080, "192.168.1.100", DeviceCapabilities{
		Audio:   true,
		Mouse:   true,
		Control: true,
	})
	s1.SetName("Test-Host-1")

	if s1.ID() == "" {
		t.Fatal("expected non-empty node ID")
	}
	if s1.Name() != "Test-Host-1" {
		t.Fatalf("expected name Test-Host-1, got %s", s1.Name())
	}

	// Test Manual Peer addition
	p := s1.AddManualPeer("192.168.1.105:8080", "Office-PC")
	if p.Target != "192.168.1.105:8080" {
		t.Fatalf("expected target 192.168.1.105:8080, got %s", p.Target)
	}
	if p.Source != SourceManual {
		t.Fatalf("expected manual peer source, got %s", p.Source)
	}

	peers := s1.GetPeers()
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}

	// Test Inbound registration of local loopback (must be ignored!)
	localClient := s1.RegisterInboundClient("client-local", "127.0.0.1:54322", "Mozilla/5.0")
	if localClient != nil {
		t.Fatalf("expected local loopback client to NOT be registered, got %+v", localClient)
	}

	// Test Inbound registration of IPv6 localhost [::1] (must be ignored!)
	ipv6Client := s1.RegisterInboundClient("client-ipv6", "[::1]:54322", "Mozilla/5.0")
	if ipv6Client != nil {
		t.Fatalf("expected [::1] client to NOT be registered, got %+v", ipv6Client)
	}

	// Test Inbound registration of self IP (must be ignored!)
	selfIPClient := s1.RegisterInboundClient("client-self", s1.GetCurrentIP()+":54322", "Mozilla/5.0")
	if selfIPClient != nil {
		t.Fatalf("expected self IP client to NOT be registered, got %+v", selfIPClient)
	}

	// Test Manual addition of self IP / localhost (must return nil!)
	selfManual := s1.AddManualPeer("127.0.0.1:8080", "Self")
	if selfManual != nil {
		t.Fatalf("expected AddManualPeer for self 127.0.0.1 to return nil, got %+v", selfManual)
	}
	selfManual2 := s1.AddManualPeer(s1.GetCurrentIP()+":8080", "Self")
	if selfManual2 != nil {
		t.Fatalf("expected AddManualPeer for self IP to return nil, got %+v", selfManual2)
	}

	// Test Inbound registration of external device
	c := s1.RegisterInboundClient("client-99", "192.168.1.200:54321", "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X)")
	if c == nil || c.OS != "iOS" {
		t.Fatalf("expected external client registered with OS iOS, got %+v", c)
	}

	// Test RenamePeer（使用返回的稳定 ID）
	ok := s1.RenamePeer(c.ID, "我的备用苹果手机")
	if !ok {
		t.Fatal("expected RenamePeer to return true")
	}

	peers = s1.GetPeers()
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}
	foundRenamed := false
	for _, p := range peers {
		if p.ID == c.ID && p.Name == "我的备用苹果手机" {
			foundRenamed = true
		}
	}
	if !foundRenamed {
		t.Fatal("expected peer to display renamed alias 我的备用苹果手机")
	}

	// Test UpdateProvides
	s1.UpdateProvides(false, true, true, true, false, "扬声器", "USB 麦克风")
	caps := s1.GetProvides()
	if caps.SystemAudio || !caps.MicAudio || !caps.Mouse || !caps.Keyboard || caps.Control {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
	if caps.MicDevice != "USB 麦克风" {
		t.Fatalf("expected mic device 'USB 麦克风', got %s", caps.MicDevice)
	}
}

// TestAliasSurvivesBeacon 回归测试：设备备注绝不能被对端广播名覆盖
// （旧实现 listenLoop 每 2 秒用 beacon 刷新 Name，用户改名立刻被打回原样）。
func TestAliasSurvivesBeacon(t *testing.T) {
	s := NewService(8080, "192.168.1.100", DeviceCapabilities{})

	c := s.RegisterInboundClient("client-1", "192.168.1.200:5000", "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X)")
	if c == nil {
		t.Fatal("expected inbound client to be registered")
	}

	// 未设置备注：显示对端广播名
	s.upsertBeacon(Beacon{ID: c.ID, Name: "出厂名", OS: "ios", IP: "192.168.1.200", Port: 8080}, "192.168.1.200")
	for _, p := range s.GetPeers() {
		if p.ID == c.ID && p.Name != "出厂名" {
			t.Fatalf("expected announced name 出厂名, got %s", p.Name)
		}
	}

	// 设置备注后再收到 beacon：备注必须保留
	if !s.RenamePeer(c.ID, "客厅手机") {
		t.Fatal("expected RenamePeer to succeed")
	}
	s.upsertBeacon(Beacon{ID: c.ID, Name: "出厂名", OS: "ios", IP: "192.168.1.200", Port: 8080}, "192.168.1.200")
	found := false
	for _, p := range s.GetPeers() {
		if p.ID == c.ID {
			found = true
			if p.Name != "客厅手机" || p.Alias != "客厅手机" {
				t.Fatalf("alias overwritten by beacon: name=%s alias=%s", p.Name, p.Alias)
			}
		}
	}
	if !found {
		t.Fatal("expected peer to remain listed")
	}

	// 重启恢复场景：SetAliases 应能应用到已存在条目
	s2 := NewService(8080, "192.168.1.100", DeviceCapabilities{})
	s2.SetAliases(map[string]string{c.ID: "客厅手机"}, nil)
	s2.upsertBeacon(Beacon{ID: c.ID, Name: "出厂名", OS: "ios", IP: "192.168.1.200", Port: 8080}, "192.168.1.200")
	for _, p := range s2.GetPeers() {
		if p.ID == c.ID && p.Name != "客厅手机" {
			t.Fatalf("expected restored alias as display name, got %s", p.Name)
		}
	}
}

// TestInboundStableID 回归测试：同一设备刷新页面/重连后不得产生重复条目。
func TestInboundStableID(t *testing.T) {
	s := NewService(8080, "192.168.1.100", DeviceCapabilities{})

	c1 := s.RegisterInboundClient("client-a", "203.0.113.5:1111", "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X)")
	c2 := s.RegisterInboundClient("client-b", "203.0.113.5:2222", "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X)")
	if c1 == nil || c2 == nil {
		t.Fatal("expected both registrations to succeed")
	}
	if c1.ID != c2.ID {
		t.Fatalf("expected stable inbound ID across reconnects, got %s vs %s", c1.ID, c2.ID)
	}
	if len(s.GetPeers()) != 1 {
		t.Fatalf("expected 1 peer for same device, got %d", len(s.GetPeers()))
	}

	// 断开校准：连接集合为空时该设备必须立即离线
	s.ReconcileInbound(map[string]bool{})
	for _, p := range s.peersList() {
		if p.ID == c1.ID && p.Online {
			t.Fatal("expected peer offline after reconcile with empty live set")
		}
	}
	// 重新上线
	s.ReconcileInbound(map[string]bool{"203.0.113.5": true})
	for _, p := range s.peersList() {
		if p.ID == c1.ID && !p.Online {
			t.Fatal("expected peer online after reconcile with live IP")
		}
	}
}

// peersList 直接读取内部条目（含被过滤项），仅测试用。
func (s *Service) peersList() []*DiscoveredPeer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*DiscoveredPeer, 0, len(s.peers))
	for _, p := range s.peers {
		out = append(out, p)
	}
	return out
}

func TestDefaultNodeName(t *testing.T) {
	name := DefaultNodeName()
	if name == "" {
		t.Fatal("expected non-empty default node name")
	}
}

// —— IP 归并去重回归测试：同一台设备只允许一个条目 ——

// beacon 到达时，同 IP 的入站条目必须被归并升级，而不是新增一条
func TestDedupeBeaconMergesInbound(t *testing.T) {
	s := NewService(8080, "192.168.1.100", DeviceCapabilities{})

	reg := s.RegisterInboundClient("client-1", "192.168.1.200:5000", "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X)")
	if reg == nil {
		t.Fatal("expected inbound registration")
	}
	s.upsertBeacon(Beacon{
		ID: "nodeA", Name: "客厅 Mac mini", OS: "darwin", IP: "192.168.1.200", Port: 8080,
		Provides: DeviceCapabilities{SystemAudio: true, Mouse: true, Control: true, Audio: true},
	}, "192.168.1.200")

	peers := s.GetPeers()
	if len(peers) != 1 {
		t.Fatalf("expected 1 deduped peer, got %d: %+v", len(peers), peers)
	}
	p := peers[0]
	if p.ID != reg.ID {
		t.Fatalf("expected stable ID %s, got %s", reg.ID, p.ID)
	}
	if p.Source != SourceBeacon {
		t.Fatalf("expected upgraded source beacon, got %s", p.Source)
	}
	if !p.Provides.SystemAudio {
		t.Fatal("expected provides upgraded from beacon")
	}
}

// 手动添加的对端被 beacon 发现后合并为一条，且用户别名保留
func TestDedupeManualMergeKeepsAlias(t *testing.T) {
	s := NewService(8080, "192.168.1.100", DeviceCapabilities{})

	manual := s.AddManualPeer("192.168.1.200:8080", "书房台式机")
	if manual == nil {
		t.Fatal("expected manual peer")
	}
	s.upsertBeacon(Beacon{
		ID: "nodeB", Name: "出厂主机名", OS: "windows", IP: "192.168.1.200", Port: 8080,
		Provides: DeviceCapabilities{SystemAudio: true, Control: true},
	}, "192.168.1.200")

	peers := s.GetPeers()
	if len(peers) != 1 {
		t.Fatalf("expected 1 deduped peer, got %d", len(peers))
	}
	p := peers[0]
	if p.Name != "书房台式机" || p.Alias != "书房台式机" {
		t.Fatalf("expected manual alias kept, got name=%s alias=%s", p.Name, p.Alias)
	}
	if !p.IsManual {
		t.Fatal("manual peer must not lose manual semantics")
	}
	if !p.Provides.SystemAudio {
		t.Fatal("expected provides upgraded from beacon")
	}
}

// 对端重启后节点 ID 变化：同 IP 仍归并为一条，条目 ID 保持既有值
func TestDedupeSameIPNewNodeID(t *testing.T) {
	s := NewService(8080, "192.168.1.100", DeviceCapabilities{})

	s.upsertBeacon(Beacon{ID: "nodeA", Name: "旧名", OS: "darwin", IP: "192.168.1.200", Port: 8080}, "192.168.1.200")
	s.upsertBeacon(Beacon{ID: "nodeB", Name: "新名", OS: "darwin", IP: "192.168.1.200", Port: 8080}, "192.168.1.200")

	peers := s.GetPeers()
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer after node restart, got %d", len(peers))
	}
	if peers[0].ID != "nodeA" {
		t.Fatalf("expected existing entry ID kept, got %s", peers[0].ID)
	}
	if peers[0].Name != "新名" {
		t.Fatalf("expected beacon data refreshed, got %s", peers[0].Name)
	}
}

// beacon 之后同 IP 的入站连接不再新建条目，只刷新在线状态
func TestDedupeInboundAfterBeacon(t *testing.T) {
	s := NewService(8080, "192.168.1.100", DeviceCapabilities{})

	s.upsertBeacon(Beacon{ID: "nodeA", Name: "Mac", OS: "darwin", IP: "192.168.1.200", Port: 8080}, "192.168.1.200")
	reg := s.RegisterInboundClient("client-9", "192.168.1.200:6000", "Mozilla/5.0 (Macintosh)")
	if reg == nil {
		t.Fatal("expected registration reuse")
	}
	if reg.ID != "nodeA" {
		t.Fatalf("expected merged into beacon entry, got %s", reg.ID)
	}
	if len(s.GetPeers()) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(s.GetPeers()))
	}
}

// 别名按 IP 恢复：设备重启换 ID 后，备注仍按 IP 生效
func TestAliasRestoredByIP(t *testing.T) {
	s := NewService(8080, "192.168.1.100", DeviceCapabilities{})
	s.SetAliases(map[string]string{}, map[string]string{"192.168.1.200": "书房台式机"})

	s.upsertBeacon(Beacon{ID: "brandNewID", Name: "出厂名", OS: "windows", IP: "192.168.1.200", Port: 8080}, "192.168.1.200")

	peers := s.GetPeers()
	if len(peers) != 1 || peers[0].Name != "书房台式机" {
		t.Fatalf("expected alias restored by IP, got %+v", peers)
	}
}

// 连接集合按 IP 保活：合并后的条目有存活连接时不被心跳超时误删语义影响
func TestReconcileByIP(t *testing.T) {
	s := NewService(8080, "192.168.1.100", DeviceCapabilities{})

	s.upsertBeacon(Beacon{ID: "nodeA", Name: "Mac", OS: "darwin", IP: "192.168.1.200", Port: 8080}, "192.168.1.200")

	// 连接存活：保活刷新
	s.ReconcileInbound(map[string]bool{"192.168.1.200": true})
	if p := s.peers["nodeA"]; !p.Online {
		t.Fatal("expected live connection to keep peer online")
	}

	// 连接断开：beacon 条目离线交给广播心跳管理，不在此置离线
	s.ReconcileInbound(map[string]bool{})
	if p := s.peers["nodeA"]; !p.Online {
		t.Fatal("beacon entry offline must be managed by beacon heartbeat, not reconcile")
	}

	// 纯入站条目：连接断开立即离线
	s.RegisterInboundClient("c", "203.0.113.9:7000", "Mozilla/5.0 (iPhone)")
	s.ReconcileInbound(map[string]bool{})
	if p := s.peers["inbound-203.0.113.9"]; p.Online {
		t.Fatal("expected inbound entry offline when connection set empty")
	}
}
