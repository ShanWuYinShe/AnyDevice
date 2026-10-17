package ui

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// clientCount 返回当前在线页面连接数（由 main 注入 hub.ClientCount）。
var clientCount func() int

// SetClientCounter 注入页面连接计数器（main 启动时调用）。
func SetClientCounter(fn func() int) {
	clientCount = fn
}

// signalChan 创建并监听中断/终止信号。
func signalChan() chan os.Signal {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	return sigChan
}

// waitSignal 阻塞直到收到中断/终止信号。
func waitSignal(sigChan chan os.Signal) {
	<-sigChan
}

// logf 统一 UI 日志前缀输出。
func logf(format string, args ...interface{}) {
	fmt.Printf("[UI] "+format+"\n", args...)
}
