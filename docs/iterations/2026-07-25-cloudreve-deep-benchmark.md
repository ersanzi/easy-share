# Cloudreve 深度对标：文件身份、同步根与任务架构

> 日期：2026-07-25
> 状态：已完成
> 研究基线：Cloudreve `0bb0ab833571d380153edd3529e01a7957b8b4ce`

## 1. 用户问题

在完成第一轮 Cloudreve 对标和网盘在线预览后，继续研究 Cloudreve 还有哪些值得 EasyShare 借鉴的设计思想，并维护完整档案。当前还有其他 agent 修改代码，因此本轮只能维护并提交自己的文档，不能混入或覆盖他人的工作树。

## 2. 目标

- 深挖 Cloudreve 的文件身份、内容实体、上传会话、事件流、任务队列和桌面同步根。
- 判断哪些能力是 EasyShare 回收站、版本、同步、知识索引和大文件上传的共同前置条件。
- 给出 P0/P1/P2 演进顺序和明确的不照搬边界。
- 同步维护 `docs/cloudreve-benchmark.md`、`docs/progress.md`、`README.md` 和排障文档。
- 仅提交本轮文档文件。

## 3. 非目标

- 不修改业务代码，不立即实现 Multipart Upload、SQLite 目录层或 CfAPI。
- 不替换现有 WebDAV/Shell NameSpace 入口。
- 不复制 Cloudreve 的多租户用户、配额、存储策略后台和部署拓扑。
- 不提交其他 agent 正在修改的 Go 文件。

## 4. 研究方法与源码证据

### 4.1 基线固定

通过 GitHub API 确认 Cloudreve `master` 在研究时指向：

```text
0bb0ab833571d380153edd3529e01a7957b8b4ce
fix(dbfs): thumbnail not generated for shared files (close #3495)
```

研究结论绑定该 commit，而不是只记录会持续变化的 `master`。

### 4.2 关键源码

- `ent/schema/file.go`、`ent/schema/entity.go`、`inventory/file.go`
  - 逻辑 File 与物理 Entity/Blob 分离。
  - 主实体、引用计数、上传会话和存储关系分别建模。
- `pkg/filemanager/fs/uri.go`
  - 稳定资源身份可表达我的文件、回收站、分享和搜索视图。
- `pkg/filemanager/fs/dbfs/upload.go`
- `pkg/filemanager/manager/upload.go`
- `pkg/filemanager/workflows/upload.go`
- `service/explorer/upload.go`
  - Upload Session 与逻辑文件/物理实体联动，不只是 Multipart API 封装。
- `service/explorer/events.go`
- `pkg/filemanager/fs/dbfs/events.go`
- `pkg/filemanager/eventhub/`
- `ent/schema/fsevent.go`
  - 文件事件有单调 ID、续传游标和断档后的全量重扫语义。
- `pkg/queue/task.go`、`ent/schema/task.go`、`pkg/filemanager/workflows/`
  - 任务状态、重试、恢复时间、错误和 Cleanup 生命周期统一。

### 4.3 官方文档交叉验证

- `usage/concept`：File/Blob、不可变内容、copy-on-write 和引用关系。
- `usage/desktop-client`：Windows Cloud Files API 同步根、占位文件、按需下载、pin/unpin、状态图标和冲突处理。
- `api/events`：SSE、`Last-Event-ID`、事件过期时返回 409 并要求重扫。
- `usage/metadata`、`usage/file-management/search-files`：目录字段、标签、元数据和全文检索分层。
- `usage/file-management/batch-operations`、`compress-and-decompress`：逐项批处理结果和后台任务。
- `usage/integrations`：第三方集成通过 OAuth scope 获取有限权限。

## 5. 关键发现

### 5.1 当前最大瓶颈是把 S3 key 当永久文件身份

EasyShare 当前 `cloud.File` 以 `Key` 为核心，列表、下载、删除和分享直接操作对象 key。它在基础网盘阶段足够简单，但会让重命名、回收站、版本、分享、缩略图、知识索引和同步事件共同依赖可变化的物理路径。

决定：后续引入轻量目录层和稳定 `fileId`，保留 `key` 作为物理定位器与兼容字段，不一次迁移完整 Cloudreve DBFS。

### 5.2 目录层必须与 Upload Session 一起设计

如果先实现完全绑定 S3 key 的 Multipart Upload，后续增加版本和稳定身份时会再次迁移会话、任务和恢复逻辑。

决定：P0 同时设计 `files`、`file_versions`、`upload_sessions` 与 `tasks`，上传完成以幂等方式切换当前版本；失败或重启时旧版本继续可用。

### 5.3 同步根之前先有文件事件日志

CfAPI 占位文件、跨入口状态同步和知识索引增量更新都需要可靠事件源。只依赖目录轮询会出现漏事件、重复处理和难以诊断的问题。

决定：先实现本地持久化事件日志，后续再增加 loopback SSE。事件断档必须触发全量重扫，不能静默继续。

