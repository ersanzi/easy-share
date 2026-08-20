# EasyShare 当前架构

> 更新基线：2026-07-26。

## 1. 进程模型

EasyShare 由两个进程组成（Windows 为 .exe，macOS 为无后缀二进制）：

```text
┌─────────────────────────────────────────────────────────────────┐
│ easyshare(.exe)  — Wails 桌面端                                 │
│ main.go + app.go：窗口、托盘、Core 进程管理、WebSocket 事件流    │
│ tray_windows.go / tray_darwin.go：平台托盘                      │
│ frontend/：Vue 3 + TypeScript UI                                │
└───────────────────────────┬─────────────────────────────────────┘
                            │ HTTP 127.0.0.1:19080 / Bearer Token
                            │ WebSocket ws://127.0.0.1:19080/api/events
┌───────────────────────────▼─────────────────────────────────────┐
│ easyshare-core(.exe)  — 后台服务                                │
│ cmd/core/main.go：组装、信号、优雅退出                           │
│ internal/api：HTTP API + WebSocket 事件Hub                      │
│ internal/discovery：UDP 设备发现                                │
│ internal/transfer：TCP 文件传输（含文件夹 zip 管线）            │
│ internal/drive：WebDAV 共享服务                                 │
│ internal/cloud：网盘 API（S3 对象存储）                         │
│ internal/namespace：系统文件入口（此电脑 / Finder 挂载）         │
└───────┬────────────────────┬───────────────────────┬────────────┘
        │ UDP 9527           │ TCP 9528              │ 127.0.0.1:19080
        │ 设备发现           │ 文件传输              │ WebDAV 共享（无认证）
        ▼                    ▼                       ▼
   局域网设备             对端 EasyShare       资源管理器 / Finder
                                                   │ 127.0.0.1:19081
                                                   │ WebDAV 云盘驱动器
                                                   ▼
                                              「此电脑」品牌入口
```

关闭桌面窗口默认隐藏到托盘（OnBeforeClose 拦截），Core 继续运行。托盘菜单"退出"或界面"退出服务"才执行全量关闭。

## 2. 主要代码入口

| 路径 | 职责 |
| --- | --- |
| `main.go` | Wails 窗口创建、Frameless、DragAndDrop、OnBeforeClose 隐藏/退出 |
| `app.go` | 前端桥接、Core 探测/启动、事件流订阅、系统通知、watchdog |
| `tray_windows.go` | Windows 托盘（getlantern/systray + ICO） |
| `tray_darwin.go` + `tray_native_darwin.m` | macOS 菜单栏（NSStatusItem，不接管 AppDelegate） |
| `cmd/core/main.go` | Core 组装、信号监听、优雅退出 |
| `internal/api` | HTTP 路由、WebSocket eventHub、状态、资源清理 |
| `internal/config` | 配置默认值、验证、原子保存、热加载 |
| `internal/desktop` | Core HTTP 客户端（含 WebSocket 订阅）、健康校验、子进程启动 |
| `internal/discovery` | UDP 设备广播与在线列表 |
| `internal/transfer` | TCP 流式发送/接收、文件夹 zip 管线、速度计算 |
| `internal/drive` | WebDAV 服务（本地目录 + S3 云盘两种 FileSystem） |
| `internal/cloud` | 网盘业务层：上传/下载/列表/删除/分享/预览 |
| `internal/cloud/objectstore` | S3 兼容存储抽象（s3store + memory fake） |
| `internal/cloud/webdavfs` | S3-backed WebDAV FileSystem |
| `internal/namespace` | 系统文件入口：Windows Shell NameSpace / macOS Finder 挂载 |
| `internal/fsutil` | 跨平台磁盘/卷枚举、目录列举、打开文件/文件夹 |
| `internal/logging` | 日志目录、追加写入和 5 MiB 轮转 |
| `internal/task` | 传输任务状态机、持久化（终态 JSON 文件） |
| `frontend/src/services/core.ts` | Wails 绑定的前端适配层 |
| `frontend/src/composables/useEasyShare.ts` | 前端状态、实时事件订阅、轮询 fallback |
| `frontend/src/components/` | 概览、设备、传输、网盘、设置 UI |
| `knowledge/` | Python 知识平台服务（FastAPI，独立进程） |

`frontend/wailsjs` 是 Wails 自动生成代码，`wails build` 时自动重生成。

## 3. 默认端口和地址

| 功能 | 默认地址/端口 | 暴露范围 |
| --- | --- | --- |
| Core API + WebDAV 共享 | `127.0.0.1:19080` | 仅本机 |
| 云盘驱动器 WebDAV | `127.0.0.1:19081` | 仅本机 |
| 设备发现 | UDP `9527` | 局域网 |
| 文件传输 | TCP `9528` | 局域网 |

端口由 `%LOCALAPPDATA%\EasyShare\config.json`（macOS: `~/Library/Application Support/EasyShare/config.json`）配置。Core API Host 必须是 loopback 地址。

## 4. Core API

