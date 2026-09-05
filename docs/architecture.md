# EasyShare 当前架构

> 更新基线：2026-09-06（云盘 P0 三片 + 全局快捷搜索第二面板表面）。

## 1. 进程模型

EasyShare 由两个进程组成（Windows 为 .exe，macOS 为无后缀二进制）；账号控制面（RuoYi Java）与知识服务是独立部署的服务端进程：

```text
┌─────────────────────────────────────────────────────────────────┐
│ easyshare(.exe)  — Wails 桌面端                                 │
│ main.go + app.go：窗口、托盘、悬浮窗、Core 进程管理、事件流      │
│ spacemount.go + internal/spacedav：空间 WebDAV（19082/19083）， │
│   登录后挂「此电脑」个人/共享盘，每个文件操作经控制面            │
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
│ internal/knowledge：知识网关（登录/问答代理，会话落盘）         │
└───────┬────────────────────┬───────────────┬────────────┬──────┘
        │ UDP 9527           │ TCP 9528      │ 127.0.0.1:19080 │ HTTP（公司内网）
        │ 设备发现           │ 文件传输      │ WebDAV 共享（无认证） │ 知识服务（FastAPI）
        ▼                    ▼               ▼                ▼
   局域网设备             对端 EasyShare  资源管理器 / Finder  知识服务器（各公司部署）
```

关闭桌面窗口默认隐藏到托盘（OnBeforeClose 拦截），Core 继续运行。托盘菜单"退出"或界面"退出服务"才执行全量关闭。

**账号控制面（RuoYi-Vue-Plus 6.0，Java，服务端部署）**：桌面端与 Core 均为其客户端。登录经控制面拿 JWT；云盘上传/下载由控制面模块 `platform-drive/` 验证身份后签发短期预签名 URL，客户端凭 URL 直传 RustFS，**不再持有任何对象存储静态凭据**（ADR-0007 不变量 1/3，`internal/cloud/defaults.go` 已删除）。2026-09-06 起共享空间授权主体泛化为**账号/部门**（es_space_member.member_type，部门来源 sys_dept 只读投影，生效权限=个人行∨部门行取宽；DDL 见 `deploy/ruoyi-db/easyshare-space-member-type.sql`）；同日 `platform-drive/` 增设 **es_file 目录层**：云盘列表响应带稳定 `fileId`（存量对象首次列举惰性补账）、presignPut 幂等登记、es_file 另有 `visible_depts` 文档级可见性（共享列表列举出口统一过滤，`POST /easyshare/drive/file-visibility` 设置）；删除支持按 fileId 或路径（过渡期双轨；DDL 见 `deploy/ruoyi-db/easyshare-file.sql`）。同日增设 **es_upload_session 上传会话**：`/easyshare/drive/upload-session/create|part|complete|abort` 四端点支撑 Multipart 断点续传（幂等 Complete、分片大小服务端定默认 8MB；DDL 见 `deploy/ruoyi-db/easyshare-upload-session.sql`，客户端会话快照在数据根 `upload-sessions/`）。客户端在线升级的版本清单与安装包也托管在控制面（匿名 `/easyshare/app/*`，安装包存 RustFS `releases/{version}/` 前缀、预签名直传，见第 4b 节）。环境组成：RuoYi admin（REST :8090）+ PostgreSQL 16（:5433，本机原生 PG 占用 5432 故容器映射 5433）+ Redis 7（:6380）+ plus-ui 管理后台（:8091，次要出口），dev 部署见 `deploy/ruoyi-db/`；**生产 Linux 服务器**部署见 `deploy/server-linux/`（RuoYi 容器化 temurin 21-jre + 知识服务 systemd，LAN 暴露 8000/8090/9000；控制面默认地址可构建期注入客户端：`build.ps1 -PlatformUrl`）。

## 2. 主要代码入口

