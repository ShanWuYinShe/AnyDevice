# libopus（vendored 静态库）

跨平台构建用的 Opus 静态库，避免构建机与最终用户安装 Homebrew / 系统 opus 包。

| 路径 | 目标 | 来源 | 版本 |
| :--- | :--- | :--- | :--- |
| `include/opus/` | 共用头文件 | Xiph libopus | 与 win64 库配套（所用 API 自 1.0 起稳定） |
| `win64/libopus.a` | Windows x86_64 | mingw 交叉编译静态库 | — |
| `macos-arm64/libopus.a` | macOS arm64 | Homebrew opus 1.6.1 bottle 的 `lib/libopus.a` | 1.6.1 |
| `macos-amd64/libopus.a` | macOS x86_64 | Homebrew opus 1.6.1 sonoma bottle 的 `lib/libopus.a`，由 `scripts/fetch-libopus-amd64.sh` 提取（sha256 校验） | 1.6.1 |

链接方式见 `internal/audio/opus_encoder.go` 的 cgo 指令：

- darwin/arm64：`-I${SRCDIR}/../../third_party/libopus/include -L${SRCDIR}/../../third_party/libopus/macos-arm64 -lopus`
- darwin/amd64：`-I${SRCDIR}/../../third_party/libopus/include -L${SRCDIR}/../../third_party/libopus/macos-amd64 -lopus`
- linux：`-I${SRCDIR}/../../third_party/libopus/include -l:libopus.a`（静态链接系统库）
- windows：`-I${SRCDIR}/../../third_party/libopus/include -L${SRCDIR}/../../third_party/libopus/win64 -lopus`

目录内只有 `.a`（无 `.dylib` / `.dll`），链接结果是静态的。验证方式：

```bash
otool -L bin/AnyDevice.app/Contents/MacOS/any_device | grep -i opus   # 应无输出
nm bin/AnyDevice.app/Contents/MacOS/any_device | grep -c opus_decode_float  # >0
```

## 许可

libopus 为 BSD-3-Clause，见 `COPYING` / `AUTHORS`。二进制分发（.app / exe）需随附
许可文本：`scripts/build.sh` 会把 `COPYING` 放进
`AnyDevice.app/Contents/Resources/third_party/libopus-COPYING`（Windows 分发包同理应附上）。

## 更新

替换对应平台的 `libopus.a` 即可；头文件与库版本不一致时仅使用稳定的公开 API
（`opus_encoder_create/ctl/encode_float`、`opus_decoder_create/decode_float` 等）。

macOS 两个架构的库都必须来自 Homebrew 官方 bottle（保证版本与编译选项一致），
不要手工编译：

```bash
# arm64：从本机 Homebrew 安装目录取（brew install opus）
cp "$(brew --prefix opus)/lib/libopus.a" third_party/libopus/macos-arm64/libopus.a

# amd64：从官方 bottle 提取（脚本内含 sha256 校验，CI 也会校验一致性）
./scripts/fetch-libopus-amd64.sh third_party/libopus/macos-amd64
```

CI 的 macos job 会重新跑一次提取并与仓库中的文件比对，不一致即失败——
防止手工编译的不可复现产物混入仓库。升级 opus 版本时同步更新脚本里的
URL/sha256（取自 https://formulae.brew.sh/api/formula/opus.json 的
`bottle.stable.files`）。
