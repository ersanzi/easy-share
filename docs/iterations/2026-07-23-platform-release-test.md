# P0 双平台发布验收：不稳定测试 tag

> 日期：2026-07-23
> 状态：已完成（Release 下载闭环通过，真机安装验收待后续执行）

## 背景

EasyShare 已在 `dev` 分支配置 macOS 与 Windows GitHub Actions。此前已验证 macOS workflow 能完成编译、测试、架构检查和 Artifact 上传，但尚未验证 Windows workflow 以及 tag 触发的 Release 下载闭环。

## 目标

- 从当前 `dev` 提交创建不稳定测试 tag `v0.1.0-test.1`；
- 将 tag 分别推送到 Gitee (`origin`) 和 GitHub (`github`)；
- 验证 macOS 与 Windows workflow 均能被 tag 触发；
- 验证两个 workflow 生成的产物能上传到同一个 GitHub Release 并可下载；
- 不影响正式版本 `v0.1.0` 的发布状态。

## 测试流程

```bash
git push origin dev
git push github dev

git tag v0.1.0-test.1
git push origin v0.1.0-test.1
git push github v0.1.0-test.1
```

GitHub Actions 应出现两个 tag workflow run：

- `macOS Build`：DMG、`.app.zip`、`SHA256SUMS.txt`；
- `Windows Build`：桌面端、Core 和 Windows 安装包。

## 验收标准

- [ ] Gitee 和 GitHub 均存在 `v0.1.0-test.1`；
- [ ] macOS tag workflow 成功；
- [ ] Windows tag workflow 成功；
- [ ] GitHub Release 自动创建；
- [ ] Release 可下载 macOS DMG；
- [ ] Release 可下载 Windows 安装包；
- [ ] Release 资产没有覆盖或丢失；
- [ ] Actions Artifacts 仍可单独下载；
- [ ] 测试完成后记录真实 Mac/Windows 安装验收结果。

## 风险与回滚

- 测试 tag 只用于验证，不代表稳定版本，不应作为正式发布版本使用；
- 如果两个 workflow 并发创建 Release 产生冲突，需要把 Release 创建集中到单独的发布 job，构建 job 只上传 Artifact；
- 如果只推送到了 Gitee，补执行 `git push github v0.1.0-test.1`；
- 如需删除测试 tag：

```bash
git push origin --delete v0.1.0-test.1
git push github --delete v0.1.0-test.1
```

删除远程 tag 后，还需要在 GitHub Releases 页面手动删除对应测试 Release。

## test.1 结果

`v0.1.0-test.1` 已成功推送到 Gitee 和 GitHub。GitHub 上的 macOS Build #10 与 Windows Build #3 均执行成功，两个 workflow 能并发写入同一个 Release，Release 下载功能基本可用。

已实际下载并核对以下资产：

- `EasyShare.dmg`：24,394,491 字节，SHA-256 通过；
- `EasyShare.app.zip`：22,763,977 字节，SHA-256 通过；
- `SHA256SUMS.txt`：164 字节；
- `easyshare.exe`：11,576,832 字节；
- `easyshare-core.exe`：13,927,936 字节。

发现两个问题：

1. Windows workflow 没有生成 NSIS 安装包。根因是 Chocolatey 安装 NSIS 后，没有把 `${env:ProgramFiles(x86)}\NSIS` 显式写入后续步骤的 `GITHUB_PATH`；原产物校验又只发 warning，导致 workflow 误报成功。
2. 测试 tag 创建的 Release 默认不是 Prerelease，容易被误认为稳定版本。

修复方案：

- 安装 NSIS 后检查 `makensis.exe` 并写入 `GITHUB_PATH`；
- Windows 安装包缺失时直接让 workflow 失败；
- tag 名包含 `-test.` 时给 Release 设置 `prerelease: true`；
- 创建 `v0.1.0-test.2` 重新验证完整的 macOS + Windows Release 资产。

## test.2 最终结果

`v0.1.0-test.2` 已成功推送到 Gitee 和 GitHub。两个 tag workflow 均成功：

- macOS Build #13：成功；
- Windows Build #6：成功；
- Release：[`v0.1.0-test.2`](https://github.com/ersanzi/easy-share/releases/tag/v0.1.0-test.2)；
- Release 已标记为 **Prerelease**。

Release 共包含 6 个资产，均已实际下载并验证文件大小：

| 资产 | 大小 | 验证 |
| --- | ---: | --- |
| `EasyShare.dmg` | 24,394,478 bytes | SHA-256 通过 |
| `EasyShare.app.zip` | 22,763,972 bytes | SHA-256 通过 |
| `SHA256SUMS.txt` | 164 bytes | 已下载 |
| `EasyShare-amd64-installer.exe` | 13,698,761 bytes | PE 文件头通过 |
| `easyshare.exe` | 11,576,832 bytes | 已下载 |
| `easyshare-core.exe` | 13,927,936 bytes | 已下载 |

结论：**tag → macOS/Windows Actions → 同一 GitHub Prerelease → 资产下载** 的闭环已经跑通。两个 workflow 并发发布时没有发生 Release 冲突，资产也没有互相覆盖。

仍未完成的部分是下载后的真实设备验收：需要在 Apple Silicon Mac、Intel Mac 和 Windows 10/11 上分别安装并启动，确认菜单栏、Finder WebDAV、Windows 安装/卸载和「此电脑」入口。