`/health` 使用随机 nonce、Device ID 和 HMAC proof 确认端口上的进程确实是当前配置对应的 EasyShare Core。其余 API 需要：

```http
Authorization: Bearer <apiToken>
```

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `GET` | `/health?nonce=...` | Core 身份和健康检查 |
| `GET` | `/api/status` | Core、发现、接收、WebDAV、云盘状态 |
| `GET` | `/api/peers` | 附近设备 |
| `GET` | `/api/tasks` | 传输任务列表 |
| `GET` | `/api/events` | WebSocket 实时事件流 |
| `POST` | `/api/transfers` | 发起文件发送 |
| `POST` | `/api/transfers/{id}/accept` | 接受接收（可指定 saveDir） |
| `POST` | `/api/transfers/{id}/reject` | 拒绝接收 |
| `POST` | `/api/tasks/clear` | 清除全部任务记录 |
| `DELETE` | `/api/tasks/{id}` | 删除单条任务 |
| `POST` | `/api/drive/start` | 启动 WebDAV 共享 |
| `POST` | `/api/drive/stop` | 停止 WebDAV 共享 |
| `POST` | `/api/config/reload` | 热加载配置 |
| `GET` | `/api/cloud/files` | 网盘文件列表 |
| `GET` | `/api/cloud/preview?key=` | 网盘文件预览 |
| `POST` | `/api/cloud/upload` | 网盘上传（multipart 流式） |
| `POST` | `/api/cloud/download` | 网盘下载（返回预签名 URL） |
| `DELETE` | `/api/cloud/files` | 网盘删除 |
| `POST` | `/api/cloud/share` | 网盘分享链接 |
| `POST` | `/api/shutdown` | 优雅退出全部 Core 服务 |

### WebSocket 事件类型

| type | data | 触发时机 |
| --- | --- | --- |
| `transfer.updated` | Task 对象 | 传输进度/状态变化（100ms 节流） |
| `status.changed` | Status 对象 | 服务启停 |
| `drive.status.changed` | 驱动器状态 | WebDAV 启停 |
| `error` | ErrorResponse | 传输失败等异步错误 |

## 5. 关键生命周期

### 启动

1. 桌面端打开 `desktop.log`，加载或首次生成 `config.json`。
2. 调用带 HMAC proof 的 `/health` 检查现有 Core。
3. 身份匹配则复用；否则启动同目录下的 `easyshare-core(.exe)`（macOS 用 Setsid 独立会话）。
4. 建立 Core API Client，启动 watchdog（5s 心跳，连续 3 次失败重启 Core）。
5. 启动 eventStream：WebSocket 订阅 Core 事件，转发为 Wails `core-event`，断线指数退避重连。
6. 注册系统文件入口（Windows: Shell NameSpace 注册表；macOS: mount_webdav 挂载）。
7. 前端初始化：首次快照 → 订阅 `core-event` 实时事件 → 5s 轮询 fallback。

### 系统文件入口（此电脑 / Finder）

**Windows**：通过注册表在 Shell NameSpace 注册 CLSID，委托文件夹指向 WebDAV UNC（`\\127.0.0.1@19080\DavWWWRoot`），无需盘符映射。云盘驱动器使用独立端口 19081 的 S3-backed WebDAV。

**macOS**：优先使用 `mount_webdav` 命令行挂载到 `/Volumes/EasyShare 网盘`（不弹 GUI 对话框），osascript `mount volume` 仅作兜底。

### 退出全部服务

前端先停止轮询和事件订阅，然后调用 `ShutdownAll`。Core 资源清理顺序：

1. 停止云盘驱动器 WebDAV（:19081）
2. 停止共享 WebDAV（:19080）
3. 取消 Core 后台 context（发现、接收等）
4. 关闭 Core HTTP Server 并退出进程
5. 桌面端设置 quitting=true，调用 runtime.Quit
6. 前端进入"服务已安全退出"状态

托盘退出额外加 3s 超时：Core 无响应时强制退出桌面进程，避免卡死。

## 6. 实时通信架构

```text
Core eventHub (内部广播)
    │
    ▼ WebSocket /api/events
desktop eventStream (Go 协程，指数退避重连)
    │
    ▼ runtime.EventsEmit("core-event", rawJSON)
前端 EventsOn("core-event", handler)
    │
    ├── transfer.updated → 原地更新 tasks 数组
    ├── status.changed / drive.status.changed → 全量 refresh
    └── error → 显示错误提示

5s 轮询 GetSnapshot() 作为 fallback（断线/遗漏兜底）
```

## 7. 状态和持久化

- 配置持久化在 `config.json`，使用临时文件加原子替换写入，支持热加载（`/api/config/reload`）。
- 传输任务：运行中为内存状态；终态（completed/rejected/failed）持久化为 JSON 文件。
- 网盘文件存储在 RustFS（S3 兼容），凭据编译期常量（`internal/cloud/defaults.go`），不暴露给前端。
- peers 是内存状态，Core 退出后丢失。

## 8. 云盘与对象存储