### 5.4 WebDAV 是当前能力，CfAPI 是独立原型

Cloud Files API 更符合 OneDrive/WPS 式按需文件体验，但涉及 Explorer 生命周期、占位文件状态、离线和崩溃恢复，风险明显高于 WebDAV。

决定：保留现有 WebDAV 作为主入口和跨平台回退；P1 单独做 CfAPI 原型，完成验收矩阵后再决定是否迁移 Windows 主入口。macOS 对等方向为 File Provider。

### 5.5 统一任务模型优先于继续增加任务类型

LAN、云上传/下载、同步、缩略图、解压、知识解析和索引都需要进度、取消、重试、持久化和错误展示。

决定：执行器保持分层，但共享任务基础字段和状态机，避免每类功能形成独立任务孤岛。

### 5.6 通用 capabilities 延续预览方案

`previewKind` 已证明后端声明能力比前端根据扩展名猜测更稳健。

决定：后续逐步返回 `preview/thumbnail/download/share/delete/restore/rename/move/pin` 等明确能力，不采用难读的压缩位图，也不把权限或状态判断散落到前端。

### 5.7 知识索引不能成为文件真相源

目录字段搜索、媒体元数据、全文索引和向量索引用途不同。版本变化、删除、恢复和权限调整必须先落到稳定目录层，再驱动索引失效或重建。

决定：知识索引使用 `fileId + version` 关联，SQLite 目录层是本机文件业务真相，对象存储负责内容，Milvus/全文索引负责检索。

## 6. 建议路线

### P0

1. 稳定 `fileId` 与 SQLite 轻量目录层。
2. Multipart Upload Session、断点恢复与幂等 Complete。
3. 统一任务模型基础字段和状态机。
4. 故障注入：断网、Core 重启、分片失败、重复提交、本地文件变化。

### P1

1. 回收站、版本记录与恢复。
2. 文件事件日志和 loopback SSE。
3. 缩略图与通用 capabilities。
4. 分享记录、撤销和过期管理。
5. Windows Cloud Files API 独立技术原型。

### P2

1. 名称/类型/时间搜索，再扩展标签和媒体元数据。
2. 批量操作逐项结果和部分失败处理。
3. 压缩/解压等长任务统一入队。
4. WPS/第三方 OAuth scope。
5. 音视频、Office 等高级预览按真实需求评估。

## 7. 代码影响评估

本轮没有修改代码。后续若实施 P0，预计影响：

- 新增本地目录/版本/上传会话持久化包，优先考虑 SQLite。
- `internal/cloud` 文件模型增加稳定 ID、版本和 capabilities。
- `internal/objectstore` 增加 Multipart 生命周期接口。
- `internal/api/server.go` 增加会话创建、分片状态、完成/中止和按 fileId 操作的路由。
- `internal/task` 迁移到统一状态机与 checkpoint。
- 前端 `types/core.ts`、`services/core.ts`、`useEasyShare.ts` 按 Wails 绑定级联同步。

实施时必须先写迁移兼容策略，避免现有仅有 S3 对象而没有目录记录的用户看不到文件。建议首次启动扫描对象并惰性补齐 `FileRecord`。

## 8. 验证结果

本轮为文档研究迭代，没有业务代码变化，因此未重复执行全量构建。执行以下文档与 Git 边界检查：

```powershell
git diff --check
git diff --cached --check
git diff --cached --name-only
```

验收重点：

- 研究文档记录固定 commit 和源码证据。
- `progress.md`、README 路线图与对标结论一致。
- 排障文档记录 clone 失败时的替代研究方法。
- 暂存区只包含本轮文档，不包含其他 agent 的代码文件。

## 9. 排障方法

### 9.1 浅克隆反复 `connection reset`

不要因为 `git clone --depth 1` 失败就放弃或改用不确定的二手文章：

1. GitHub API 获取仓库默认分支和当前 commit SHA。
2. 使用 `/git/trees/{sha}?recursive=1` 获取固定提交的源码树。
3. 通过 raw 内容地址按该 SHA 下载关键文件。
4. 把 commit SHA 写入迭代文档，后续复核同一基线。

### 9.2 产品文档与源码表述不同

产品文档用于确认用户行为，源码用于确认实现边界。涉及 File/Entity、事件续传、上传会话和任务状态时必须交叉验证，不能只根据功能介绍推断内部架构。

### 9.3 对标能力被误写成已实现

所有结论必须明确区分：

- Cloudreve 已实现；
- EasyShare 已实现；
- 建议原型；
- 后续路线。

尤其不能把 Cloudreve 桌面客户端的 CfAPI 特性写成 EasyShare 当前能力。EasyShare 当前仍是 Shell NameSpace + WebDAV，CfAPI 只是 P1 原型建议。

## 10. 结果

本轮形成了比“补功能”更重要的架构排序：先解决稳定文件身份和可靠工作流，再实现回收站、版本、同步根与知识索引。详细结论见 [`../cloudreve-benchmark.md`](../cloudreve-benchmark.md)。
