package util

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// 统一日志：同时写 stdout 与日志文件。
// Windows Wails 版为 GUI 子系统，stdout 不可见；引擎初始化失败等关键
// 信息写入日志文件后用户才能排查。
//
// 日志路径优先级：exe 同目录 → 用户配置目录 → 系统临时目录。
// 路径通过 LogFilePath() 暴露给 API/UI 显示，用户无需自行猜测位置。
var (
	logMu       sync.Mutex
	logWriter   io.Writer = os.Stdout
	logFilePath string
)

// BuildTime 构建时间标识（构建脚本用 -ldflags -X 注入），
// 用于 UI/API 确认运行的是哪个版本。
var BuildTime = "dev"

// InitLogFile 打开日志文件并接管输出。返回日志文件路径（失败返回空串）。
func InitLogFile() string {
	candidates := make([]string, 0, 3)
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "any_device.log"))
	}
	if cfgDir, err := os.UserConfigDir(); err == nil {
		candidates = append(candidates, filepath.Join(cfgDir, "AnyDevice", "any_device.log"))
	}
	candidates = append(candidates, filepath.Join(os.TempDir(), "any_device.log"))

	for _, path := range candidates {
		if dir := filepath.Dir(path); dir != "" {
			_ = os.MkdirAll(dir, 0o755)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			continue
		}
		logMu.Lock()
		logWriter = io.MultiWriter(os.Stdout, f)
		logFilePath = path
		logMu.Unlock()
		Logf("[系统] 日志文件: %s", path)
		return path
	}
	return ""
}

// LogFilePath 返回当前日志文件路径（未初始化时为空）。
func LogFilePath() string {
	logMu.Lock()
	defer logMu.Unlock()
	return logFilePath
}

// Logf 写一条日志（stdout + 日志文件，并发安全）。
func Logf(format string, args ...interface{}) {
	logMu.Lock()
	defer logMu.Unlock()
	fmt.Fprintf(logWriter, format+"\n", args...)
}
