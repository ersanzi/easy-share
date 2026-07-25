# Cloudreve 对标研究与 EasyShare 演进建议

> 研究日期：2026-07-25
> Cloudreve 基线：`0bb0ab833571d380153edd3529e01a7957b8b4ce`（2026-07-15，`fix(dbfs): thumbnail not generated for shared files (close #3495)`）
> 研究方式：固定提交源码、官方概念文档、桌面客户端文档与 API 文档交叉核验

## 1. 研究目的

EasyShare 是面向普通消费者、并逐步承接企业知识入口的桌面文件产品，不是 Cloudreve 式多租户 Web 网盘。本次对标不追求功能数量，而是识别已经被成熟文件产品验证的架构边界，避免在回收站、版本、同步、搜索和大文件上传阶段重复返工。

核心结论：**学习 Cloudreve 的文件身份、内容实体、能力声明、事件流和任务工作流；不复制它的多租户管理后台、部署拓扑与复杂策略配置。**

## 2. 证据范围

### 2.1 第一轮：预览与工作流

- `README_zh-CN.md`：多存储策略、并行分片上传、媒体元数据、标签搜索、分享、在线预览与 WebDAV 能力总览。
- `application/dependency/dependency.go`：依赖集中装配和能力提供者边界。
- `service/explorer/viewer.go`、`pkg/filemanager/manager/viewer.go`：预览器选择、Viewer Session 与内容访问。
- `pkg/filemanager/manager/thumbnail.go`：缩略图生成、读取和生命周期。
- `pkg/filemanager/manager/recycle.go`：回收站工作流。
- `pkg/filemanager/workflows/upload.go`、`pkg/queue/`：上传工作流、任务调度、进度和持久化。
- `service/share/`：分享访问与分享管理分离。

### 2.2 第二轮：文件身份、同步与目录架构

- 官方概念文档 `usage/concept`：File、Blob、主 Blob、引用计数、不可变内容和 copy-on-write。
- `ent/schema/file.go`、`ent/schema/entity.go`、`inventory/file.go`：逻辑文件与物理内容实体分离。
- `pkg/filemanager/fs/uri.go`：用稳定资源身份表达“我的文件、回收站、分享、搜索”等不同视图。
- `pkg/filemanager/fs/dbfs/upload.go`、`pkg/filemanager/manager/upload.go`、`service/explorer/upload.go`：Upload Session 与逻辑文件记录联动。
- `service/explorer/events.go`、`pkg/filemanager/fs/dbfs/events.go`、`pkg/filemanager/eventhub/`、`ent/schema/fsevent.go`：可恢复的文件事件流。
- `pkg/queue/task.go`、`ent/schema/task.go`、`pkg/filemanager/workflows/`：统一任务状态机、重试、恢复和清理。
- 官方桌面客户端文档：Windows Cloud Files API 同步根、占位文件、按需下载、固定到本机、状态图标和冲突处理。
- 官方元数据、搜索、批量操作、压缩解压和集成文档：索引分层、逐项结果、后台任务和 OAuth scope。

研究结论均绑定上述固定 commit；产品文档只用于解释行为，关键架构判断以源码交叉验证。

## 3. 最重要的架构发现

### 3.1 File 与 Blob/Entity 分离

Cloudreve 把用户可见的逻辑文件与真实存储内容分开：

- `File` 保存名称、目录关系、元数据、分享关系和当前主实体。
- `Entity`/Blob 保存真实内容位置、大小、引用计数、上传会话和存储策略关系。
- Blob 不可变；内容修改时创建新 Blob，再切换 File 的主 Blob。
- 同一 Blob 可被多个逻辑文件引用，从而支持 copy-on-write、版本历史与垃圾回收。

EasyShare 当前的云盘文件以 S3 `key` 同时承担路径、名称、业务身份和物理定位。列表、下载、删除、分享都直接围绕 `key` 工作。这个模型简单，但会同时阻塞：

- 重命名和移动的稳定引用；
- 回收站恢复到原位置；
- 文件版本与历史记录；
- 分享链接在重命名后的有效性；
- 缩略图、知识索引与文件版本绑定；
- 同步事件去重和冲突处理。

**建议：不要一次迁移完整 DBFS，而是先建立本地 SQLite 轻量目录层。**

最小文件记录可包含：

```text
FileRecord
- id              稳定 UUID
- parentId        逻辑父目录
- name            用户可见名称
- objectKey       当前物理对象定位器
- versionId/etag  当前内容版本
- size/contentType
- state           active/recycled/deleted
- createdAt/updatedAt/deletedAt
```

`key` 继续保留用于兼容和对象读取，但不再作为永久业务身份。

### 3.2 稳定 File ID 优先于复杂 URI

Cloudreve 的 URI 抽象可以统一表达我的文件、回收站、分享和搜索视图。EasyShare 目前不需要立即复制完整 URI scheme，但必须先解决稳定身份问题：

- API 响应逐步增加 `fileId`；
- 新增操作优先接受 `fileId`，过渡期允许 `{fileId, key}`；
- 用户可见路径、S3 key 与资源身份分开；
- 分享、任务、事件、缩略图和知识索引统一引用 `fileId`。

