package util

import (
	"sync"
	"testing"
	"time"
)

// TestIsLocalIPConcurrentNetCache 并发「失效/刷新网卡缓存」与「IsLocalIP(v6)」
// 查询：回归保护 netV6 的读写同步（此前直接遍历 netV6，与缓存刷新构成数据竞争）。
func TestIsLocalIPConcurrentNetCache(t *testing.T) {
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				InvalidateNetCache()
				GetAllIPs()
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				IsLocalIP("fe80::1%en0")
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}
