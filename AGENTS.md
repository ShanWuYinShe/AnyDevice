# any_device

macOS Wails 桌面应用（Go + 前端）。本文件只做项目特有补充，通用约束以全局 `~/AGENTS.md` 为准。

## 常用命令

```bash
# 构建/打包（具体参数以 scripts/ 下脚本为准，先 ls 确认）
ls scripts/
```

## skill 索引（改对应模块前先加载对应 skill）

| skill | 管什么 |
|---|---|
| `wails-macos-native` | AnyDevice.app 的 macOS Wails 原生窗口构建/打包/排障：`wails://wails` 页面里的 WebSocket 地址、WKWebView 缺失 WebCrypto 等实测事实 |

> 新增 skill 时同步本表——漏索引时系统仍会挂载，但 AI 很难主动想到加载它（2026-10-31 补建本文时本目录有 skill 却没有任何 AGENTS.md）。

## 维护提醒

本文件与 skill 里的路径/命令断言，改动构建脚本或升级 Wails 后必须复核；
跑 `~/.agents/bin/skill-lint ~/work/any_device/.agents/skills` 可机器检查失效引用。
