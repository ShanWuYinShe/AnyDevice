#!/usr/bin/env bash
# 从 Homebrew 官方 bottle 提取 macOS x86_64 的 libopus.a 静态库。
#
# 为什么需要：仓库 vendored 的 arm64 库来自 Homebrew opus 1.6.1 bottle，
# Intel 版也应来自同一来源以保证版本/编译选项一致，而不是手工编译
# （手工编译的产物不可复现、无法审计）。
#
# 用法：./scripts/fetch-libopus-amd64.sh [输出目录]
#   CI 中由 .github/workflows/ci.yml 的 libopus-amd64 job 调用并上传 artifact；
#   维护者下载 artifact 后覆盖 third_party/libopus/macos-amd64/libopus.a 即可。
set -euo pipefail

OUT_DIR="${1:-third_party/libopus/macos-amd64}"
# Homebrew opus 1.6.1 sonoma bottle（x86_64 架构）；sha256 用于完整性校验。
# 升级 opus 版本时需同步更新此处的 URL 与 sha256（可从
# https://formulae.brew.sh/api/formula/opus.json 的 bottle.stable.files 取得）。
BOTTLE_URL="https://ghcr.io/v2/homebrew/core/opus/blobs/sha256:0849d017c155c55ea1fb01b6156ff5dc77f2a2d7fec84592a099c32f6db9eef1"
BOTTLE_SHA256="0849d017c155c55ea1fb01b6156ff5dc77f2a2d7fec84592a099c32f6db9eef1"
# GHCR 的 blob 需要 Homebrew 的匿名 token 才能拉取
TOKEN_URL="https://ghcr.io/token?scope=repository:homebrew/core/opus:pull&service=ghcr.io"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo ">>> 获取 GHCR 匿名 token..."
TOKEN=$(curl -fsSL --max-time 60 "$TOKEN_URL" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
[ -n "$TOKEN" ] || { echo "获取 token 失败"; exit 1; }

echo ">>> 下载 opus 1.6.1 sonoma bottle..."
curl -fsSL --max-time 300 -H "Authorization: Bearer $TOKEN" \
    -o "$TMP/opus.tar.gz" "$BOTTLE_URL"

echo ">>> 校验 sha256..."
ACTUAL=$(shasum -a 256 "$TMP/opus.tar.gz" | awk '{print $1}')
[ "$ACTUAL" = "$BOTTLE_SHA256" ] || {
    echo "sha256 不匹配：期望 $BOTTLE_SHA256，实得 $ACTUAL"; exit 1
}

echo ">>> 解包并提取 libopus.a..."
tar xzf "$TMP/opus.tar.gz" -C "$TMP"
LIB=$(find "$TMP" -name 'libopus.a' -type f | head -1)
[ -n "$LIB" ] || { echo "bottle 内未找到 libopus.a"; exit 1; }

mkdir -p "$OUT_DIR"
cp "$LIB" "$OUT_DIR/libopus.a"

echo ">>> 校验架构（应为 x86_64）..."
lipo -info "$OUT_DIR/libopus.a"
ARCHS=$(lipo -archs "$OUT_DIR/libopus.a")
case "$ARCHS" in
    *x86_64*) ;;
    *) echo "架构不符：$ARCHS（期望 x86_64）"; exit 1 ;;
esac

echo ">>> 校验关键符号..."
# 先导出完整符号表再逐个匹配：直接 nm | grep -q 会因 grep 提前退出触发
# SIGPIPE，使 nm 输出被截断、后续符号误判为缺失
nm "$OUT_DIR/libopus.a" > "$TMP/symbols.txt" 2>/dev/null || true
for SYM in opus_encoder_create opus_encode_float opus_decoder_create opus_decode_float; do
    grep -qw "_${SYM}" "$TMP/symbols.txt" || {
        echo "缺少符号：$SYM"; exit 1
    }
done

echo ">>> 完成：$OUT_DIR/libopus.a（$(ls -lh "$OUT_DIR/libopus.a" | awk '{print $5}')）"
