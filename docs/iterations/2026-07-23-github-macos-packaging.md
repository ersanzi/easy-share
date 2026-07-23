# GitHub Actions macOS 自动打包与产物校验

> 日期：2026-07-23
> 状态：进行中

## 用户问题

代码已经推送到 GitHub，但 Actions 左侧没有出现 `macOS Build`。仓库中的 `.github/workflows/build-mac.yml` 可通过 Contents API 读取，Actions API 却只登记了系统生成的 Dependency Graph workflow。

## 目标

1. 让 GitHub 在默认分支上重新登记 macOS workflow；
2. `master`、Pull Request、手动运行和 `v*` 标签都拥有明确的构建入口；
3. runner 在上传前验证 `.app`、Core 和 DMG，而不是只依赖构建命令退出码；
4. 提供可安全下载的 `.app.zip`、DMG 和 SHA-256 校验文件；
5. tag 构建自动形成 GitHub Release 产物。

## 技术决策

- 增加 `master` push 和 Pull Request 触发。对 workflow 文件做一次真实提交，可强制 GitHub 重新扫描默认分支，同时之后每次主分支变更都能发现 macOS 回归。
- 保留 `workflow_dispatch` 的目标架构选择以及 `v*` tag 发布。
- runner 先执行 Go、前端编译与测试，再进入 Wails 打包，避免把单元测试失败混入打包日志。
- universal 构建必须使用 `lipo -verify_arch arm64 x86_64` 同时检查桌面端和捆绑 Core。
- `.app` 使用 macOS `ditto` 打成 zip，避免普通跨平台压缩破坏可执行权限、扩展属性和包结构。
- Artifact 同时上传 DMG、`.app.zip` 和 `SHA256SUMS.txt`；找不到产物时立即失败。
- 使用 concurrency 取消同一引用的旧构建，避免重复占用 macOS runner。

## 代码影响

| 文件 | 修改 |
| --- | --- |
| `.github/workflows/build-mac.yml` | 增加 master/PR 触发、测试、缓存、架构检查、zip 和校验文件 |
| `docs/progress.md` | 登记当前自动打包迭代 |
| `README.md` | 同步 macOS CI 与产物说明 |

## 验证方法

本地验证：

```powershell
git diff --check
go build ./...
go test ./...
npm --prefix frontend run build
npm --prefix frontend test
```

GitHub 验证：

1. Actions API 能返回 `macOS Build`；
2. 推送 `master` 后自动出现 workflow run；
3. 所有编译、测试和打包步骤通过；
4. Artifact 包含 `EasyShare.dmg`、`EasyShare.app.zip`、`SHA256SUMS.txt`；
5. 下载后校验 SHA-256，并在真实 Mac 上完成菜单栏和 Finder 挂载验收。

## 排障方法（省的下次还有问题）

- 文件存在但 Actions 左侧不显示：先查 `/repos/<owner>/<repo>/actions/workflows`，区分“文件已推送”和“workflow 已登记”。对默认分支上的 workflow 做一次有效提交通常会触发重新扫描。
- HTTPS 推送 workflow 被拒绝并提示缺少 `workflow` scope：使用带 `workflow` 权限的 Token，或改用已经登记到 GitHub 的 SSH key。本项目当前 GitHub remote 使用 `ssh.github.com:443`，可避开网络对 22 端口的限制。
- 构建成功但应用不可运行：检查 Artifact 是否直接压缩了 `.app` 目录；macOS 应使用 `ditto --keepParent` 保留 bundle 元数据。
- universal 桌面端启动不了 Core：分别对 `Contents/MacOS/easyshare` 和 `easyshare-core` 执行 `lipo -info`，两者都必须包含 arm64 与 x86_64。
