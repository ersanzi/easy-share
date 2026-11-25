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

**账号控制面（RuoYi-Vue-Plus 6.0，Java，服务端部署）**：桌面端与 Core 均为其客户端。登录经控制面拿 JWT；云盘上传/下载由控制面模块 `platform-drive/` 验证身份后签发短期预签名 URL，客户端凭 URL 直传 RustFS，**不再持有任何对象存储静态凭据**（ADR-0007 不变量 1/3，`internal/cloud/defaults.go` 已删除）。2026-09-06 起共享空间授权主体泛化为**账号/部门**（es_space_member.member_type，部门来源 sys_dept 只读投影，生效权限=个人行∨部门行取宽；DDL 见 `deploy/ruoyi-db/easyshare-space-member-type.sql`）；同日 `platform-drive/` 增设 **es_file 目录层**：云盘列表响应带稳定 `fileId`（存量对象首次列举惰性补账）、presignPut 幂等登记、es_file 另有 `visible_depts` 文档级可见性（共享列表列举出口统一过滤，`POST /easyshare/drive/file-visibility` 设置）；删除支持按 fileId 或路径（过渡期双轨；DDL 见 `deploy/ruoyi-db/easyshare-file.sql`）。2026-10-16 起删除改**软删**（置 `state='recycled'` + `origin_path` 快照，对象保留），回收站三端点 `/recycle`｜`/recycle/restore`｜`/recycle/purge` 按 fileId 定位（DDL 见 `deploy/ruoyi-db/easyshare-file-recycle.sql`）；同日增设 **`POST /easyshare/drive/move`**：改名/移动走服务端 `CopyObject` + 删源，**字节不出对象存储、fileId 不变**（WebDAV 层优先用它，退化路径仍为「下载+上传+删除」）。列举出口（`GET /objects`）**同时**按 state 隐藏软删对象并跳过其惰性补账；下载签发口（`POST /presign-get`）与列举**共用同一份可见性判据**（回收站不可签、共享空间文档级可见性对下载同样生效）。详见 [`known-issues.md`](known-issues.md) KI-10 / KI-11。同日增设 **es_upload_session 上传会话**：`/easyshare/drive/upload-session/create|part|complete|abort` 四端点支撑 Multipart 断点续传（幂等 Complete、分片大小服务端定默认 8MB；DDL 见 `deploy/ruoyi-db/easyshare-upload-session.sql`，客户端会话快照在数据根 `upload-sessions/`）。2026-10-18 起支持**新建文件夹**：对象存储没有目录实体，目录落地为「键以 / 结尾的 0 字节标记对象」——`POST /easyshare/drive/mkdir` 创建（重复创建幂等，与本地文件系统行为对齐；不写 es_file 行，删除即直接删对象、**不进回收站**，`DELETE /object` 对无 active 行的对象直接删对象）；`GET /objects` 增加 `includeDirs` 参数（默认 false）让目录标记参与列举，**只有网盘页传 true**——挂载盘 / WebDAV / 快搜继续走默认列举，不会多出一种 0 字节「文件」；目录标识就是路径尾斜杠，服务端无 isDir 字段，前端 `frontend/src/utils/driveFolder.ts` 同款判断（该模块本就为目录占位预留了识别，本次补全的是控制面一侧）。非空文件夹暂不支持删除（递归删子树的事务边界未解决，前端拒绝并提示先清空）。客户端在线升级的版本清单与安装包也托管在控制面（匿名 `/easyshare/app/*`，安装包存 RustFS `releases/{version}/` 前缀、预签名直传，见第 4b 节）。环境组成：RuoYi admin（REST :8090）+ PostgreSQL 16（:5433，本机原生 PG 占用 5432 故容器映射 5433）+ Redis 7（:6380）+ plus-ui 管理后台（:8091，次要出口），dev 部署见 `deploy/ruoyi-db/`；**生产 Linux 服务器**部署见 `deploy/server-linux/`（RuoYi 容器化 temurin 21-jre + 知识服务 systemd，LAN 暴露 8000/8090/9000；控制面默认地址可构建期注入客户端：`build.ps1 -PlatformUrl`）。

## 2. 主要代码入口

