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
// 信息写入日志文件后用户才能排查（日志优先落 exe 同目录，无权限回退临时目录）。
var (
	logMu     sync.Mutex
	logWriter io.Writer = os.Stdout
)

// InitLogFile 打开日志文件并接管输出。返回日志文件路径（失败返回空串）。
func InitLogFile() string {
	path := ""
	if exe, err := os.Executable(); err == nil {
		path = filepath.Join(filepath.Dir(exe), "any_device.log")
	}
	var f *os.File
	var err error
	if path != "" {
		f, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	}
	if f == nil || err != nil {
		path = filepath.Join(os.TempDir(), "any_device.log")
		f, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return ""
		}
	}
	logMu.Lock()
	logWriter = io.MultiWriter(os.Stdout, f)
	logMu.Unlock()
	Logf("[系统] 日志文件: %s", path)
	return path
}

// Logf 写一条日志（stdout + 日志文件，并发安全）。
func Logf(format string, args ...interface{}) {
	logMu.Lock()
	defer logMu.Unlock()
	fmt.Fprintf(logWriter, format+"\n", args...)
}
