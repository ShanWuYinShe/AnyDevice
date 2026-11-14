# 发布流程

版本号以 **git tag** 为唯一源（仓库内不维护版本文件），推 tag 即触发
`.github/workflows/release.yml`：三平台并行构建 → 自动创建 GitHub Release
并附上全部安装包。

```bash
# 1. 确保 main 已同步且 CI 全绿
git checkout main && git pull --ff-only

# 2. 打 annotated tag（SemVer：破坏性升 major、新功能升 minor、修复升 patch）
git tag -a v1.2.0 -m "AnyDevice v1.2.0"

# 3. 逐个推送并确认（禁止 git push --tags：超过 3 个 tag 时 GitHub 不产生事件）
git push origin v1.2.0
gh run watch $(gh run list --workflow=release.yml --limit 1 --json databaseId --jq '.[0].databaseId')
```

Release 产物：macOS 三架构 zip、Windows 双 exe、Linux AppImage + 裸二进制。
带 `-rc` / `-beta` 后缀的 tag 会标记为预发布。

> 手动演练：`gh workflow run release.yml`（只构建产物、不创建 Release）。