| 路径 | 职责 |
| --- | --- |
| `main.go` | Wails 窗口创建、Frameless、DragAndDrop、OnBeforeClose 隐藏/退出 |
| `app.go` | 前端桥接、Core 探测/启动、事件流订阅、系统通知、watchdog |
| `tray_windows.go` + `tray_hover_windows.go` | Windows 托盘（原生 `Shell_NotifyIcon` + `NOTIFYICON_VERSION_4`，systray 已移除）与托盘悬停浮窗（独立线程 Win32 窗口内嵌 WebView2） |
| `tray_darwin.go` + `tray_native_darwin.m` | macOS 菜单栏（NSStatusItem，不接管 AppDelegate） |
| `cmd/core/main.go` | Core 组装、信号监听、优雅退出 |
| `internal/api` | HTTP 路由、WebSocket eventHub、状态、资源清理 |
| `internal/config` | 配置默认值、验证、原子保存、热加载 |
| `internal/desktop` | Core HTTP 客户端（含 WebSocket 订阅）、健康校验、子进程启动 |
| `internal/discovery` | UDP 设备广播与在线列表 |
| `internal/transfer` | TCP 流式发送/接收、文件夹 zip 管线、速度计算 |
| `internal/drive` | WebDAV 服务 + 控制面云盘客户端（预签名 URL 上传/下载/列表，`client.go`/`upload.go`） |
| `internal/account` | 控制面账号客户端：登录态、用户/空间/配额管理（P1/P3） |
| `internal/spacedav` | 空间 WebDAV 文件系统（P4 挂载的服务层）：建在 `internal/drive` 客户端之上，每个操作经控制面，配额与共享授权对资源管理器同样生效 |
| `internal/winui` | Win32 窗口几何与工具（悬停浮窗定位/多显示器适配） |
| `spacemount.go` | 桌面端空间挂载（P4：登录后挂「此电脑」个人/共享盘、换账号重挂）与浮窗拖放目标 |
| `appupdate.go` + `internal/update` | 客户端在线升级（检查/下载/SHA256 校验/静默安装，升级源为控制面，见第 4b 节） |
| `internal/cloud` | 网盘视图类型与预览辅助（`File`/`Preview`/预览分类/文本限量内联），供桌面端 Wails 绑定使用；Core 直连 S3 的 Service 与路由已删（KI-5） |
| `internal/cloud/objectstore` | S3 兼容存储抽象（s3store + memory fake，`scripts/create_bucket.go` 等控制面之外的用途仍在用） |
| `internal/namespace` | 系统文件入口：Windows Shell NameSpace / macOS Finder 挂载（双平台同一 `SpaceEntries` 模型） |
| `internal/fsutil` | 跨平台磁盘/卷枚举、目录列举、打开文件/文件夹 |
| `internal/logging` | 日志目录、追加写入和 5 MiB 轮转 |
| `internal/task` | 传输任务状态机、持久化（终态 JSON 文件） |
| `frontend/src/services/core.ts` | Wails 绑定的前端适配层 |
| `frontend/src/composables/useEasyShare.ts` | 前端状态、实时事件订阅、轮询 fallback |
| `frontend/src/components/` | 概览、设备、传输、网盘、设置 UI |
| `knowledge/` | Python 知识平台服务（FastAPI，独立进程） |
| `platform-drive/` | RuoYi 控制面内的云盘存储授权模块（顶层独立 Maven 模块，父 POM 指向 gitignore 的 `platform/` RuoYi 源码工程） |

`frontend/wailsjs` 是 Wails 自动生成代码，`wails build` 时自动重生成。

## 3. 默认端口和地址