可以先定义轻量 `FileRef{ID, Key}`，等回收站、搜索和共享视图增多后再引入统一 URI。

### 3.3 Upload Session 必须与目录记录联动

可恢复上传不只是把 S3 Multipart API 包一层。Cloudreve 在创建上传会话时就把逻辑文件、内容实体、覆盖策略和上传状态连接起来，完成后再原子切换当前内容。

EasyShare 的最小模型建议为：

```text
UploadSession
- id
- fileId
- objectKey
- localPath
- fileSize/modifiedAt/fingerprint
- uploadId/partSize/completedParts
- status
- createdAt/updatedAt
```

要求：

1. `Complete` 幂等，重复提交不产生多个逻辑版本。
2. Core 重启后能校验本地文件指纹与远端分片并恢复。
3. 覆盖现有文件时，成功前不破坏旧版本。
4. part size、并发数、重试与恢复策略由 Core 自动推断，不暴露给普通用户。

因此建议把“轻量目录层”和“Multipart Upload Session”作为同一个 P0 设计落地，避免先做一套完全绑定对象 key 的断点续传后再返工。

### 3.4 文件事件流是同步根的前置能力

Cloudreve 的文件事件 API 使用带单调递增 ID 的 SSE，客户端可通过 `Last-Event-ID` 续传。事件过期或断档时，服务端明确要求客户端全量重扫，而不是静默漏同步。

EasyShare 可先实现本地持久化事件日志，不必第一步就做完整跨设备同步：

```text
FileEvent
- eventId
- fileId
- kind            created/updated/renamed/moved/deleted/recycled/restored
- oldPath/newPath
- version/etag
- occurredAt
```

后续在 loopback API 上提供 SSE。同步客户端发现事件断档时必须重扫目录层，并把“事件续传失败”做成可观测状态。

### 3.5 Windows Cloud Files API 是长期系统入口

Cloudreve 桌面客户端在 Windows 上使用 Cloud Files API，提供同步根、占位文件、按需 hydration、固定到设备、状态图标、上下文菜单和冲突处理。这比 EasyShare 当前 Shell NameSpace + WebDAV 更接近 OneDrive/WPS 云盘的原生体验。

但这是一条高价值、高风险路线：

- 现有 WebDAV 应继续作为跨平台入口和回退能力；
- CfAPI 必须做独立技术原型，不直接替换主链路；
- 原型至少验证同步根注册、1000 个占位文件枚举、双击 hydration、pin/unpin、离线行为、Core 崩溃恢复和 Explorer 重启；
- 原型成功后，再决定 Windows 主入口是否从 WebDAV 迁移到按需文件同步根。

macOS 对等方向是 File Provider，不应把 Windows CfAPI 设计硬套到 Finder WebDAV。

### 3.6 统一任务状态机，而不是继续增加任务类型

Cloudreve 的任务模型统一了 queued、processing、suspended、completed、error、canceled，并包含持久化、重试次数、错误历史、恢复时间、执行时长和 Cleanup 生命周期。

EasyShare 后续会同时出现 LAN 传输、云盘上传/下载、同步、缩略图、解压、知识解析和索引任务。如果每类功能各自定义状态与持久化格式，UI、重试和诊断会迅速失控。

建议共享任务基础模型：

```text
Task
- id/kind/direction
- source/target
- state
- bytesTotal/bytesDone
- retryCount/nextRetryAt
- errorCode/errorMessage
- checkpoint
- createdAt/updatedAt/finishedAt
```

执行器可以独立，但状态机、持久化、取消、重试、进度与错误呈现应共用。

### 3.7 从 `previewKind` 推广为通用能力声明

在线预览已经验证“后端声明能力、前端不猜测”的价值。下一步可演进为明确、可读的能力集合：

```text
capabilities:
- preview
- thumbnail
- download
- share
- delete
- restore
- rename
- move
- pin
```

不建议直接照搬压缩位图或前端扩展名判断。能力集合应由 Core 根据文件状态、内容类型、入口和权限生成，前端仅决定如何呈现。

### 3.8 元数据和搜索要分层

Cloudreve 区分数据库字段搜索、用户标签/元数据、全文索引和当前会话快速过滤。EasyShare 的知识平台也应保持分层：

1. 目录层索引名称、类型、大小、时间和状态；
2. 异步提取标签、媒体元数据和文档结构；
3. 知识平台维护全文/向量索引；
4. 向量索引不是文件目录的唯一真相源，对象存储也不承担查询数据库职责。

这样删除、恢复、版本切换和权限变化才有稳定的联动入口。

## 4. 能力矩阵