| 路径 | 职责 |
| --- | --- |
| `main.go` | Wails 窗口创建、Frameless、DragAndDrop、OnBeforeClose 隐藏/退出 |
| `app.go` | 前端桥接、Core 探测/启动、事件流订阅、系统通知、watchdog |
| `tray_windows.go` + `tray_hover_windows.go` + `desktop_pet_windows.go` | Windows 原生托盘菜单与常驻圆角桌面宠物；宠物/展开WebView2面板同属独立UI线程，托盘悬停不再弹富窗，双击固定面板仍可拖动，服务器上传复用原任务通路 |
| `tray_darwin.go` + `tray_native_darwin.m` | macOS 菜单栏（NSStatusItem，不接管 AppDelegate） |
| `cmd/core/main.go` | Core 组装、信号监听、优雅退出 |
| `internal/api` | HTTP 路由、WebSocket eventHub、状态、资源清理 |
| `internal/config` | 配置默认值、验证、原子保存、热加载 |
| `internal/desktop` | Core HTTP 客户端（含 WebSocket 订阅）、健康校验、子进程启动 |
| `internal/discovery` | UDP 设备广播与在线列表 |
| `internal/transfer` | TCP 流式发送/接收、文件夹 zip 管线、速度计算 |
| `internal/drive` | WebDAV 服务 + 控制面云盘客户端（预签名 URL 上传/下载/列表，`client.go`/`upload.go`） |
| `internal/account` | 控制面账号客户端：登录态、用户/空间/配额管理（P1/P3）、成员功能权限/权限角色与个人资料（`permission.go`/`profile.go`，2026-10-27）；会话落盘用户级 `account-session.json`（0600，不含密码），启动恢复并校验，401 时清空退回登录页 |
| `internal/spacedav` | 空间 WebDAV 文件系统（P4 挂载的服务层）：建在 `internal/drive` 客户端之上，每个操作经控制面，配额与共享授权对资源管理器同样生效 |
| `internal/winui` | Win32 窗口几何与工具（悬停浮窗定位/多显示器适配） |
| `spacemount.go` + `cfmount_app_windows.go` | 登录态与平台挂载编排：Windows 个人CFAPI、非Windows原空间通路、悬浮窗空间目标 |
| `internal/cfapi` + `internal/cfmount` | 按需读取、持久同步根、条件写回、受保护目录投影及已验证缓存回收 |
| `internal/shellroot` + `internal/shellstatus` + `internal/explorerview` | 完整WinRT提供方展示、原生volatile活动/进度、云盘专属状态图标/默认详细视图；生命周期不破坏性注销根，不读取正文来猜状态 |
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
| `engine/` | **minutecraft**：转写文本 → 会议纪要的通用引擎（独立可安装包，`pip install -e engine`）。负责 LLM 结构化产出（纪要/要点/待办/章节/关键词/洞察/核心发言/可视化汇报卡）、导图 harness、钉钉版式 Word 导出；**不碰音频**。被知识服务（`app/meetings/*` 薄壳转发）与外部 Agent（CLI / Python 库）共同消费，详见 `engine/README.md` |
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
| `GET` | `/api/tasks` | 当前账号云任务与设备级局域网任务 |
| `POST` | `/api/tasks` | 创建任务；云任务归属由服务端核验账号生成 |
| `PATCH` | `/api/tasks/{id}` | 仅修改当前账号可见任务，归属字段不可变 |
| `POST` | `/api/tasks/check-path` | 打开已记录云下载路径前验证账号归属 |
| `GET` | `/api/events` | WebSocket 实时事件流 |
| `POST` | `/api/transfers` | 发起文件发送 |
| `POST` | `/api/transfers/{id}/accept` | 接受接收（可指定 saveDir） |
| `POST` | `/api/transfers/{id}/reject` | 拒绝接收 |
| `POST` | `/api/tasks/clear` | 清除当前可见终态任务；云下载保留隐藏归属记录 |
| `DELETE` | `/api/tasks/{id}` | 删除单条任务 |
| `POST` | `/api/drive/start` | 启动 WebDAV 共享 |
| `POST` | `/api/drive/stop` | 停止 WebDAV 共享 |
| `POST` | `/api/config/reload` | 热加载配置 |
| `GET` | `/api/knowledge/status` | 知识服务登录态（本地，无网络探测） |
| `POST` | `/api/knowledge/login` | 登录知识服务（代理远端 `/auth/login`，成功后 Core 落盘会话，`mode=password`） |
| `POST` | `/api/knowledge/sso` | 用控制面令牌登记知识会话（先探测远端 `/auth/me`，成功才落盘，`mode=sso`；失败不覆盖手动会话） |
| `POST` | `/api/knowledge/logout` | 清空知识登录会话 |
| `POST` | `/api/knowledge/health` | 探测知识服务健康度（文档数/索引块数/LLM 状态；`documents` 与 `records` 分开） |
| `POST` | `/api/knowledge/query` | 知识问答代理（解除 30s 写超时，120s 上下文兜底；`mode=search` 仅检索不生成，供全局快搜） |
| `POST` | `/api/knowledge/documents/process-drive` | 显式将网盘所选文件/版本加入知识库；仅 SSO，Core 只透传字符串身份，知识服务向 Java 核验来源并冻结正文 |
| `GET` | `/api/knowledge/documents` | 知识库文档清单（最新任务状态 + 最近成功版本；网盘来源实时裁决，其他来源按归属/部门过滤） |
| `GET` | `/api/knowledge/documents/{file_id}/versions/{version_id}/view` | 知识文档阅读页数据（manifest + `document.json` 结构化正文；按文档归属/部门校验，与 `/query` 同口径） |
| `GET` | `/api/knowledge/documents/{file_id}/versions/{version_id}/source` | 知识文档原件下载（流式透传；与阅读页同一可见性判据） |
| `POST` | `/api/knowledge/meetings/transcribe` | 会议音频上传代理（multipart 流式透传，远端异步转写，202 回受理记录；带 `meeting_id` 时复用实时会话那条记录，不新建） |
| `GET` | `/api/knowledge/meetings` | 会议听记列表（不含分段全文） |
| `GET` | `/api/knowledge/meetings/{id}` | 会议详情（分段/发言人/总结/关键词/待办/区块状态；受理后前端轮询） |
| `GET` | `/api/knowledge/meetings/{id}/audio` | 会议音频回放代理（流式透传） |
| `GET` | `/api/knowledge/meetings/{id}/export/{format}` | 备忘录导出（`markdown`\|`docx`，流式透传） |
| `PATCH` | `/api/knowledge/meetings/{id}/speakers` | 发言人改名（单次一个） |
| `POST` | `/api/knowledge/meetings/{id}/speakers/merge` | 合并发言人（把 `from_speaker` 的全部轮次并进 `into_speaker`，对标钉钉「合并发言人」） |
| `PATCH` | `/api/knowledge/meetings/{id}/segments/{index}` | 修改转写正文中某一句（逐字稿可编辑；改过的下标记入 `edited_segments`） |
| `POST` | `/api/knowledge/meetings/{id}/archive` | 会议纪要归档入知识库 |
| `POST` | `/api/knowledge/meetings/{id}/mindmap` | 重新生成思维导图区块（LLM 慢链路） |
| `POST` | `/api/knowledge/meetings/{id}/summary` | 按模板重生成总结区块（可选 `template_id`；刷新 AI 待办，保留手工待办） |
| `POST` | `/api/knowledge/meetings/{id}/keywords` | 补跑关键词区块（LLM 慢链路） |
| `POST` | `/api/knowledge/meetings/stream/warm` | 后台预热流式 ASR 模型（进听记页即调；2026-10-20 在活栈实测首次加载 14~88s，不预热首片会卡住） |
| `POST` | `/api/knowledge/meetings/stream/session` | 开实时录制会话（先落一条 `status=recording` 记录，其 id 即推流会话 id） |
| `POST` | `/api/knowledge/meetings/{id}/stream` | 推一片 PCM16/16kHz/单声道（600ms=19200B，`application/octet-stream` 原样转发），回当前已上屏字幕 |
| `POST` | `/api/knowledge/meetings/{id}/stream/stop` | 结束推流会话（冲尾包），回完整实时字幕行 |
| `GET` | `/api/knowledge/meetings/{id}/stream/live` | 录制会话状态快照（不推音频也能刷新上屏内容；`recording=false` 表示会话已不存在，前端改为只等离线重跑） |
| `POST` | `/api/knowledge/meetings/{id}/live-points` | 会中要点（LLM 归纳已上屏内容，对标钉钉实时摘要树） |
| `POST` | `/api/knowledge/meetings/{id}/live-check` | 会中快检（只喂最近 4 行，核对数字/期限/人名口径；结果 `ok\\|attention\\|unclear`，LLM 不可用降级为 `unavailable`） |
| `GET` / `POST` | `/api/knowledge/meetings/hotwords` | 读 / 批量新增当前用户个性化热词（seaco 模型级上下文增强；校验字符合法性与每人上限） |
| `DELETE` | `/api/knowledge/meetings/hotwords/{word}` | 删除一条热词 |
| `POST` | `/api/knowledge/meetings/{id}/retranscribe` | 重新转写整场（区块状态复位后重跑转写+总结+关键词+导图，热词在此生效；原始音频缺失则拒绝） |
| `GET` | `/api/knowledge/meetings/voiceprints` | 当前用户已登记声纹列表（**仅名字与样本时长，向量本体不出服务端**） |
| `DELETE` | `/api/knowledge/meetings/voiceprints/{name}` | 删除一条声纹（生物特征数据必须可删，删后不再自动认领） |
| `POST` | `/api/knowledge/meetings/{id}/todos` | 新增待办（一等实体，手工项 `source=manual`） |
| `PATCH` | `/api/knowledge/meetings/{id}/todos/{todoId}` | 编辑待办内容/负责人/期限或勾选完成 |
| `DELETE` | `/api/knowledge/meetings/{id}/todos/{todoId}` | 删除待办 |
| `DELETE` | `/api/knowledge/meetings/{id}` | 删除会议记录与音频 |
| `ANY` | `/mcp` | 听记只读 MCP（Streamable HTTP，独立配对令牌 `mcpToken` 鉴权，7 个工具） |
| `GET` | `/mcp/meetings/{id}/audio` | MCP 侧「音频地址」取原始 WAV（同一 `mcpToken`，不复用桌面 apiToken） |
| `GET` / `POST` | `/api/mcp/info` · `/api/mcp/regenerate` | MCP 接入信息与令牌重新生成（走 apiToken，仅设置页调用） |
| `POST` | `/api/shutdown` | 优雅退出全部 Core 服务 |

