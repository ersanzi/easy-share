# 控制面批次收尾 — P4 挂载对账 + KI-5 死代码清理 + 设置页账号资料

## 用户问题

- 外部批次合入后（2026-08-31，b1de4dc/866a94e），文档与代码脱节：progress.md、合并迭代记录、architecture.md、README 都把 P4「按用户命名空间的资源管理器挂载」写成"剩余/入口不可用"，但实读代码发现批次已携带完整实现（`spacemount.go` 401 行 + `internal/spacedav/` + `internal/namespace` 双平台 + 浮窗空间切换器 `SetDropSpace`，并已接线登录/登出/悬浮窗拖放）。后续开发者按文档去做 P4 会重复造轮子。
- KI-5（Core 侧 `/api/cloud/*` 七条死路由 + `cloud.Service` + `webdavfs`，约 900 行）当时因"P4 方案未定 webdavfs 去留"而搁置；现在 P4 已选 spacedav 路线，删除分支明确。死代码的风险是回归性的：看起来可用的云盘实现，误接后静默绕过用户隔离。
- 设置页没有任何账号区：登录后用户在设置页看不到自己是谁、空间用量多少，只能回主界面标题栏找头像 chip。P1 只做了"点头像进设置"，资料展示一直欠着。

## 目标

1. 文档对账：progress.md（进行中/已完成/已知阻塞）、architecture.md（§1 进程图、§2 代码入口、§3 端口、§4 API 表、§5 生命周期、§8）、README「当前限制」与特性清单、known-issues KI-5、合并文档勘误注记，全部与代码一致。
2. KI-5 清理：删除 `/api/cloud/*` 七条路由、`cloud.Service`/`ConfigureCloud`/`StartCloudDrive`/`StopCloudDrive`、`internal/cloud/webdavfs/`、`internal/drive/cloud_service.go`、`cloud_preview.go` 及其测试；保留 `cloud.File`/`cloud.Preview`/`DetectPreviewKind`/`ContentTypeForKey`/`FillTextPreview` 与 `objectstore` 抽象。
3. 设置页账号资料卡片：头像/昵称/账号名/管理员标识 + 个人/共享空间用量 + 退出登录入口。
4. 回归绿 + 构建过（冲刺期轻测试豁免生效）。

## 非目标

- 真机鼠标验收（登录→上传→换账号看列表、悬浮窗悬停/固定/拖放）——需要用户上手，登记为遗留。
- 不动 spacedav/spacemount 行为（本迭代只做文档对账，不做挂载代码变更）。
- 不做 2b/2c 权限感知检索、空间整理算法、KI-3 稳定文件身份。

## 设计决策

- **P4 认定**：`spacemount.go` + `internal/spacedav`（建在 `internal/drive` 客户端之上，每操作经控制面）即 P4 的落地实现；「此电脑」条目 = 个人空间（`<昵称> 的网盘`，19082）+ 共享空间（`EasyShare 共享`，19083），随登录态挂卸。旧 19081 云盘 WebDAV（bucket 根挂载点）确认永久下线，不再恢复。
- **KI-5 走删除分支**：P4 未复用 `webdavfs`（spacedav 与它的差异写在该包文档注释里：凭据与授权模型不同），故 `webdavfs` 整包删除，不改造。保留面以 app.go 实际引用为准：`cloud.File`（driveObjectToFile 的返回类型，迁至 preview.go）、`cloud.Preview`、`DetectPreviewKind`、`ContentTypeForKey`、`FillTextPreview`；`objectstore`（s3store/memory）因 `scripts/create_bucket.go` 仍在用而保留。
- **设置页账号资料不加新 Go API**：`CurrentUser()`（AuthUser 含头像 URL）与 `MySpaces()`（含用量/配额/权限）绑定已存在且有前端封装，纯前端卡片。头像失败回退首字符圆标（Avatar 字段是控制面 URL，可能为空或不可达）。

## 兼容与迁移

