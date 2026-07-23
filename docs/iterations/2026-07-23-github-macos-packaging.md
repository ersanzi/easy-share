# GitHub Actions macOS 自动打包与产物校验

> 日期：2026-07-23
> 状态：已完成（待 Mac 真机验收）

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
- universal 构建使用 `lipo -archs` 读取实际架构，再逐项检查 `arm64` 与 `x86_64`；避免 `lipo -verify_arch` 在不同 macOS runner 工具链上的参数兼容问题。
- `.app` 使用 macOS `ditto` 打成 zip，避免普通跨平台压缩破坏可执行权限、扩展属性和包结构。
- Artifact 同时上传 DMG、`.app.zip` 和 `SHA256SUMS.txt`；找不到产物时立即失败。
- 打包和产物校验都通过 `tee` 保存完整日志；失败时把合并日志末尾 200 行写入 Job Summary 并上传诊断 Artifact，便于无 Mac 环境下远程定位。
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

## 首次 runner 结果

GitHub 已成功登记 `macOS Build`，提交 `ff6d3bc` 的 push 自动触发了首次运行。运行在“Go 编译与测试”步骤失败：

```text
pattern all:frontend/dist: no matching files found
```

根因是 `main.go` 使用 `//go:embed all:frontend/dist`，而 `frontend/dist` 不纳入 Git。干净 runner 必须先执行前端构建生成该目录，随后才能编译 Go 主程序。工作流已调整为“前端构建与测试 → Go 编译与测试 → Wails 打包”。

runner 同时提示 checkout/setup-go/setup-node 的 Node.js 20 action runtime 已弃用，因此升级为当前 Node.js 24 major；上传产物 action 同步升级到 Node.js 24 major。
## 第二次 runner 结果

前端、Go 编译测试和 Wails 安装均通过，打包脚本随后报告：

```text
scripts/build-mac.sh: line 23: PLATFORM…: unbound variable
```

脚本启用了 `set -u`，`$PLATFORM` 后紧邻中文全角标点时，在 runner 的 Bash/locale 组合下被错误解释为更长的变量名。已把所有同类位置改为 `${PLATFORM}`，变量边界不再依赖后续字符。

## 第三次 runner 结果

Core、桌面端和 DMG 已经成功构建，失败只发生在“校验并整理产物”步骤。原校验直接调用 `lipo -verify_arch`，改为使用 `lipo -archs` 取得实际架构列表，然后用精确匹配逐项验证。

同时调整诊断步骤顺序：产物校验写入 `build/logs/artifact-validation.log`，打包或校验任一阶段失败时，都会输出 Job Summary、Check Annotation 并上传 diagnostics Artifact。

本次修复已在 Windows 开发机完成 YAML 解析、`git diff --check` 和项目全量构建测试，待 GitHub macOS runner 复验。

## 第四次 runner 结果

GitHub Actions Run #6（提交 `9335899`）已完整通过：

- 前端构建与测试：通过
- Go 编译与测试：通过
- Wails 环境检查：通过
- Core、App 与 DMG 构建：通过
- universal 架构校验：通过
- 产物上传：通过

Run 产出 Artifact `EasyShare-macOS-6`，包含 `EasyShare.dmg`、`EasyShare.app.zip` 和 `SHA256SUMS.txt`。至此，GitHub 自动打包闭环已完成，后续工作是下载 Artifact 后在真实 Mac 上验收菜单栏、Finder WebDAV 挂载与两种 CPU 架构启动。

## 排障方法（省的下次还有问题）

- 文件存在但 Actions 左侧不显示：先查 `/repos/<owner>/<repo>/actions/workflows`，区分“文件已推送”和“workflow 已登记”。对默认分支上的 workflow 做一次有效提交通常会触发重新扫描。
- HTTPS 推送 workflow 被拒绝并提示缺少 `workflow` scope：使用带 `workflow` 权限的 Token，或改用已经登记到 GitHub 的 SSH key。本项目当前 GitHub remote 使用 `ssh.github.com:443`，可避开网络对 22 端口的限制。
- 构建成功但应用不可运行：检查 Artifact 是否直接压缩了 `.app` 目录；macOS 应使用 `ditto --keepParent` 保留 bundle 元数据。
- universal 桌面端启动不了 Core：分别对 `Contents/MacOS/easyshare` 和 `easyshare-core` 执行 `lipo -info`，两者都必须包含 arm64 与 x86_64。