| 能力 | EasyShare 当前状态 | 建议 |
| --- | --- | --- |
| 稳定文件身份/目录层 | S3 key 同时承担身份与定位 | **P0**：SQLite 轻量目录层，先引入稳定 `fileId`，保留 key 兼容 |
| 分片/断点上传 | 单请求流式上传 | **P0**：Upload Session + Multipart + 幂等 Complete；与目录层一起设计 |
| 统一任务模型 | LAN 与云盘状态分散 | **P0**：先统一基础字段、持久化和状态机，再扩展执行器 |
| 在线预览 | 图片/PDF/限量 UTF-8 文本已完成 | 已落地；继续使用后端能力声明和短期内容票据 |
| 回收站/版本 | 当前直接删除对象 | **P1**：基于 `fileId` 和版本记录实现，不仅靠隐藏 S3 前缀 |
| 文件事件流 | 无持久化事件日志 | **P1**：单调 event ID、断档重扫、后续 loopback SSE |
| 缩略图 | 通用文件图标 | **P1**：按 fileId + version/ETag 缓存，失败不阻塞列表 |
| Windows 按需文件 | Shell NameSpace + WebDAV | **P1 原型**：保留 WebDAV，独立验证 CfAPI Sync Root |
| 分享生命周期 | 可生成链接，缺少记录管理 | P1/P2：记录、撤销、过期与重命名稳定性统一引用 fileId |
| 搜索/标签/元数据 | 对象列表，无目录索引 | P2：目录字段 → 标签/媒体元数据 → 全文/语义索引 |
| 批量操作 | 未统一 | P2：逐项结果、允许部分失败、可重试 |
| 压缩/解压 | LAN 文件夹传输内置 zip | P2：用户发起的压缩/解压进入统一后台任务 |
| OAuth 集成 | Core 本地 Token，不适合第三方长期使用 | 企业阶段：WPS/第三方应用使用有限 scope，不共享 Core 长期 Token |
| 多存储策略 | RustFS/S3 单后端 | 继续保留 `objectstore.Store` 抽象，需求出现前不做策略配置 UI |

## 5. 推荐演进顺序

### P0：先打稳文件与上传地基

1. 设计 SQLite schema：`files`、`file_versions`、`upload_sessions`、`tasks`。
2. 云盘列表返回 `fileId + key`，保持现有 API 兼容。
3. 实现 Upload Session、Multipart Upload、断点恢复和幂等完成。
4. 统一上传/下载任务的基础状态、进度、取消和错误模型。
5. 故障注入验证 Core 重启、断网、单分片失败、重复 Complete 和本地文件变化。

### P1：把“可恢复”扩展到文件生命周期和系统入口

1. 回收站、恢复、保留期和文件版本记录。
2. 持久化文件事件日志与 loopback SSE。
3. 图片缩略图缓存和通用 capabilities。
4. 分享记录、撤销与过期管理。
5. Windows Cloud Files API 独立原型；不影响现有 WebDAV 入口。

### P2：管理、批处理和知识检索

1. 名称/类型/时间搜索，再扩展标签和媒体元数据。
2. 批量操作返回逐项结果，允许部分失败。
3. 压缩、解压、媒体处理进入统一后台任务。
4. 知识索引通过 `fileId + version` 关联，版本变化触发重建或失效。
5. 仅在真实业务需要时扩展存储适配器和第三方 OAuth。

## 6. 明确不照搬的内容

- 不在消费级桌面阶段引入用户组、复杂配额、管理员后台和公开站点。
- 不一次性迁移到完整 DBFS；先做满足本项目的轻量目录层。
- 不把回收站仅实现为隐藏 S3 前缀，否则恢复、版本、分享和索引仍绑定 key。
- 不让用户选择分片大小、并发数、重试、queryMode、fieldKey 等技术参数。
- 不让前端持有对象存储凭据，也不把长期 Core API Token 放入 URL。
- 不直接暴露第三方预览 URL，不因对标而同时引入 Office 编辑、音视频转码等重型能力。
- 不未经原型验证就用 CfAPI 替换 WebDAV。
- 不把 Cloudreve 的服务端部署拓扑和多租户权限系统塞进桌面 Core。

## 7. 后续验收门槛

任何借鉴的新能力都应满足：

1. 默认自动工作，不新增普通用户难以理解的设置。
2. 文件以稳定 `fileId` 作为业务身份，物理 key 可替换。
3. 能力由 Core 声明，前端只负责呈现和交互。
4. 写操作具备幂等边界，后台失败可重试、可观测、可恢复。
5. 事件断档有明确的全量重扫策略，不能静默丢状态。
6. 长期凭据不进入内容 URL、日志或前端状态。
7. Windows/macOS 系统入口和应用内入口复用同一业务模型。
8. 知识索引绑定文件版本，不能成为文件目录的唯一真相源。

## 8. 最终判断

Cloudreve 最值得 EasyShare 学习的不是“再增加多少功能”，而是三层边界：

1. **稳定身份层**：File 与内容实体分离，路径和对象 key 可变化。
2. **可靠工作流层**：Upload Session、统一任务、事件日志、重试与恢复。
3. **多入口能力层**：应用、WebDAV、CfAPI/File Provider、分享和知识索引都复用同一文件模型。

下一轮实现应优先从 `fileId + 轻量目录层 + Upload Session + 统一任务基础字段` 开始。这四项是回收站、版本、同步根、缩略图、分享生命周期与知识索引共同的前置条件。