| 功能 | 默认地址/端口 | 暴露范围 |
| --- | --- | --- |
| Core API + WebDAV 共享 | `127.0.0.1:19080` | 仅本机 |
| 空间 WebDAV·个人盘 | `127.0.0.1:19082`（WebDAVPort+2，桌面端进程；刻意跳过 +1，旧版 Core 的已废弃云盘 WebDAV 占过 19081） | 仅本机 |
| 空间 WebDAV·共享盘 | `127.0.0.1:19083`（WebDAVPort+3，桌面端进程） | 仅本机 |
| 设备发现 | UDP `9527` | 局域网 |
| 文件传输 | TCP `9528` | 局域网 |
| 账号控制面 REST（RuoYi admin） | `http://localhost:8090`（`config.json` platformBaseUrl） | 服务端（dev 本机） |
| plus-ui 管理后台（次要出口） | `http://localhost:8091`（adminConsoleUrl） | 服务端（dev 本机） |
| 控制面 PostgreSQL 16 | `127.0.0.1:5433`（docker compose；本机原生 PG 占用 5432，宿主映射 5433） | 仅本机（dev） |
| 控制面 Redis 7 | `127.0.0.1:6380`（docker compose，专用实例） | 仅本机（dev） |

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
| `GET` | `/api/knowledge/status` | 知识服务登录态（本地，无网络探测） |
| `POST` | `/api/knowledge/login` | 登录知识服务（代理远端 `/auth/login`，成功后 Core 落盘会话） |
| `POST` | `/api/knowledge/logout` | 清空知识登录会话 |
| `POST` | `/api/knowledge/health` | 探测知识服务健康度（文档规模/LLM 状态） |
| `POST` | `/api/knowledge/query` | 知识问答代理（解除 30s 写超时，120s 上下文兜底；`mode=search` 仅检索不生成，供全局快搜） |
| `POST` | `/api/shutdown` | 优雅退出全部 Core 服务 |

## 4b. 控制面升级接口（platform-drive，2026-08-31 起）

客户端在线升级（`internal/update` + `appupdate.go`）。升级检查先于登录发生，故前两个端点**匿名**（`security.excludes` 白名单放行，`@SaIgnore` 只是注解层保险；安装包本就公开）；管理端点要求 superadmin。安装包本体存 RustFS `releases/{version}/` 前缀，控制面不在数据路径上。

| 方法 | 路径 | 鉴权 | 用途 |
| --- | --- | --- | --- |
| `GET` | `/easyshare/app/latest?platform=windows\|macos` | 匿名 | 最新版本清单（version/notes/publishedAt/assets；从未发布返回 data=null） |
| `GET` | `/easyshare/app/assets/{assetId}/url` | 匿名 | 现取预签名下载 URL（GET 10m，客户端下载前调用） |
| `POST` | `/easyshare/app/admin/uploads` | superadmin | 上传准备：建/复用版本与资产记录，返回预签名 PUT URL（两段式，发布方直传 RustFS） |
| `POST` | `/easyshare/app/admin/assets/{assetId}/publish` | superadmin | 发布：校验对象存在且大小一致后置已发布 |
| `GET` | `/easyshare/app/admin/releases` | superadmin | 版本列表（管理/回滚决策） |
| `DELETE` | `/easyshare/app/admin/releases/{releaseId}` | superadmin | 删除版本（回滚：记录+对象一并删） |

桌面端升级链路：启动 24h 节流自动检查（`update-state.json`）→ 设置页「关于与更新」手动检查 → 下载（SHA256 校验，`%LOCALAPPDATA%\EasyShare\updates\`）→ Windows 安装版「重启并更新」（`installer /S /update` → 优雅停 Core → NSIS taskkill 残留 → 覆盖安装 → 自动重启）；macOS 与绿色版仅引导下载。发布入口 `scripts/publish-release.ps1`。

## 4b-2. 租户服务配置下发（2026-09-06 起）

「客户端只预知控制面一个地址，其余服务地址登录后下发」的租户服务发现（`ServiceConfigController.java`，客户端 `internal/account/serviceconfig.go` + App 绑定 `ServiceEndpoints`/`SaveServiceConfig`）。存储复用 RuoYi `sys_config`（键 `drive.service.knowledge.url`，**零 DDL**），读写在模块内闭环（不经 RuoYi 配置缓存，口径自洽）。当前下发项：知识服务地址。客户端未登录/未登记时回退**同主机推导**（控制面 `:8090` → 知识服务 `:8000`，「知识」页自动填好，员工免手填）；管理页「服务配置」卡片登记（superadmin）。

| 方法 | 路径 | 鉴权 | 用途 |
| --- | --- | --- | --- |
| `GET` | `/easyshare/service/config` | 登录 | 下发服务配置（未登记项空串，客户端回退推导） |
| `PUT` | `/easyshare/service/config` | superadmin | 登记知识服务地址（空串=清除，回退推导；自动 strip 尾斜杠/校验 http 前缀） |

## 4c. 插件系统与商城接口（2026-08-31 起）

**插件形态**：Web 插件包（zip：`manifest.json` + HTML/JS/CSS），UI 跑在桌面端前端 `<iframe sandbox="allow-scripts">`（opaque origin），经 postMessage RPC 调用宿主能力；公共 SDK 由宿主统一 serve（`/plugins/_sdk/eshare.js`）。**剪切板为首发普通插件**（2026-09-01 起，可卸载可禁用；此前为内置不可卸载，用户明确「插件即可安装可卸载」后改造）：源码在插件工程 `plugins/clipboard/`，主仓 `//go:embed all:plugins/clipboard` 直读，经 `SeedPlugin` 做**首次运行种子**——落成普通插件（登记 builtin=false），之后更新走商城流程（含权限确认），卸载后不复活（`plugins-seeded.json` 标记）；录制服务与快捷面板随插件在场状态启停（卸载即停记录、销毁面板、释放热键）。宿主侧监听在 `internal/clipboard`（Windows `AddClipboardFormatListener` + message-only 窗口；macOS NSPasteboard changeCount 800ms 轮询，2026-09-01 起；文本/图片(DIBV5/TIFF→PNG)/文件路径，hash 去重、来源进程/应用名、Windows 侧排除密码管理器标记格式）。

