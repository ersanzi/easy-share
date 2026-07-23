# v0.1.0-preview.1 对外预览版发布

## 用户问题

测试 Release 下载闭环已经跑通，需要选择 `preview`、`beta` 或其他阶段名称，并发布一个当前最新的预发布版本。

## 结论

当前采用 `v0.1.0-preview.1`，不直接进入 `beta`。

原因：macOS Apple Silicon / Intel 真机安装、菜单栏、Finder WebDAV，以及 Windows 10/11 安装与系统入口仍有验收项未完成。`preview` 能明确表达“可下载体验，但仍可能不稳定”；`beta` 留给主要能力和跨平台真机验收基本稳定之后；`rc` 只用于没有已知发布阻断项的正式候选。

阶段约定：

| 标识 | 用途 |
| --- | --- |
| `test` | CI/Release 流水线冒烟，不面向普通用户 |
| `preview` | 可公开下载体验，但功能或真机验收尚未完全收口 |
| `beta` | 主要功能稳定，进入更大范围测试 |
| `rc` | 正式版候选，只接受发布阻断问题修复 |
| 无后缀 | 正式稳定版 |

## 技术决策

GitHub Actions 不再只识别 `-test.`。凡 tag 名包含连字符，按 SemVer 预发布版本处理：

```yaml
prerelease: ${{ contains(github.ref_name, '-') }}
```

这样 `v0.1.0-test.2`、`v0.1.0-preview.1`、`v0.1.0-beta.1`、`v0.1.0-rc.1` 都会创建 Prerelease；`v0.1.0` 才会创建正式 Release。

## 发布流程

```bash
git push origin dev
git push github dev
git tag -a v0.1.0-preview.1 -m "EasyShare v0.1.0 Preview 1"
git push origin v0.1.0-preview.1
git push github v0.1.0-preview.1
```

GitHub 收到 tag 后，macOS 与 Windows workflow 会分别构建并把资产上传到同一个 `v0.1.0-preview.1` Prerelease。

## 验收标准

1. Gitee、GitHub 都存在 `v0.1.0-preview.1` tag；
2. GitHub macOS Build 与 Windows Build 均由该 tag 触发；
3. Release 标记为 Prerelease，而不是正式 Latest；
4. Release 同时包含 macOS DMG/app zip/校验文件和 Windows 安装包/双进程文件；
5. 后续完成 Apple Silicon、Intel Mac、Windows 10/11 真机安装与启动验收。

## 排障方法

- Release 被错误标成正式版：检查两个 workflow 的 `prerelease` 表达式是否仍只匹配 `-test.`；
- 只在 Gitee 有 tag：补执行 `git push github <tag>`，否则不会触发 GitHub Actions；
- 一个 Release 只有单平台资产：分别检查 macOS Build 和 Windows Build 的 tag run；两个 workflow 会并发更新同一个 Release；
- 页面没有“Latest”徽标不代表发布失败：GitHub 的正式 Latest 默认只授予非 Prerelease；预览版应显示 Pre-release。