- config.json 无新字段；`/api/cloud/*` 删除对外无影响（前端经 Wails 绑定走 app.go → 控制面，全仓无 `/api/cloud` 调用方）。
- Wails 绑定不变（app.go 导出方法集不变），无 `wails generate` 级联。
- 端口：19081 从此无进程监听；19082/19083 由桌面端进程（非 Core）监听。

## 测试计划

- `go build ./...` + `go test ./...`（cloud 包测试随保留面裁剪：只留 FillTextPreview/DetectPreviewKind/ContentTypeForKey 相关）。
- `vue-tsc --noEmit` + `vitest run`（设置页账号卡片如需测试按冲刺豁免从简）。
- `wails build` + `go build -o build/bin/easyshare-core.exe ./cmd/core`（AGENTS 坑表：只跑 wails build 不重编 Core）。

## 发布与回滚

- 常规桌面端发布；无数据迁移。回滚 = 回退提交。
- 观察信号：Core 日志不应再出现 `start cloud WebDAV`；`GET /api/cloud/files` 应返回 404（此前是 503）。

## 完成记录

### 已完成项（2026-08-31）

1. **P4 对账（核心发现）**：外部批次 b1de4dc 已携带 P4 全套实现——`spacemount.go`（401 行，双空间挂载/登出卸载/浮窗拖放/切换器）、`internal/spacedav`（建在 drive 客户端之上的 WebDAV FS，含 3 秒清单缓存、内存空目录、只读拒写、目录改名明确拒绝）、`internal/namespace` 双平台 `SpaceEntries`。progress/合并文档/architecture/README 均已纠正（合并文档加勘误注记，历史记录不重写）。
2. **KI-5 清理**：净删约 1390 行——`internal/api` 七条 `/api/cloud/*` 路由 + `ConfigureCloud`/`ConfigureCloudDrive`/`StartCloudDrive`/`StopCloudDrive` + 五 handler + `cloud_preview.go`（HMAC 票据）及测试；`internal/cloud/service.go` 与 `webdavfs/` 整包；`internal/drive/cloud_service.go`；`internal/desktop/client.go` 的 Cloud* 方法（实读发现的连带死代码，KI-5 登记时未覆盖到）。`cloud.File` 迁入 `preview.go`，预览辅助与 `objectstore` 保留。known-issues KI-5 转「已修复」。
3. **设置页「账号」卡片**：首字头像 + 昵称/账号名/管理员标识 + 个人/共享空间用量（待开通/不限/只读口径与 Go 侧一致）+ 退出登录（emit → App.vue 绑 `app.logout`，登录态经 composable 单一路径清空）。样式入 `style.css` 设置页分段。
4. **文档同步**：architecture.md（进程图去 19081/cloud、代码入口表、端口表加 19082/19083、API 表删云盘路由、系统文件入口重写、退出顺序、§8）、README（特性/仓库结构/当前限制）、progress.md（当前位置/已完成/进行中/已知阻塞）、known-issues.md。

### 测试结果（2026-08-31）

- `go build ./...` / `go vet ./...` / `go test ./...`：18 包全绿（cloud 包测试随保留面裁剪为 FillTextPreview/DetectPreviewKind 直测）
- `vue-tsc --noEmit`：无错误；`vitest run`：33/33
- `go build -o build/bin/easyshare-core.exe ./cmd/core` + `wails build`：通过（无绑定变更，无 `wails generate` 级联）

### 已知问题 / 遗留

- 真机鼠标验收欠账移交 progress.md 已知阻塞（四项清单），建议随公司部署一并做
- KI-4 的更彻底修复方向（自有 MIME 表替代 `mime.TypeByExtension`）未动——`ContentTypeForKey` 仍受宿主机注册表影响，属独立行为变更
- spacedav 的 Rename 是"下载+上传+删除"，目录改名明确拒绝（EPERM）——产品化前若要支持需控制面出 rename/服务端复制接口，已在该包注释中说明
