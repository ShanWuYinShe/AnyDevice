package util

import (
	"path/filepath"
	"testing"
)

// TestNodeConfigRoundTrip 校验持久化配置的写入与恢复（改名/备注跨重启不丢失）。
func TestNodeConfigRoundTrip(t *testing.T) {
	SetConfigPathForTest(filepath.Join(t.TempDir(), "config.json"))
	defer SetConfigPathForTest("")

	// 未写入前：返回零值配置
	if got := LoadNodeConfig(); got.Name != "" || len(got.Aliases) != 0 {
		t.Fatalf("expected empty config before save, got %+v", got)
	}

	cfg := NodeConfig{
		Name:    "我的主机",
		Aliases: map[string]string{"abc123": "客厅设备", "inbound-192.168.1.7": "我的手机"},
	}
	if err := SaveNodeConfig(cfg); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	got := LoadNodeConfig()
	if got.Name != "我的主机" {
		t.Fatalf("expected name 我的主机, got %q", got.Name)
	}
	if got.Aliases["abc123"] != "客厅设备" || got.Aliases["inbound-192.168.1.7"] != "我的手机" {
		t.Fatalf("aliases not restored: %+v", got.Aliases)
	}
}
