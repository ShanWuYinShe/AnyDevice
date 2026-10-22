#!/usr/bin/env bash
set -e

# 构建策略：
#   - macOS: Wails 原生窗口（WKWebView），打包为 bin/AnyDevice.app。
#     必须是 .app 包：裸二进制没有 Info.plist，ATS 本地网络例外与麦克风
#     用途声明不会生效，窗口内 WebSocket/麦克风都会失败。
#   - Windows: Wails 原生窗口 exe（无控制台黑窗）+ Portable 浏览器壳回退版。
# 前端产物嵌入 internal/web/dist，由 go:embed 打进二进制。
WAILS_TAGS="wails,desktop,production"
BUILD_TIME=$(date +%Y%m%d-%H%M)
LDFLAGS_COMMON="-s -w -X any_device/internal/util.BuildTime=${BUILD_TIME}"
# Wails v2.16 的 darwin 前端引用 UniformTypeIdentifiers framework，
# 需向外部链接器显式传递
MAC_EXTLD="-framework UniformTypeIdentifiers"
VERSION=$(sed -n 's/^const Version = "\(.*\)"$/\1/p' main.go)

echo ">>> 正在构建前端 (Svelte + Vite, 产物嵌入 internal/web/dist)..."
cd frontend
if [ ! -d node_modules ]; then
    bun install
fi
bun run check
bun run build
cd ..

echo ">>> 正在编译 macOS 桌面版 (arm64, Wails 原生窗口, 版本 ${VERSION}, 构建 ${BUILD_TIME})..."
MAC_APP="bin/AnyDevice.app"
rm -rf "$MAC_APP"
mkdir -p "$MAC_APP/Contents/MacOS"
go build -tags "$WAILS_TAGS" -ldflags "$LDFLAGS_COMMON -extldflags '$MAC_EXTLD'" \
    -o "$MAC_APP/Contents/MacOS/any_device" main.go
# Info.plist 由仓库根目录模板生成（注入版本号），再对整个 bundle 做 ad-hoc 签名
sed "s/__VERSION__/${VERSION}/g" Info.plist > "$MAC_APP/Contents/Info.plist"
# 静态链接的 libopus 为 BSD-3-Clause：二进制分发需随附许可文本（必须在签名前放入包内）
mkdir -p "$MAC_APP/Contents/Resources/third_party"
cp third_party/libopus/COPYING "$MAC_APP/Contents/Resources/third_party/libopus-COPYING"
# 应用图标 AppIcon.icns
if [ -f "build/AppIcon.icns" ]; then
    cp "build/AppIcon.icns" "$MAC_APP/Contents/Resources/AppIcon.icns"
fi
codesign -s - -f "$MAC_APP" >/dev/null 2>&1 || true

if command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1; then
    # Windows Wails 版（Wails 的 Windows 前端为纯 Go，音频包走 mingw cgo）
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
