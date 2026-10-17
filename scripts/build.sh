#!/usr/bin/env bash
set -e

# Wails 构建统一标签与 darwin 链接参数（v2.16 darwin 前端依赖
# UniformTypeIdentifiers framework，需显式传给外部链接器）
WAILS_TAGS="wails,desktop,production"
MAC_EXTLD="-framework UniformTypeIdentifiers"
# 构建时间注入 UI/日志（确认运行版本、排查旧产物问题）
BUILD_TIME=$(date +%Y%m%d-%H%M)
LDFLAGS_COMMON="-s -w -X any_device/internal/util.BuildTime=${BUILD_TIME}"

echo ">>> 正在构建前端 (Svelte + Vite, 产物嵌入 internal/web/dist)..."
cd frontend
if [ ! -d node_modules ]; then
    bun install
fi
bun run check
bun run build
cd ..

# Wails 原生窗口版（默认桌面形态；Windows 版无控制台黑窗）
echo ">>> 正在编译 macOS Wails 桌面版 (arm64, 构建 $BUILD_TIME)..."
go build -tags "$WAILS_TAGS" -ldflags "$LDFLAGS_COMMON -extldflags '$MAC_EXTLD'" -o bin/any_device_darwin_arm64 main.go
codesign -s - -f bin/any_device_darwin_arm64 >/dev/null 2>&1 || true

# Windows Wails 版（交叉编译：Wails 的 Windows 前端为纯 Go，音频包走 mingw cgo）
if command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1; then
    echo ">>> 正在交叉编译 Windows Wails 桌面版 (x86_64, 无控制台窗口)..."
    CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc GOOS=windows GOARCH=amd64 \
        go build -tags "$WAILS_TAGS" -ldflags "$LDFLAGS_COMMON -H=windowsgui" -o bin/any_device_windows_amd64.exe main.go
    # Portable 回退版（浏览器 --app 独立窗口壳，控制台可见便于排查）
    echo ">>> 正在交叉编译 Windows Portable 版 (浏览器窗口回退)..."
    CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc GOOS=windows GOARCH=amd64 \
        go build -ldflags "$LDFLAGS_COMMON" -o bin/any_device_windows_portable.exe main.go
fi

echo ">>> 编译完成！产物位于 bin/ 目录："
ls -lh bin/
