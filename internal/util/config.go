package util

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// NodeConfig 是需要跨重启持久化的本机发现配置：
// Name 为本机在局域网中显示的名称，Aliases 为按对端 ID 的本地备注。
// 此前两者只存在内存里，用户改完名一重启就丢——这是“设备改名没解决”的另一半根因。
type NodeConfig struct {
	Name        string            `json:"name,omitempty"`
	Aliases     map[string]string `json:"aliases,omitempty"`
	AliasesByIP map[string]string `json:"aliases_by_ip,omitempty"`
}

var (
	configMu           sync.Mutex
	configPathOverride string
)

// SetConfigPathForTest 仅供单元测试重定向配置路径；传空串恢复默认路径。
func SetConfigPathForTest(path string) {
	configMu.Lock()
	defer configMu.Unlock()
	configPathOverride = path
}

// NodeConfigPath 返回配置文件路径；默认写入用户配置目录（macOS ~/Library/Application Support、
// Windows %AppData%、Linux ~/.config 下的 AnyDevice/config.json）。
// 可用环境变量 ANY_DEVICE_CONFIG 指定任意路径（便携/受限环境场景）。
func NodeConfigPath() (string, error) {
	configMu.Lock()
	override := configPathOverride
	configMu.Unlock()
	if override != "" {
		return override, nil
	}
	if env := os.Getenv("ANY_DEVICE_CONFIG"); env != "" {
		return env, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "AnyDevice", "config.json"), nil
}

// LoadNodeConfig 读取持久化配置；文件不存在或损坏时返回零值配置
// （尽力而为，不阻断启动）。
func LoadNodeConfig() NodeConfig {
	path, err := NodeConfigPath()
	if err != nil {
		return NodeConfig{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return NodeConfig{}
	}
	var cfg NodeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return NodeConfig{}
	}
	if cfg.Aliases == nil {
		cfg.Aliases = map[string]string{}
	}
	if cfg.AliasesByIP == nil {
		cfg.AliasesByIP = map[string]string{}
	}
	return cfg
}

// SaveNodeConfig 原子化写入配置（先写临时文件再 rename），避免写一半被读到。
func SaveNodeConfig(cfg NodeConfig) error {
	path, err := NodeConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	// CreateTemp 保证并发写各用各的临时文件（固定 .tmp 名会互相覆盖踩踏），
	// 成功 rename 后 defer 的 Remove 是无害 no-op
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// CreateTemp 固定 0600；配置不含敏感数据，保持原有的 0644 语义
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return nil
}
