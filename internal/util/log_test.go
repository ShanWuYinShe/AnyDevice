package util

import (
	"bytes"
	"errors"
	"testing"
)

type errWriter struct{ err error }

func (w errWriter) Write([]byte) (int, error) { return 0, w.err }

// TestFileThenStdoutWritesFileWhenStdoutFails：Windows GUI 子系统下 stdout 是
// 无效句柄，日志文件必须照常收到内容（io.MultiWriter 会在此短路，文件日志全丢）。
func TestFileThenStdoutWritesFileWhenStdoutFails(t *testing.T) {
	var file bytes.Buffer
	w := fileThenStdout{file: &file, stdout: errWriter{err: errors.New("stdout closed")}}
	n, err := w.Write([]byte("hello"))
	if err != nil || n != len("hello") {
		t.Fatalf("写日志返回 (%d, %v)，期望 (5, nil)", n, err)
	}
	if file.String() != "hello" {
		t.Fatalf("日志文件应收到 hello，实际 %q", file.String())
	}
}

// TestFileThenStdoutPropagatesFileError：日志文件写失败必须上报（调用方可感知），
// 但 stdout 错误应被忽略。
func TestFileThenStdoutPropagatesFileError(t *testing.T) {
	w := fileThenStdout{file: errWriter{err: errors.New("disk full")}, stdout: &bytes.Buffer{}}
	if _, err := w.Write([]byte("x")); err == nil {
		t.Fatal("日志文件写失败应返回错误")
	}
}

// TestNetCacheIPv6NoDuplicates：v6 地址只枚举一次并去重（旧实现在 IPv4 地址
// 双层循环里反复枚举接口，同一地址会重复入表）。
func TestNetCacheIPv6NoDuplicates(t *testing.T) {
	netMu.Lock()
	refreshNetCacheLocked()
	v6 := append([]string(nil), netV6...)
	netMu.Unlock()

	seen := make(map[string]bool, len(v6))
	for _, s := range v6 {
		if seen[s] {
			t.Fatalf("netV6 存在重复地址 %q", s)
		}
		seen[s] = true
	}
}