- `internal/cloud` 是网盘业务层，提供上传/下载/列表/删除/分享/预览 API。
- `internal/cloud/objectstore` 定义 provider-neutral 存储接口；`s3store` 使用 AWS SDK v2 连接 RustFS。
- `internal/cloud/webdavfs` 将 S3 对象存储映射为 WebDAV FileSystem，供云盘驱动器使用。
- 上传采用 multipart 流式（真实进度），AWS SDK 非 seekable 流需 `SwapComputePayloadSHA256ForUnsignedPayloadMiddleware`。
- 本地 RustFS 开发环境见 `deploy/rustfs/`，生产启用受 ADR-0006 门禁约束。

## 9. 跨平台抽象

| 能力 | Windows | macOS |
| --- | --- | --- |
| 托盘 | getlantern/systray（ICO） | NSStatusItem（tray_native_darwin.m） |
| 文件入口 | Shell NameSpace 注册表 | mount_webdav / osascript |
| 打开文件 | `explorer /select,` | `open` |
| 磁盘枚举 | kernel32 GetLogicalDrives | syscall.Statfs /Volumes |
| Core 进程 | 同目录子进程 | Setsid 独立会话 |
| 构建 | `wails build` + NSIS | GitHub Actions macos-latest |

平台拆分通过 build tags 实现：`*_windows.go` / `*_darwin.go`。Wails macOS 版不可从 Windows 交叉编译（WebKit/CGO）。

## 10. 安全边界

- Core API 和 WebDAV 只监听 loopback，无外部网络暴露。
- Core API 使用随机 Token；健康检查同时验证 Device ID 和 HMAC proof。
- WebDAV 无认证（仅回环，无需认证）。
- 云端凭据编译期固定（`internal/cloud/defaults.go`），不暴露给前端或 Shell 扩展。
- UDP 发现和 TCP 文件传输面向可信局域网，无端到端加密或设备配对。

## 11. 知识平台（独立服务）

`knowledge/` 是 Python FastAPI 服务，独立于 Go 双进程运行（架构与运行说明见 [`../knowledge/README.md`](../knowledge/README.md)，方向见 [`knowledge-platform.md`](knowledge-platform.md)）：

- 文档解析管线：TXT/Markdown/DOCX/PDF/XLSX/PPTX 与 PNG/JPEG/BMP/TIFF 统一解析（含 Office OLE/OOXML 真实格式预检、可选 PaddleOCR 页级识别）→ 结构化清洗 → Markdown 渲染 → 结构感知切块（标题边界 + 层级上下文 + 表格完整性）→ 版本化索引；PDF 走三层解析路由（pdf-inspector 快路由 → MinerU 深度解析 → 本地管线兜底，全部默认关闭、逐级回退，manifest `parsing` 字段留痕）
- 向量检索：阿里云百炼 DashScope qwen3.7-text-embedding（1024 维），未配置时退回 HashEmbedder（仅跑通流程），供应商故障时自动降级 BM25；向量库支持 Milvus Standalone（docker-compose 部署，IVF_FLAT + COSINE）或 JSON 文件存储（开发/测试），由 `MILVUS_URI` 配置切换
- LLM 生成：SenseNova deepseek-v4-flash（推理模型），未配置时 /query 降级为纯检索；chunk 携带入库时间（`ingested_at`），prompt 注入文档时间并指示优先依据较新文档、对可能过时的内容提示时效
- OCR 能力：`/health` 声明 provider/availability/reason/formats；manifest 记录 OCR 页、失败页、低置信度块和耗时；`/query` contexts 返回块 ID、来源位置、提取方式和入库时间
- 检索质量评测：`tests/retrieval/` 标注集 + recall@5 / MRR 基线进入 pytest 回归
- 对象存储：复用 RustFS；派生产物写 `derived/{fileId}/{versionId}/`
- 凭据存 `knowledge/.env`（.gitignore 已排除）
- `/lab` 本地实验台（仅回环）：上传观察八阶段处理 + 检索问答（引用溯源 clean.md）
- MCP Server（可选，`pip install mcp` 后 `python -m app.mcp_server`）：stdio 薄桥转发 `/query` 与 `/health`，任何 AI 工具（Claude Code/Cursor/OA 助手）可检索企业知识
- 账号与登录（薄控制面 2a，`AUTH_ENABLED` 默认关闭）：SQLite 用户库 + PBKDF2，`/auth/bootstrap`（首管理员）/`/auth/login`（Bearer 令牌）/`/auth/users`（管理员）；启用后 `/documents`、`/query`、`/ingest`、`/lab/api/uploads` 需令牌（GET 支持 `?token=`），`/auth`、`/health`、`/lab`、`/debug` 白名单；/lab 内置登录条
- 目录监听自动入库（`WATCH_DIRS` 分号分隔，默认关闭）：轮询扫描（SMB 共享盘可靠）+ mtime 稳定性窗口 + 内容哈希版本去重 + 失败自动重试；与 lab 上传同链路（storage → job → pipeline）
