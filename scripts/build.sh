#!/usr/bin/env bash
set -e

echo ">>> 正在构建前端 (Svelte + Vite, 产物嵌入 internal/web/dist)..."
cd frontend
if [ ! -d node_modules ]; then
    bun install
fi
bun run check
bun run build
cd ..

mkdir -p bin

echo ">>> 正在编译 macOS (arm64)..."
go build -ldflags "-s -w" -o bin/any_device_darwin_arm64 main.go
codesign -s - -f bin/any_device_darwin_arm64 >/dev/null 2>&1 || true

if command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1; then
    echo ">>> 正在交叉编译 Windows (x86_64)..."
    CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc GOOS=windows GOARCH=amd64 \
        go build -ldflags "-s -w" -o bin/any_device_windows_amd64.exe main.go
fi

echo ">>> 编译完成！产物位于 bin/ 目录："
ls -lh bin/