**任务账号隔离（2026-11-25）**：除本机 apiToken 外，桌面向 Core 任务路径及事件连接提供 `X-EasyShare-Account-Token`；Core 实时查询控制面的稳定账号与 Drive 权限，并将服务地址和账号合成不可由请求声明的 ownerId。账号服务故障/撤权时列表仅保留设备任务并返回 `X-EasyShare-Task-Scope: device`，云任务写入拒绝。历史云任务缺少 ownerId 不自动认领；删除云下载历史只隐藏，保留路径归属供应用打开/定位时核验。该边界不改变 Windows 文件系统 ACL，也不能阻止同一操作系统用户通过其他软件读取已经下载的文件。

云进度事件以任务 ID 合并最新值，待处理最多 64 项，每批重新验权，批间至少一秒；不缓存正向授权。设备事件使用独立通道，不被控制面等待阻塞；溢出后的完整状态由快照恢复。换号取消旧订阅，界面立即清掉云任务并阻止旧快照回填。证据：`internal/api/task_scope.go` · `taskOwner`、`internal/api/events.go` · `EventHub`、`internal/task/scope.go` · `VisibleTo`。

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

桌面端升级链路：启动 24h 节流自动检查（`update-state.json`）→ 设置页「关于与更新」手动检查 → 下载（SHA256 校验，`%LOCALAPPDATA%\EasyShare\updates\`）→ Windows 安装版「重启并更新」（`installer /S /update` → 优雅停 Core → NSIS taskkill 残留 → 覆盖安装 → 自动重启）；macOS 与绿色版仅引导下载。

发布链路（2026-11-23 起）：正式 `vX.Y.Z` tag → 对应平台 CI 构建 → 版本/提交/目标/摘要绑定的资产 → 独立发布任务 → `scripts/publish-release.mjs` 登录控制面、直传 RustFS、发布、匿名清单与实际下载摘要回验。Windows/macOS 发布串行但各自依赖自己的构建，不需要双平台都成功才给单个平台上线；预发布 tag/手动构建只生成 GitHub 分发产物，不进入普通更新源。同版本不同正文拒绝覆盖，旧版补发不得覆盖更高版本；这些护栏在发布器实现，管理 API 本身仍保留原兼容语义。PowerShell 入口 `scripts/publish-release.ps1` 共用该发布器，作为隔离部署/恢复通道。CI secrets/网络前置与发版验收见 [开发手册发布章节](development.md#在线更新发布首次配置)。

## 4b-2. 租户服务配置下发（2026-09-06 起）

「客户端只预知控制面一个地址，其余服务地址登录后下发」的租户服务发现（`ServiceConfigController.java`，客户端 `internal/account/serviceconfig.go` + App 绑定 `ServiceEndpoints`/`SaveServiceConfig`）。存储复用 RuoYi `sys_config`（键 `drive.service.knowledge.url`，**零 DDL**），读写在模块内闭环（不经 RuoYi 配置缓存，口径自洽）。当前下发项：知识服务地址。客户端未登录/未登记时回退**同主机推导**（控制面 `:8090` → 知识服务 `:8000`，「知识」页自动填好，员工免手填）；管理页「服务配置」卡片登记（superadmin）。

**地址对账与统一登录**（`App.reconcileKnowledgeSession` / `App.syncKnowledgeAccess`，2026-10-26 起，2026-10-27 补 SSO）：账号登录成功、启动恢复会话、管理员保存服务配置后各跑一次。登记值是权威源，与会话里的 `knowledge.json.serverUrl` 不一致就失效旧会话（令牌属于旧服务，静默改写地址复用旧令牌只会拿到 401）；未登记时**只纠正明显错误**——控制面非回环、本机会话却指向回环，用推导值兜底；控制面本身是回环（本地联调）则不干预。对账返回应使用的地址，随后 Core 用控制面令牌调知识服务 `GET /auth/me` 探测，成功才把会话切为 `mode=sso`（员工不再二次输入知识账号口令，功能权限矩阵因此在桌面端真实生效）；探测失败保留原手动会话，知识页回落到本地账号登录。前端侧：知识页给回环会话打「本机地址」标记、给 SSO 会话打「统一登录」标记，两页订阅 `knowledge:target-changed` 与 `knowledge:session-changed` 刷新登录态。

| 方法 | 路径 | 鉴权 | 用途 |
| --- | --- | --- | --- |
| `GET` | `/easyshare/service/config` | 登录 | 下发服务配置（未登记项空串，客户端回退推导） |
| `PUT` | `/easyshare/service/config` | superadmin | 登记知识服务地址（空串=清除，回退推导；自动 strip 尾斜杠/校验 http 前缀） |
| `GET` | `/easyshare/service/ai-config` | superadmin | 读 AI 能力配置（LLM 地址/模型/密钥掩码 + 听记开关；密钥明文不出控制面） |
| `PUT` | `/easyshare/service/ai-config` | superadmin | 保存 AI 配置（sys_config `drive.service.ai.config` 权威）并凭服务密钥（`drive.service.ai.secret`）**同步推送到知识服务 `PUT /admin/ai-config` 热生效**；推送失败不阻断保存，结果在响应 `pushed/pushError` |

**AI 配置热生效链路（2026-10-12）**：管理页「AI 能力」卡片 → 控制面 sys_config（权威）→ 服务密钥头 `X-Service-Secret` 推送 → 知识服务写 `data/runtime_config.json` 覆盖层（优先于 .env，重启仍在）→ 原地重建 LLM 消费组件（RAG Generator/MultiHop/ContextualBuilder/听记 Summarizer）与 ASR 开关。知识服务推送端点固定收 camelCase（pydantic alias）。**坑：Java 推送客户端必须钉死 HTTP/1.1**——JDK HttpClient 默认 HTTP/2 语义对明文地址带 h2c 升级头，uvicorn/h11 回 422（排障实录见 `iterations/2026-10-12-AI能力配置化.md`）。

## 4b-3. 成员功能权限与个人资料（2026-10-27 起）

企业化整改第二轮：成员能自助改昵称/头像/密码，超管按人配置五项功能开关。第三轮（2026-10-27）补上角色/权限组与子管理员。DDL 见 `deploy/ruoyi-db/easyshare-user-permission.sql`（`es_user_permission`）、`easyshare-user-profile.sql`（`es_user_profile`）与 `easyshare-permission-role.sql`（`es_permission_role` / `es_user_role`），均幂等并纳入 `scripts/ship-control-plane.ps1` 的 `$easyshareSql` 每次重放。迭代记录见 [`iterations/2026-10-27-桌面端企业化整改-第二轮.md`](iterations/2026-10-27-桌面端企业化整改-第二轮.md) 与 [`iterations/2026-10-27-企业权限角色与子管理员.md`](iterations/2026-10-27-企业权限角色与子管理员.md)。

**权限模型（角色优先，逐账号兜底）**：先验证账号及所属部门仍有效，再裁决功能。① 有角色绑定时取五项开关并集，逐账号行不参与；② 无角色时读逐账号行；③ 有效账号也没有逐账号行时保持既有五项全开的开箱策略。缺失/停用/删除主体、孤儿角色绑定和空布尔值不会获得缺省权限。drive 关闭时 shared 一并失效。删除角色或移除成员最后一个角色会先保存其当前有效限制，再解绑，避免复活旧的宽松配置。证据：`platform-drive/src/main/java/org/dromara/easyshare/drive/service/FeaturePermissionService.java` · `snapshot`、`platform-drive/src/main/java/org/dromara/easyshare/drive/service/PermissionSubjectService.java` · `activeUser`。

**子管理员范围**：支持 `none`、`dept`（本部门）、`dept_children`（本部门及下级）、`specified`（指定有效部门）、`all`，多个角色取并集。指定范围从数据库真实加载；空、非法或溢出的部门 ID 拒绝扩大范围。子管理员不能修改超级管理员；角色定义、角色分配和空间授权仅超管可写。保存逐账号开关时在事务中再次确认当前管理范围；目标挂角色时拒绝无效的逐账号覆盖。证据：`platform-drive/src/main/java/org/dromara/easyshare/drive/service/PermissionRoleService.java` · `canManage`。

**强制落地**：网盘由 `DriveFeatureInterceptor` 按路径覆盖 `/easyshare/drive/**`（未登录放行给 `@SaCheckLogin` 回 401，避免把「没登录」误导成「没权限」，新增端点自动受管）；共享空间在 `SpaceService` 的 `sharedPermissionOf`/`checkSharedWritable`/`checkSharedReadable` 收口，`SpaceController.mine()` 在 drive 关闭时返回空列表；知识服务见第 11 节「功能权限拦截」。

| 方法 | 路径 | 鉴权 | 用途 |
| --- | --- | --- | --- |
| `GET` | `/easyshare/permission/mine` | 登录 | 自己的五项开关 + `adminScope` + 角色名（缺行按全开；SSO 与桌面端据此隐藏入口） |
| `GET` | `/easyshare/permission/admin/members` | 超管或范围内子管理员 | 管理页成员矩阵：身份 + 生效权限 + 角色，按操作者范围裁剪 |
| `GET` | `/easyshare/permission/admin/list` | superadmin | 全部**显式配置行**（旧版桌面端兼容；新客户端改读 `/admin/members`） |
| `PUT` | `/easyshare/permission/admin` | 超管或范围内子管理员 | 保存某账号的逐账号五项开关（子管理员限有效管理范围；目标挂角色时拒绝） |
| `GET` | `/easyshare/permission/admin/roles` | superadmin | 全部权限角色及成员数 |
| `PUT` | `/easyshare/permission/admin/roles` | superadmin | 新建或更新角色（`roleId` 空=新建，角色名全局唯一） |
| `DELETE` | `/easyshare/permission/admin/roles/{roleId}` | superadmin | 删除角色（单角色成员落逐账号快照后解绑） |
| `PUT` | `/easyshare/permission/admin/members/{userId}/roles` | superadmin | 整组替换成员角色（显式空列表=保存当前限制后解绑；缺失列表拒绝） |
| `GET` | `/easyshare/profile/mine` | 登录 | 自己的头像地址（相对控制面；空串=未设置） |
| `POST` | `/easyshare/profile/avatar` | 登录 | 上传头像（multipart `file`，≤2MB，文件头嗅探 MIME，拒绝 SVG） |
| `DELETE` | `/easyshare/profile/avatar` | 登录 | 清除头像（客户端回落昵称首字） |
| `GET` | `/easyshare/profile/avatars/{userId}` | **匿名** | 读头像字节，7 天缓存 + ETag；`<img>` 发不了 Authorization 故放开，需随地址带 `?v=` 版本避免换图后看旧图 |

昵称与密码**不走**本模块：RuoYi 标准个人中心 `/system/user/profile`、`/system/user/profile/updatePwd` 已允许任何登录用户改自己。头像不用 `sys_user.avatar` + `sys_oss` 的原因：标准上传接口要求 `system:oss:upload` 权限（普通成员没有），且控制面只配了 EasyShare 自己的 RustFS。

**桌面端权限态**：登录、恢复和后台校验刷新通过 `MyPermission` 获取功能、管理范围及角色。读取失败时保留可展示的账号身份，但清空功能和管理入口并通知界面，不再按全开放行；管理桥也拒绝无效权限。请求绑定发起时的账号状态，迟到的失败/注销不能覆盖后续成功登录。管理员保存后重新读取实际状态；保存失败保留草稿，写成功但重读失败明确显示需要核验。角色管理/分配仅超管可见，子管理员看到自己的有效管理范围。证据：`app.go` · `syncAccountProfile`、`frontend/src/composables/useEasyShare.ts` · `useEasyShare`。

**已知边界**：插件商城的公开包目录保持匿名，plugin 开关控制企业客户端的使用入口，不等同于撤回已经安装的包。功能角色取并集；新有效账号无配置仍为全开；移除文件显式成员后回到继承授权，不是显式拒绝。既有已下载字节和已签发存储 URL 无法追溯撤回。需要最小权限开户的企业应先分配受限角色。

**授权写入与审计（2026-11-25）**：权限开关、角色、角色分配、空间成员与文件成员变更在同一事务内先取得数据库授权互斥行，再锁定组织/相关账号并复验操作者范围，最后记录修改前后状态。锁冲突立即返回刷新重试，审计失败回滚授权；未完成迁移时写入失败关闭。需要先执行 `deploy/ruoyi-db/easyshare-permission-hardening.sql`，再升级所有控制面实例；旧实例不遵守新锁协议，不可混跑授权写入口。证据：`platform-drive/src/main/java/org/dromara/easyshare/drive/service/PermissionWriteService.java` · `begin`、`platform-drive/src/main/java/org/dromara/easyshare/drive/service/PermissionWriteService.java` · `audit`。

## 4b-4. 文件成员与知识实时权限（2026-11-23 起）

共享文件成员角色由 `platform-drive/src/main/java/org/dromara/easyshare/drive/service/FileRoleService.java` · `effective` 统一裁决，manage/edit/download/read 四档覆盖默认空间角色；空间准入、功能开关和 active 文件状态仍是前置，显式文件成员不能绕过空间撤权。个人文件仅空间归属者可用，不靠同名知识账号认领。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `GET` / `PUT` | `/easyshare/file-member/{fileId}` | 文件管理成员读取/整组替换成员角色；自己的账号行保留且不接受自改，空数组合法 |
| `GET` | `/easyshare/file-member/{fileId}/candidates` | 先验文件 manage，再按关键词与上限返回同共享空间有效账号/部门的最小展示字段；写接口执行同一候选范围规则 |
| `POST` | `/easyshare/knowledge/acl/access` | 根据当前登录身份批量裁决文件阅读/原件下载；无权限或非 active 文件不返回可用授权 |
| `POST` | `/easyshare/knowledge/acl/bind` | 按文件身份＋预期版本或旧受管对象 key 核验真实源与内容，返回可信文件关联，不接受客户端声明归属 |

共享文件首先要求账号有效且仍有空间访问权，然后才裁决上传者/显式文件成员/可见部门/空间默认角色。空间撤权后，上传者和旧文件成员也失去入口。

共享空间 `GET /easyshare/drive/changes` 的增量请求返回业务码 `409`、`DRIVE_SYNC_RESET_REQUIRED` 与 `resetRequired=true`，不携带旧文件名或部分日志；调用方清空游标及缓存后，重新取得按实时权限过滤的全量。桌面挂载的共享列表直接重取全量；个人缓存绑定当前令牌。个人增量仍支持分页，但水位仅推进到本页最后事件，日志有缺口或游标非法时要求重建；全量交付列举开始前水位，避免跳过并发变化。代价是共享全量及实时授权请求增加，尚未做生产规模压测。证据：`platform-drive/src/main/java/org/dromara/easyshare/drive/DriveController.java` · `changes`、`platform-drive/src/main/java/org/dromara/easyshare/drive/service/DriveChangeLogService.java` · `changesAfter`。

网盘页的共享入口按服务端 `effectiveRole` 展示动作；普通上传者/manage 成员与超管管理页复用成员编辑器，所有文件/版本/主体 ID 保持字符串。个人原功能保留；共享 CFAPI、自动入库及安全水印不在本片范围。

显式「加入知识库」经 `POST /documents/process-drive`，Java 核验网盘文件/版本与原件内容，Python 在入队前冻结源正文并记录 `source_binding`，后续处理及原件访问使用同一快照。知识文档 ID 与网盘 ID 不是同一域，不能直接拿 watcher/lab 的任意 ID 作为网盘身份。网盘来源的问答、清单、阅读、产物和原件访问通过统一适配器逐请求回查控制面；read 可检索和阅读，原件要求 download。控制面不可用/缺新接口/来源未登记时受管文档拒绝放行，不回退入库 owner/部门快照。旧受管文档只有原件摘要与可信来源一致时才可关联，不一致须重新入库；本地知识身份不得冒用 SSO 同名账号。

升级须先投递新控制面再更新知识服务/桌面端；反向回滚到缺少 ACL 的控制面会使受管知识访问关闭。仅查看不承诺防截图/防复制，既有已下载文件或已签发存储 URL 不可远程撤回。实现与验收记录见 [`iterations/2026-11-23-权限闭环完善.md`](iterations/2026-11-23-权限闭环完善.md)。

## 4c. 插件系统与商城接口（2026-08-31 起）

**插件形态**：Web 插件包（zip：`manifest.json` + HTML/JS/CSS），UI 跑在桌面端前端 `<iframe sandbox="allow-scripts">`（opaque origin），经 postMessage RPC 调用宿主能力；公共 SDK 由宿主统一 serve（`/plugins/_sdk/eshare.js`）。**剪切板为首发普通插件**（2026-09-01 起，可卸载可禁用；此前为内置不可卸载，用户明确「插件即可安装可卸载」后改造）：源码在插件工程 `plugins/clipboard/`，主仓 `//go:embed all:plugins/clipboard` 直读，经 `SeedPlugin` 做**首次运行种子**——落成普通插件（登记 builtin=false），之后更新走商城流程（含权限确认），卸载后不复活（`plugins-seeded.json` 标记）；录制服务与快捷面板随插件在场状态启停（卸载即停记录、销毁面板、释放热键）。宿主侧监听在 `internal/clipboard`（Windows `AddClipboardFormatListener` + message-only 窗口；macOS NSPasteboard changeCount 800ms 轮询，2026-09-01 起；文本/图片(DIBV5/TIFF→PNG)/文件路径，hash 去重、来源进程/应用名、Windows 侧排除密码管理器标记格式）。

**运行时边界**：插件能力调用走唯一动态通道 `PluginInvoke(pluginId, api, argsJSON)`（`appplugin.go`），Go 侧能力注册表（`internal/plugin/registry.go`）按 manifest `permissions` 鉴权——新增能力不改 Wails 绑定。首发能力：`storage.*`（按插件隔离 KV）、`clipboard.history/delete/clear/write/settings`、`clipboard.events`（变更推送）、`notification.show`、`drive.upload`（文本上传个人空间，走统一任务通道）。能力另含 `clipboard.stats`（分类计数，2026-09-01）。静态资源由同一 `assetMux` serve（`/plugins/{id}/...` 映射 `%LOCALAPPDATA%\EasyShare\plugins\{id}\`；`/clipboard-files/` 给剪切板图片），两个入口：Wails AssetServer fallback Handler，以及面板与插件页共用的进程内 loopback 监听。**插件页 iframe 一律从 loopback 源加载**（`PluginHostBase` 下发基址，2026-10-26 起）：sandbox iframe 是 opaque origin，在 `wails.localhost` 下只拿得到入口 HTML，`style.css` / `app.js` / SDK 等子资源全部失败，页面会裸奔成一列纯文本。**快捷面板**（2026-09-01，`panel_surface.go` + 平台实现 `panel_windows.go`/`panel_darwin.go`）：全局热键（Win+V，被占则依次回退 Win+Shift+V / Ctrl+Shift+V / Alt+V；macOS ⌘⇧V）唤起独立小窗（Win32+WebView2 / NSPanel+WKWebView），加载同一插件页 `?panel=1` 紧凑形态；面板静态资源走桌面端进程临时 loopback 监听（`127.0.0.1:0`，复用同一 asset mux，仅回环）；SDK 经原生通道（WebView2 postMessage 字符串 / WKWebView script handler）走同一 `PluginInvoke` 鉴权路径；**面板内成功的 clipboard.write = 选中条目：收起面板并把焦点切回唤起前窗口合成 Ctrl+V / ⌘V 粘贴**（macOS 合成按键需辅助功能授权，未授权降级仅复制）；失焦、Esc（`eshare.window.dismiss()`）或再按热键收起。安装：SHA256 校验 → 解压临时目录（zip-slip 防护）→ 原子换入 → `plugins.json` 登记；包上限 50MB。

**商城（官方自营）**：插件/版本/资产放 PG（`es_plugin` / `es_plugin_release` / `es_plugin_release_asset`），zip 本体存 RustFS `plugins/{pluginId}/{version}/`，发布走与在线升级相同的两段式预签名直传。发布入口两条：单插件带版本说明用 `scripts/publish-plugin.ps1`，补齐全部官方插件用 `scripts/publish-official-plugins.ps1`（自动发现 `plugins/` 下含 manifest.json 的目录，排除 `template`）。后者挂在 `scripts/ship-control-plane.ps1` 探活之后（`-SkipMarketSeed` 可跳），保证**新装用户的商城不空**——清单只列「有已发布 zip 资产」的版本，纯 SQL 种子无效，字节必须在对象存储里。

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
4. 建立 Core API Client；先清理上次遗留的 Shell 入口，再恢复保存的账号会话。恢复会异步挂载，因此不能在它之后再做无条件入口清理。
5. 启动 watchdog、Core事件流与插件系统；恢复的用户快照通过 `auth:restored` 通知前端，前端仍做一次当前用户查询并防止迟到响应覆盖新状态。
6. Windows 通过认证、目录能力探测与本机保护扫描后发布个人CFAPI入口；非Windows保留原平台挂载通路。
7. 前端初始化快照、订阅事件并建立5秒fallback轮询。

### 系统文件入口（此电脑 / Finder）

Windows 云端空间与局域网入口分开；CFAPI同步根是服务器云盘的Windows接入与透明缓存，不是另一份本地云盘。

- **Windows 个人云盘**：认证、配额和 `GET /easyshare/drive/capabilities` 协议1/持久目录探测通过后建立CFAPI连接。根与根外写回状态按服务器/账号/空间稳定身份隔离。首次及每轮都先扫描保护本机修改，再投影远端树；新建与编辑用带版本条件上传，独立远端全文校验后确认。
- **持久目录**：通过 `POST /easyshare/drive/directories` 创建数据库目录实体，目录类型同时出现在清单与变更日志；旧 `/mkdir`、`includeDirs` 与明确递归删除继续兼容，部署须先执行配套迁移。
- **缓存管理**：默认5分钟空闲后，仅对精确已提交版本/哈希、无pending、无占用/修改/固定的完整缓存回收；持同一NTFS句柄再确认远端。显式保留/释放接口走同一保护链路，失败保留数据。
- **Windows 共享CFAPI**：未具备完整权限执行适配，暂不发布，不退回UNC冒充成功；主窗口的共享文件操作保留。
- **局域网与非Windows**：Core局域网WebDAV职责不变；非Windows个人/共享继续使用 `internal/spacedav` 与既有平台挂载。Windows云盘不再依赖19082/19083或WebClient。
- **正常退出**：断开CFAPI回调而不注销持久根；注销/重建根可能损坏未水合占位身份，不得用作日常进程清理。退出登录撤销入口和旧会话，旧世代请求不得发布结果。
- **安全范围**：只实现新增/编辑/目录创建；不从本机消失推断远端删除/改名，不从远端缺项删除已有本机内容。可写模式使用保守全量清单，不宣称已启用增量优化。完整取舍、状态恢复与测试见 [Windows云端主存说明](windows-cloud-primary.md)。

### 退出全部服务

前端先停止轮询和事件订阅，然后调用 `ShutdownAll`。Core 资源清理顺序：

1. 停止共享 WebDAV（:19080）
2. 取消 Core 后台 context（发现、接收等）
3. 关闭 Core HTTP Server 并退出进程
4. 桌面端设置 quitting=true，调用 runtime.Quit（桌面进程退出时空间 WebDAV :19082/:19083 随之关闭）
5. 前端进入"服务已安全退出"状态

Windows个人CFAPI的托盘退出还要先断开回调连接，保持根注册和占位身份；见 `app.go` · `beginQuit`。退出不等于清除CFAPI状态，回退程序前须确认活动传输/pending并保存当前状态。

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
- Windows云根与根外 `cfapi-state` 按稳定服务器/账号/空间身份分隔；`pending.json` 和不可变上传快照用于失败恢复，不能直接覆盖或删掉。普通目录恢复需显式受限ACL授权与原NTFS身份/远端目录身份匹配，JSON不是跨信任边界的签名。
- peers 是内存状态，Core 退出后丢失。
- 知识服务登录态持久化在 `knowledge.json`（与 config.json 同目录，仅 Core 读写）：桌面进程保存设置时整份回写旧 config.json，令牌若存其中会被冲掉，故独立成文件。`mode` 区分手动登录（password）与随客户端账号的统一登录（sso），登出客户端账号只清后者。令牌不出 Core，前端只见登录态视图（`internal/knowledge`）。
- 插件系统（桌面端进程，见 §4c）：登记表 `plugins.json`、插件包 `plugins/{id}/`、插件私有 KV `plugins-data/{id}.json`（均在 config.json 同目录）；剪切板历史 `clipboard/history.jsonl`（追加写 + 环形截断默认 1000 条）与图片 `clipboard/files/{sha256}.png`（默认 200MB LRU），设置在 `clipboard/settings.json`。

## 8. 云盘与对象存储

**现行链路（ADR-0007，P2 起）**：桌面端 `internal/drive` 持控制面 JWT 调 `platform-drive/` 换短期预签名 URL（PUT 15m / GET 10m），直传/直取 RustFS；对象键强制落在 `users/{userId}/`（个人）或 `shared/`（共享）命名空间，服务端拒绝跨用户 key。配额与池上限（物理容量探测、预留水位、两种"满"分开报错）见 ADR-0007「空间授权与配额」。

- `/api/cloud/*` 七条路由与 `cloud.Service`、`webdavfs`、desktop.Client 的 Cloud* 方法已删除（KI-5 已关闭）：P4 空间挂载走 `internal/spacedav`（建在 `internal/drive` 之上），不存在"不经控制面的云盘 WebDAV"路径，隔离边界不再有死代码可绕。
- `internal/cloud` 仅保留前端契约类型与预览辅助；`internal/cloud/objectstore` 的 provider-neutral 存储抽象（s3store + memory fake）仍在服役（控制面之外的用途）。
- 本地 RustFS 开发环境见 `deploy/rustfs/`，生产启用受 ADR-0006 门禁约束；凭据真值只在 `deploy/rustfs/.env`（gitignore），由 `run-ruoyi-admin.ps1` 注入控制面进程。
- **下载不经浏览器（2026-10-20）**：`App.CloudDownload` 在桌面进程内取预签名 GET 流、写入接收目录（`ReceiveDir`，每次从 `config.json` 现读），同名自动加 ` (n)`；字节先落 `.easyshare-download-*.part`，完成后再改名，失败不留半截文件。进度写入 Core 统一任务（`kind=cloud_download`，创建时即带 `localPath`），活动面板与传输列表按任务展示；需要换落点时走 `App.CloudDownloadAs` 的保存框。预签名 URL 不再交给 `BrowserOpenURL`，否则进度、失败原因和完成入口都会脱离应用。

## 8a. 知识网关（桌面端 ↔ 知识服务）

- `internal/knowledge` 是知识服务的 HTTP 客户端与会话存储；Core 经 `/api/knowledge/*` 代理登录/健康探测/问答/会议听记，是桌面端与后续 WPS/Shell 扩展访问知识库的唯一通道（服务器地址在登录页配置，令牌不进前端）。
- **统一登录免登（2026-10-27）**：客户端账号登录后，桌面壳把控制面令牌交给 Core（`POST /api/knowledge/sso`）；Core 先调知识服务 `GET /auth/me` 探测，成功才落 `mode=sso` 会话，自动探测失败保留独立部署的已有密码会话；企业身份不能降级为本地令牌。登出客户端账号或启动校验遇 401 时只清 SSO 会话，手动知识账号不受影响。注册与清理由 `App.knowledgeSyncMu` 串行化，注册成功后二次校验账号令牌，避免已登出的账号把知识会话留活。会话令牌沿用控制面有效期，知识服务按它解析的 features 强制功能权限。
- 问答代理 handler 解除 Core 全局 30s 写超时（`http.NewResponseController`），120s 上下文兜底；desktop.Client 对问答走无客户端级超时的专用 HTTP client。
- **会议听记大文件通道（2026-10-12）**：音频上传/回放不经 Wails 绑定（绑定是 JSON 通道，塞不下上百 MB），前端同源 `fetch /kapi/meetings/*` → 桌面进程 `pluginAssetMiddleware` 接管（`appmeetings.go`；dev 下 Vite 会吞 fallback Handler，必须 Middleware 阶段拦截）→ desktop.Client 流式转发 → Core 上述 meetings 路由 → 知识服务 `/meetings`。列表/详情/管理/归档走常规 Wails 绑定。
- **听记实时上屏（2026-10-13，刻意不用 WebSocket）**：录音时按 600ms 定长分片（16kHz 单声道 s16le = 19200 字节）经 Wails 绑定（base64）→ Core → 知识服务 `POST /meetings/{id}/stream`，**请求即回当前字幕快照**——推流方向是采集端主动供数，请求-响应天然带背压，避开 WebView 跨源与 apiToken 不能进前端两件事。稳态单片往返实测 176ms（RTF 0.303）。流式会话只活在知识服务内存（闲置 30min 回收），**实时文本是预览**：停止后整段离线重跑（同一条记录，`transcribe` 带 `meeting_id` 复用）给出终态分段/标点/说话人。听记页挂载即调 `stream/warm`，否则首片承担约 70s 的模型冷加载。
- **听记实时上屏的韧性（2026-10-20，用户报「切页就中止」「不出流式字」）**：三条约束落到代码上——① 推流**必须**走 Wails 绑定（`appmeetings.go` · `MeetingStreamChunk`），因为它不挂在组件生命周期上；`fetch` 在组件卸载时会被打断。② 单片 12s 超时（`frontend/src/services/meetingSession.ts` · `withTimeout`），失败先 `GET /meetings/{id}/stream/live` 续快照再推下一片，**连续 3 次才降级为 3 秒轮询**，录音全程不受影响。③ 服务端推理锁在**会话自己**身上（`knowledge/app/meetings/stream.py` · `feed`），会话表锁只保护容器，多场并发不再互相堵死。断行按「句末/逗号/24 字」（`knowledge/app/meetings/livenow.py` · `should_break`），上屏不再是憋一整句才出。流式模型同样吃热词（`hotword` 参数，与离线同口径）。
## 9. 跨平台抽象

| 能力 | Windows | macOS |
| --- | --- | --- |
| 托盘 | 原生 Shell_NotifyIcon（ICO，NOTIFYICON_VERSION_4 + 悬停浮窗） | NSStatusItem（tray_native_darwin.m） |
| 文件入口 | Shell NameSpace 注册表 | mount_webdav / osascript |
| 打开文件 | `ShellExecute("open")` | `open` |
| 在文件夹中显示 | `explorer /select,` | `open -R` |
| 磁盘枚举 | kernel32 GetLogicalDrives | syscall.Statfs /Volumes |
| Core 进程 | 同目录子进程 | Setsid 独立会话 |
| 构建 | `wails build` + NSIS | GitHub Actions macos-latest |

平台拆分通过 build tags 实现：`*_windows.go` / `*_darwin.go`。Wails macOS 版不可从 Windows 交叉编译（WebKit/CGO）。

## 9a. 状态变化与动效

桌面 Vue 使用 `frontend/src/style.css` 的统一 token：反馈 120ms、进入 200ms、退出 120ms、进度 180ms、减少动效下淡入 80ms。常规曲线为 `cubic-bezier(0.2, 0, 0, 1)`，退出为 `cubic-bezier(0.4, 0, 1, 1)`。`es-fade/content/icon/popover/dialog/drawer/disclosure/list` 表达变化类型；内容更换通过 `frontend/src/directives/contentMotion.ts` · `animateContentChange` 从当前透明度衔接，业务状态不等待动画。离场立即 inert，确认框与活动抽屉可反向复用同一节点，保留焦点循环及恢复。

只对必要的 opacity/transform 插值：进度使用固定宽度 scaleX，数值使用等宽数字；拖动、窗口调整和脑图直接操作立即跟手。长任务列表复用未变化的行对象，并对第 25 行之后做 feature-detected `content-visibility` 延迟渲染，保留固有高度与键盘访问。系统减少动态效果时停止空间位移、旋转与进度补间，保留静态加载文字和短反馈；设置在运行中变化也生效。Windows 悬浮面板和桌宠重新遵循系统客户端动画开关。

插件、知识实验台、驾驶舱和 WPS 静态页面沿用相同节奏，轮询保留可复用节点，旧请求不能覆盖新视图，隐藏/取消不等退出动画。Motion 守卫与浏览器实测见 `scripts/diag/check-motion-browser.mjs`、`scripts/diag/check-static-ui-motion-browser.mjs` 和本次[迭代记录](iterations/2026-11-25-企业权限与状态动效.md)。

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
- `/lab` 与 `/debug` 是未配置控制面的独立部署诊断工具，仅回环且需启用 local lab；企业模式关闭这些可直接访问索引/管线的调试路由，统一通过受保护业务 API 与 WPS/桌面入口访问。
- MCP Server（可选，`pip install mcp` 后 `python -m app.mcp_server`）：stdio 薄桥转发 `/query` 与 `/health`，任何 AI 工具（Claude Code/Cursor/OA 助手）可检索企业知识
- 账号与登录：未配置控制面的独立部署保留 SQLite/PBKDF2 本地账号，由 `AUTH_ENABLED` 控制业务认证。配置 `CONTROL_PLANE_URL` 后进入企业模式，每个受保护请求都实时回查控制面身份及功能权限，企业模式不接受本地令牌作为备用身份。请求的管理员角色来自当前控制面结果，不继承同名本地档案的旧管理员角色；没有 60 秒正向授权缓存。证据：`knowledge/app/auth/sso.py` · `ControlPlaneSSO`、`knowledge/app/main.py` · `auth_middleware`。
- **WPS 登录与注销**：企业 `POST /auth/login` 转发到控制面 password grant，复验返回令牌后交付该企业令牌；不保存密码或签发本地备用身份。`POST /auth/logout` 注销提交的企业令牌，失败明确返回 503；独立模式只撤销提交的本地令牌。WPS 退出立即清除本地显示并调用注销，失败说明服务端注销未完成。Core 知识退出仍只解绑本地会话，不顺带注销共享桌面令牌。当前密码代理未适配控制面额外验证码/加密挑战，遇到要求时返回错误，不绕过；部署前必须验证实际登录策略。
- **功能权限拦截**：企业模式始终保护知识文档、问答、入库、任务、统计和会议路径；只接受明确的布尔 `true`。权限端点失败、字段缺失或格式异常均拒绝访问。会议归档同时要求知识权限；任务读取/重试按知识权限保护；统计需管理员。企业 `/lab`、`/debug` 直接关闭，回环地址不能绕过业务权限。快检与无 LLM 检索也在返回结果前重新确认文件 ACL。证据：`knowledge/app/main.py` · `_FEATURE_PREFIXES`、`knowledge/app/api/routes.py` · `query`。
- 权限感知检索（2b/2c，2026-09-01）：文档归属 `owner`（用户名）贯通 `processing_jobs` 表、任务响应、manifest 与索引 chunk metadata；入库归属以**令牌用户为准**（lab 上传谁传归谁；`/documents/process` 带令牌时忽略请求显式 owner，防伪造；watcher 监听目录与无令牌调用落 None=共享）；`/query` 服务端按令牌计算可见集——owner 为 None/缺失的文档共享给所有人（存量数据天然共享），非空仅本人与 admin 可见，与请求显式 `doc_ids` 求交集（空交集短路返回无结果，不进检索层）；未登录不过滤（行为不变）；可见集数据源为向量库 `doc_owners()`（JSON/Milvus 双实现）；Core 知识网关已透传 Bearer 令牌，桌面端/WPS 问答链路零改动生效；企业 `/debug/query` 不开放，回环地址本身不构成授权
- 生产检索编排（2026-09-01）：`/query` 由 `QueryOrchestrator`（`app/rag/orchestrator.py`）按 `QUERY_STRATEGY` 调度——`vector`（旧单路，embedding 故障自动降级 BM25）/ `hybrid`（默认；向量+BM25 RRF 融合）/ `hybrid_rerank`（融合池 ×3 后 Cross-Encoder 精排，未配置 rerank 等价 hybrid）/ `multi_hop`（分轮混合+LLM 充分性裁判，需 LLM，未配置自动降级 hybrid_rerank，响应 `degraded` 字段说明）；部署级配置不暴露给终端用户，非法值启动即报错；响应新增 `strategy`（实际执行策略）字段；生产查询/生成事件落 `QueryLog`（逐句忠实度仅驾驶舱审计计算，生产传 None）；向量库双后端实现 `count()`/`snapshot_records()` 协议供 BM25 懒构建与健康度聚合（修复 Milvus 后端无 `records` 属性导致的崩溃）
- 目录监听自动入库（`WATCH_DIRS` 分号分隔，默认关闭）：轮询扫描（SMB 共享盘可靠）+ mtime 稳定性窗口 + 内容哈希版本去重 + 失败自动重试；与 lab 上传同链路（storage → job → pipeline）
- WPS 加载项（`/wps/ribbon.xml`、`/wps/index.html`、`/wps/taskpane.html`）：知识服务自托管加载项页面，与 `/auth`、`/query` 同源免跨域；WPS 文字功能区「知识」页签一键查选中段落，任务窗格内登录（令牌存窗格 localStorage）。本机安装/卸载：`knowledge/scripts/install_wps_addon.ps1`（登记 `%APPDATA%\kingsoft\wps\jsaddons\jsplugins.xml`）
- **会议纪要引擎外置（2026-10-18）**：`knowledge/app/meetings/{engine,summarizer,templates,mindmap_harness,docx_export}.py` 已变为**薄壳转发**，实现集中在仓库顶层 `engine/`（minutecraft 包，`pip install -e engine` 可独立安装）。分工：知识服务保留**采集与产品态**（FunASR 转写 + cam++ 说话人、声纹、热词、区块状态与任务队列、存储与权限），引擎只负责**文本 → 纪要**（LLM 结构化产出 + 导图 harness + 钉钉版式 Word）。同一份代码同时供外部 Agent 使用（`minutecraft run` CLI / Python 库 / `minutecraft schema` 自描述）。薄壳里用 `sys.path` 注入 `engine/`，因此知识服务无需把包装进 venv；若想隔离依赖，也可 `pip install -e engine` 后删掉注入。
