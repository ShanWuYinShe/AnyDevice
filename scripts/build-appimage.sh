#!/usr/bin/env bash
# 构建 Linux AppImage 分发包。
#
# 为什么用 AppImage：Linux 各发行版的 GTK/WebKitGTK 版本差异大，本项目
# 又无法全静态链接（见 third_party/libopus/README 与 opus_encoder.go 的说明），
# AppImage 把运行库打包进单文件、运行时用挂载方式提供，用户下载即可双击运行。
#
# 依赖（构建机）：libgtk-3-dev libwebkit2gtk-4.0-dev libopus-dev pkg-config
# 用法：ARCH=amd64 ./scripts/build-appimage.sh   （或 ARCH=arm64）
set -euo pipefail

ARCH="${ARCH:-amd64}"
VERSION=$(sed -n 's/^const Version = "\(.*\)"$/\1/p' main.go)
BUILD_TIME=$(date +%Y%m%d-%H%M)
APP_NAME="AnyDevice"
APPDIR="build/AppDir-${ARCH}"
OUT="bin/${APP_NAME}-${VERSION}-${ARCH}.AppImage"

echo ">>> 构建 Linux ${ARCH} 二进制（版本 ${VERSION}）..."
mkdir -p bin
CGO_ENABLED=1 GOOS=linux GOARCH="${ARCH}" \
    go build -tags "wails,desktop,production" \
    -ldflags "-s -w -X any_device/internal/util.BuildTime=${BUILD_TIME}" \
    -o "bin/any_device_linux_${ARCH}" main.go

echo ">>> 准备 AppDir..."
rm -rf "$APPDIR"
mkdir -p "$APPDIR/usr/bin" "$APPDIR/usr/share/applications" \
    "$APPDIR/usr/share/icons/hicolor/512x512/apps"

cp "bin/any_device_linux_${ARCH}" "$APPDIR/usr/bin/any_device"
# 图标：从 master PNG 生成 512x512（AppImage 要求 hicolor 尺寸目录）
if command -v convert >/dev/null 2>&1; then
    convert build/appicon_master.png -resize 512x512 \
        "$APPDIR/usr/share/icons/hicolor/512x512/apps/${APP_NAME}.png"
elif command -v magick >/dev/null 2>&1; then
    magick build/appicon_master.png -resize 512x512 \
        "$APPDIR/usr/share/icons/hicolor/512x512/apps/${APP_NAME}.png"
else
    # 无 ImageMagick 时直接用原图（尺寸需为正方形，AppImage 不强制 512）
    cp build/appicon_master.png "$APPDIR/usr/share/icons/hicolor/512x512/apps/${APP_NAME}.png"
fi

cat > "$APPDIR/usr/share/applications/${APP_NAME}.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=AnyDevice
Comment=跨端音频、鼠标与键盘无线映射中心
Exec=any_device
Icon=${APP_NAME}
Categories=Utility;Network;
Terminal=false
DESKTOP

# 许可文本（BSD-3-Clause 要求随二进制分发）
mkdir -p "$APPDIR/usr/share/licenses/${APP_NAME}"
cp third_party/libopus/COPYING "$APPDIR/usr/share/licenses/${APP_NAME}/libopus-COPYING"

echo ">>> 用 linuxdeploy 收集 GTK/WebKitGTK 等运行库并生成 AppImage..."
TOOLS="${RUNNER_TEMP:-/tmp}/appimage-tools"
mkdir -p "$TOOLS"
if [ ! -x "$TOOLS/linuxdeploy" ]; then
    curl -fsSL --max-time 300 -o "$TOOLS/linuxdeploy" \
        "https://github.com/linuxdeploy/linuxdeploy/releases/download/continuous/linuxdeploy-${ARCH}.AppImage"
    chmod +x "$TOOLS/linuxdeploy"
fi
if [ ! -x "$TOOLS/linuxdeploy-plugin-gtk.sh" ]; then
    curl -fsSL --max-time 120 -o "$TOOLS/linuxdeploy-plugin-gtk.sh" \
        "https://raw.githubusercontent.com/linuxdeploy/linuxdeploy-plugin-gtk/master/linuxdeploy-plugin-gtk.sh"
    chmod +x "$TOOLS/linuxdeploy-plugin-gtk.sh"
fi

# AppImage 在容器/CI 里运行需要 FUSE 替代方案（--appimage-extract-and-run）
export APPIMAGE_EXTRACT_AND_RUN=1
export LDAI_OUTPUT="$(pwd)/$OUT"
export LINUXDEPLOY_OUTPUT_VERSION="${VERSION}"
export DEPLOY_GTK_VERSION=3
export ARCH

"$TOOLS/linuxdeploy" --appdir "$APPDIR" \
    --plugin gtk \
    --desktop-file "$APPDIR/usr/share/applications/${APP_NAME}.desktop" \
    --icon-file "$APPDIR/usr/share/icons/hicolor/512x512/apps/${APP_NAME}.png" \
    --output appimage

echo ">>> 完成：$OUT"
ls -lh "$OUT"
