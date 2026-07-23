# P0 双平台发布验收：不稳定测试 tag

> 日期：2026-07-23
> 状态：进行中

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

## 结果

待 tag workflow 完成后补充运行链接、Release 资产清单和真实机器验收结论。