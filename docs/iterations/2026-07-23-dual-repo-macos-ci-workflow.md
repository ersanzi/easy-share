# 双仓库推送规范与 macOS CI 日常流程

## 用户问题

项目同时维护 Gitee 与 GitHub，但只推送默认的 `origin` 时，GitHub 不会收到提交，`build-mac.yml` 也不会启动。需要把双仓库推送写入提交规范，并明确日常构建和发布版本的操作路径，避免再次出现“Gitee 已更新但 GitHub Actions 没有运行”的误判。

## 目标

- 在 `AGENTS.md` 的提交规范下新增「双仓库推送」，明确 `origin`（Gitee）与 `github`（GitHub）每次都要 push；
- 说明 `build-mac.yml` 的三种主要触发：`dev` push 自动构建、`workflow_dispatch` 手动选择架构、`v*` tag 自动创建 Release；
- 固化日常流程：推送 GitHub `dev` 后从 Actions Artifacts 下载 DMG；发布时推送 tag 后从 Release 获取产物；
- 同步 README、macOS 移植文档与进度真相源。

## 技术决策

1. 保留两个语义清晰的 remote，不再使用给同一 `origin` 配置多个 push URL 的隐式镜像方式；
2. Gitee 仍是国内主仓库，GitHub 专门承载 macOS GitHub Actions 与 Release；
3. 普通 `dev` 构建只上传 Actions Artifact，不创建 Release；
4. 只有 `refs/tags/v*` 触发的成功构建才执行 `softprops/action-gh-release`；
5. 手动构建默认选择 `darwin/universal`，也可按需选择 `darwin/arm64` 或 `darwin/amd64`。

## 日常操作

提交后：

```bash
git push origin dev
git push github dev
```

随后进入 GitHub Actions 的 `macOS Build` 运行记录，从 Artifacts 下载：

- `EasyShare.dmg`
- `EasyShare.app.zip`
- `SHA256SUMS.txt`

发布版本：

```bash
git tag v1.0.0
git push origin v1.0.0
git push github v1.0.0
```

GitHub 收到 `v*` tag 后自动构建，并在成功后创建 Release。

## 代码影响

| 文件 | 修改 |
| --- | --- |
| `AGENTS.md` | 在提交规范下新增双仓库推送要求与命令 |
| `README.md` | 补充三种 macOS CI 触发方式、Artifacts 与 Release 流程 |
| `docs/macos-port.md` | 改为现行双 remote 操作，移除旧的 `origin` 多 push URL 说明 |
| `docs/progress.md` | 登记本次已完成迭代并同步 macOS CI 状态 |

## 验证方法

```powershell
git diff --check
```

人工核对 `.github/workflows/build-mac.yml`：

- `push.branches` 包含 `dev`；
- `workflow_dispatch.inputs.platform` 提供 universal、arm64、amd64；
- `push.tags` 匹配 `v*`；
- Release 步骤带 `if: startsWith(github.ref, 'refs/tags/v')`；
- 普通构建上传 DMG、`.app.zip`、`SHA256SUMS.txt` 到 Artifacts。

## 排障方法（省的下次还有问题）

- Gitee 有最新提交、GitHub Actions 没运行：执行 `git status` 与 `git log github/dev..dev --oneline`，确认是否漏推 `github` remote；
- `dev` 构建成功但 Releases 没有新版本：这是预期行为，普通分支 push 只产生 Artifact；只有 `v*` tag 才创建 Release；
- 手动构建架构不对：在 Run workflow 对话框重新选择 `darwin/universal`、`darwin/arm64` 或 `darwin/amd64`；
- tag 只推到了 Gitee：补执行 `git push github <tag>`，GitHub 收到 tag 后才会触发 Release 流程；
- 下载后需验证完整性：在 macOS 执行 `shasum -a 256 -c SHA256SUMS.txt`。