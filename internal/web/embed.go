package web

import (
	"embed"
	"io/fs"
	"net/http"
)

// dist 由 frontend/ 的 Svelte + Vite 构建产出（vite outDir 指向本目录），
// 构建命令见 scripts/build.sh：先构建前端再 go build。
// 仓库仅提交 .gitkeep 占位，保证 go:embed 在全新检出时可编译。
//
//go:embed all:dist
var distFS embed.FS

// GetFileSystem returns an http.FileSystem serving the embedded static assets.
func GetFileSystem() (http.FileSystem, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	return http.FS(sub), nil
}