**运行时边界**：插件能力调用走唯一动态通道 `PluginInvoke(pluginId, api, argsJSON)`（`appplugin.go`），Go 侧能力注册表（`internal/plugin/registry.go`）按 manifest `permissions` 鉴权——新增能力不改 Wails 绑定。首发能力：`storage.*`（按插件隔离 KV）、`clipboard.history/delete/clear/write/settings`、`clipboard.events`（变更推送）、`notification.show`、`drive.upload`（文本上传个人空间，走统一任务通道）。能力另含 `clipboard.stats`（分类计数，2026-09-01）。静态资源由 Wails AssetServer fallback Handler serve（`/plugins/{id}/...` 映射 `%LOCALAPPDATA%\EasyShare\plugins\{id}\`；`/clipboard-files/` 给剪切板图片）。**快捷面板**（2026-09-01，`panel_surface.go` + 平台实现 `panel_windows.go`/`panel_darwin.go`）：全局热键（Win+V，被占则依次回退 Win+Shift+V / Ctrl+Shift+V / Alt+V；macOS ⌘⇧V）唤起独立小窗（Win32+WebView2 / NSPanel+WKWebView），加载同一插件页 `?panel=1` 紧凑形态；面板静态资源走桌面端进程临时 loopback 监听（`127.0.0.1:0`，复用同一 asset mux，仅回环）；SDK 经原生通道（WebView2 postMessage 字符串 / WKWebView script handler）走同一 `PluginInvoke` 鉴权路径；**面板内成功的 clipboard.write = 选中条目：收起面板并把焦点切回唤起前窗口合成 Ctrl+V / ⌘V 粘贴**（macOS 合成按键需辅助功能授权，未授权降级仅复制）；失焦、Esc（`eshare.window.dismiss()`）或再按热键收起。安装：SHA256 校验 → 解压临时目录（zip-slip 防护）→ 原子换入 → `plugins.json` 登记；包上限 50MB。

**商城（官方自营）**：插件/版本/资产放 PG（`es_plugin` / `es_plugin_release` / `es_plugin_release_asset`），zip 本体存 RustFS `plugins/{pluginId}/{version}/`，发布走与在线升级相同的两段式预签名直传。发布入口 `scripts/publish-plugin.ps1`（插件源码目录 `plugins/`）。

| 方法 | 路径 | 鉴权 | 用途 |
| --- | --- | --- | --- |
| `GET` | `/easyshare/plugins` | 匿名 | 商城列表（全部有已发布版本的插件，各带最新版） |
| `GET` | `/easyshare/plugins/{pluginId}/latest` | 匿名 | 单插件最新清单（客户端检查更新） |
| `GET` | `/easyshare/plugins/assets/{assetId}/url` | 匿名 | 现取预签名下载 URL（GET 10m） |
| `POST` | `/easyshare/plugins/admin/uploads` | superadmin | 上传准备：upsert 插件登记 + 建版本与资产，返回预签名 PUT URL |
| `POST` | `/easyshare/plugins/admin/assets/{assetId}/publish` | superadmin | 发布：校验对象存在且大小一致后置已发布 |
| `GET` | `/easyshare/plugins/admin/releases?pluginId=` | superadmin | 版本列表（下架决策） |
| `DELETE` | `/easyshare/plugins/admin/releases/{releaseId}` | superadmin | 下架版本（已装客户端不受影响） |

前端入口：侧边栏「插件中心」（商城 tab + 已装管理）+ 已启用插件的动态导航项（视图名 `plugin:{id}`）；设置页有插件管理卡与「从 zip 安装」。

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

桌面端与 Core 各贡献一部分入口，全部指向本机 WebDAV：

- **局域网共享**（Core，:19080 常驻）：Windows 通过注册表在 Shell NameSpace 注册 CLSID，委托文件夹指向 WebDAV UNC（`\\127.0.0.1@19080\DavWWWRoot`），无需盘符映射；macOS 优先 `mount_webdav` 挂载（不弹 GUI 对话框），osascript 兜底。
- **云端空间盘**（P4，桌面端进程 ：19082/:19083）：登录后按账号**实际拥有的空间**挂载——个人盘条目名为「<昵称> 的网盘」，共享盘为「EasyShare 共享」（只读授权也挂，但 WebDAV 层拒写）。退出登录、配额收回或共享授权撤销时条目随之卸载；数据全部经控制面（`internal/spacedav`），配额与授权对资源管理器同样生效。旧 19081 云盘驱动器（bucket 根挂载点）已随 P2 永久下线。

### 退出全部服务

前端先停止轮询和事件订阅，然后调用 `ShutdownAll`。Core 资源清理顺序：

1. 停止共享 WebDAV（:19080）
2. 取消 Core 后台 context（发现、接收等）
3. 关闭 Core HTTP Server 并退出进程
4. 桌面端设置 quitting=true，调用 runtime.Quit（桌面进程退出时空间 WebDAV :19082/:19083 随之关闭）
5. 前端进入"服务已安全退出"状态

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
- 网盘文件存储在 RustFS（S3 兼容），凭据只在控制面（`deploy/rustfs/.env` → RuoYi 进程环境），客户端经预签名 URL 直传，不暴露给前端。
- peers 是内存状态，Core 退出后丢失。
- 知识服务登录态持久化在 `knowledge.json`（与 config.json 同目录，仅 Core 读写）：桌面进程保存设置时整份回写旧 config.json，令牌若存其中会被冲掉，故独立成文件。令牌不出 Core，前端只见登录态视图（`internal/knowledge`）。
- 插件系统（桌面端进程，见 §4c）：登记表 `plugins.json`、插件包 `plugins/{id}/`、插件私有 KV `plugins-data/{id}.json`（均在 config.json 同目录）；剪切板历史 `clipboard/history.jsonl`（追加写 + 环形截断默认 1000 条）与图片 `clipboard/files/{sha256}.png`（默认 200MB LRU），设置在 `clipboard/settings.json`。

## 8. 云盘与对象存储

**现行链路（ADR-0007，P2 起）**：桌面端 `internal/drive` 持控制面 JWT 调 `platform-drive/` 换短期预签名 URL（PUT 15m / GET 10m），直传/直取 RustFS；对象键强制落在 `users/{userId}/`（个人）或 `shared/`（共享）命名空间，服务端拒绝跨用户 key。配额与池上限（物理容量探测、预留水位、两种"满"分开报错）见 ADR-0007「空间授权与配额」。

- `/api/cloud/*` 七条路由与 `cloud.Service`、`webdavfs`、desktop.Client 的 Cloud* 方法已删除（KI-5 已关闭）：P4 空间挂载走 `internal/spacedav`（建在 `internal/drive` 之上），不存在"不经控制面的云盘 WebDAV"路径，隔离边界不再有死代码可绕。
- `internal/cloud` 仅保留前端契约类型与预览辅助；`internal/cloud/objectstore` 的 provider-neutral 存储抽象（s3store + memory fake）仍在服役（控制面之外的用途）。
- 本地 RustFS 开发环境见 `deploy/rustfs/`，生产启用受 ADR-0006 门禁约束；凭据真值只在 `deploy/rustfs/.env`（gitignore），由 `run-ruoyi-admin.ps1` 注入控制面进程。

## 8a. 知识网关（桌面端 ↔ 知识服务）

- `internal/knowledge` 是知识服务的 HTTP 客户端与会话存储；Core 经 `/api/knowledge/*` 代理登录/健康探测/问答，是桌面端与后续 WPS/Shell 扩展访问知识库的唯一通道（服务器地址在登录页配置，令牌不进前端）。
- 问答代理 handler 解除 Core 全局 30s 写超时（`http.NewResponseController`），120s 上下文兜底；desktop.Client 对问答走无客户端级超时的专用 HTTP client。

## 9. 跨平台抽象

| 能力 | Windows | macOS |
| --- | --- | --- |
| 托盘 | 原生 Shell_NotifyIcon（ICO，NOTIFYICON_VERSION_4 + 悬停浮窗） | NSStatusItem（tray_native_darwin.m） |
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
- 对象存储静态凭据只存在于控制面（RuoYi）进程，客户端二进制经预签名 URL 访问（ADR-0007 不变量 1，KI-2 已关闭）。
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
- 权限感知检索（2b/2c，2026-09-01）：文档归属 `owner`（用户名）贯通 `processing_jobs` 表、任务响应、manifest 与索引 chunk metadata；入库归属以**令牌用户为准**（lab 上传谁传归谁；`/documents/process` 带令牌时忽略请求显式 owner，防伪造；watcher 监听目录与无令牌调用落 None=共享）；`/query` 服务端按令牌计算可见集——owner 为 None/缺失的文档共享给所有人（存量数据天然共享），非空仅本人与 admin 可见，与请求显式 `doc_ids` 求交集（空交集短路返回无结果，不进检索层）；未登录不过滤（行为不变）；可见集数据源为向量库 `doc_owners()`（JSON/Milvus 双实现）；Core 知识网关已透传 Bearer 令牌，桌面端/WPS 问答链路零改动生效；`/debug/query` 驾驶舱不做权限过滤（回环管理员视角）
- 生产检索编排（2026-09-01）：`/query` 由 `QueryOrchestrator`（`app/rag/orchestrator.py`）按 `QUERY_STRATEGY` 调度——`vector`（旧单路，embedding 故障自动降级 BM25）/ `hybrid`（默认；向量+BM25 RRF 融合）/ `hybrid_rerank`（融合池 ×3 后 Cross-Encoder 精排，未配置 rerank 等价 hybrid）/ `multi_hop`（分轮混合+LLM 充分性裁判，需 LLM，未配置自动降级 hybrid_rerank，响应 `degraded` 字段说明）；部署级配置不暴露给终端用户，非法值启动即报错；响应新增 `strategy`（实际执行策略）字段；生产查询/生成事件落 `QueryLog`（逐句忠实度仅驾驶舱审计计算，生产传 None）；向量库双后端实现 `count()`/`snapshot_records()` 协议供 BM25 懒构建与健康度聚合（修复 Milvus 后端无 `records` 属性导致的崩溃）
- 目录监听自动入库（`WATCH_DIRS` 分号分隔，默认关闭）：轮询扫描（SMB 共享盘可靠）+ mtime 稳定性窗口 + 内容哈希版本去重 + 失败自动重试；与 lab 上传同链路（storage → job → pipeline）
- WPS 加载项（`/wps/ribbon.xml`、`/wps/index.html`、`/wps/taskpane.html`）：知识服务自托管加载项页面，与 `/auth`、`/query` 同源免跨域；WPS 文字功能区「知识」页签一键查选中段落，任务窗格内登录（令牌存窗格 localStorage）。本机安装/卸载：`knowledge/scripts/install_wps_addon.ps1`（登记 `%APPDATA%\kingsoft\wps\jsaddons\jsplugins.xml`）
