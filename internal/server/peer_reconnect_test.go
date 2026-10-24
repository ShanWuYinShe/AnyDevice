package server

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"any_device/internal/discovery"

	"github.com/gorilla/websocket"
)

// waitFor 轮询等待条件成立，超时 Fatal。
func waitFor(t *testing.T, timeout time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", msg)
}

// statusOf 读指定对端的当前连接状态（测试辅助，直接访问私有 map）。
func statusOf(pm *PeerManager, peerID string) string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if m := pm.mappings[peerID]; m != nil {
		return m.ConnectionStatus
	}
	return ""
}

// TestPeerAutoReconnect 验证对端连接的三段生命周期：
//  1. 开启映射 → 自动建连；
//  2. 对端侧断开 → 状态 error 并按退避自动重连，对端恢复接受后自动恢复 connected；
//  3. 用户关闭映射 → disconnect，此后不再重连。
func TestPeerAutoReconnect(t *testing.T) {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var accepted int32 // 假对端累计接受的连接数（每次重连 +1）
	var mu sync.Mutex
	var peerConns []*websocket.Conn // 假对端持有的活跃连接（供测试主动断开）

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		atomic.AddInt32(&accepted, 1)
		mu.Lock()
		peerConns = append(peerConns, conn)
		mu.Unlock()
		// 保持读直到对端关闭（gorilla 自动回应本端的 ping）
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	addr := ln.Addr().String()
	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = ln
	srv.Start()
	defer srv.Close()

	dropPeerConns := func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range peerConns {
			_ = c.Close()
		}
		peerConns = nil
	}

	disc := discovery.NewService(0, "127.0.0.1", discovery.DeviceCapabilities{Audio: true, Mouse: true})
	hub := NewHub(nil)
	pm := NewPeerManager(disc, hub)
	// 测试注入短退避，加快断言节奏
	pm.reconnectInitial = 100 * time.Millisecond
	pm.reconnectMax = 400 * time.Millisecond

	peer, err := pm.AddManualPeer(addr, "假对端")
	if err != nil {
		t.Fatalf("添加对端失败: %v", err)
	}

	yes := true
	// ---- 阶段 1：开启鼠标映射 → 自动建连 ----
	if err := pm.SetMapping(peer.ID, nil, nil, &yes, nil); err != nil {
		t.Fatalf("开启映射失败: %v", err)
	}
	waitFor(t, 3*time.Second, "首连未在预期时间内建立", func() bool {
		return statusOf(pm, peer.ID) == "connected"
	})
	if got := atomic.LoadInt32(&accepted); got != 1 {
		t.Fatalf("首连后假对端应接受 1 条连接，实得 %d", got)
	}

	// ---- 阶段 2：对端侧断开连接 → error + 自动重连 → 恢复 connected ----
	// （httptest.Close 不会强关 hijacked 的 WebSocket 连接，这里直接断开
	//   假对端持有的活跃连接模拟对端进程消失；listener 保留供重连拨号）
	dropPeerConns()
	// 断开 → error/connecting → 重连成功是毫秒级竞速（退避仅 100ms），
	// 中间态可能在一次轮询间隙内就跳过；这里只断言最终恢复 connected
	// 且假对端确实收到了第二次拨号（accepted ≥2 证明真实断开重拨过）
	waitFor(t, 5*time.Second, "未自动重连成功", func() bool {
		return statusOf(pm, peer.ID) == "connected" && atomic.LoadInt32(&accepted) >= 2
	})

	// ---- 阶段 3：用户关闭映射 → disconnect 且不再重连 ----
	no := false
	if err := pm.SetMapping(peer.ID, nil, nil, &no, nil); err != nil {
		t.Fatalf("关闭映射失败: %v", err)
	}
	before := atomic.LoadInt32(&accepted)
	dropPeerConns()
	time.Sleep(600 * time.Millisecond) // 覆盖一个退避周期，确认没有新拨号
	if after := atomic.LoadInt32(&accepted); after != before {
		t.Fatalf("关闭映射后不应再有重连拨号：前 %d 后 %d", before, after)
	}
	if s := statusOf(pm, peer.ID); s != "disconnected" {
		t.Fatalf("关闭映射后状态应为 disconnected，实得 %s", s)
	}

	// 收尾：确认重连会话已清理（无泄漏）
	pm.mu.RLock()
	_, retryAlive := pm.retries[peer.ID]
	_, connAlive := pm.conns[peer.ID]
	pm.mu.RUnlock()
	if retryAlive || connAlive {
		t.Fatalf("关闭映射后会话应完全清理：retries 存在=%v, conns 存在=%v", retryAlive, connAlive)
	}
}
