package server

import (
	"os"
	"testing"
)

// TestMain 统一为本包单测禁用 discovery 的真实 UDP 绑定/广播：
// NewServer 内部会 disc.Start()，默认行为会绑定真实 DiscoveryPort 并向
// 广播/组播地址发 beacon——与开发机上正在运行的正式实例互相干扰
// （listenLoop 收到未知包重试刷日志），同一测试二进制内多个 NewServer
// 还会互抢端口产生重试 goroutine。单测只测 HTTP/peer_manager 逻辑，
// 手动对端（AddManualPeer）不依赖 discovery 网络循环。
func TestMain(m *testing.M) {
	os.Setenv("ANY_DEVICE_DISCOVERY_OFF", "1")
	os.Exit(m.Run())
}
