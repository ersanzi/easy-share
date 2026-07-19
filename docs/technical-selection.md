# EasyShare 技术选型基线

> 状态：规划基线。本文描述面向 [`product-vision.md`](product-vision.md) 的推荐技术路线，不代表相关能力已经实现；当前实现仍以 [`architecture.md`](architecture.md) 为准。具体决策及其取舍记录在 [`adr/`](adr/README.md)，产品活跃度和候选评估规则见 [`technology-evaluation.md`](technology-evaluation.md)。

## 1. 目标与边界

本文用于回答 EasyShare 从 Windows 局域网工具演进为网络云盘时，客户端、云端、同步和 Windows 原生集成分别采用什么技术，以及哪些决策必须在进入对应阶段前冻结。

本轮只确定架构边界和首选方案，不包含：

- 云端服务和数据库的实际部署；
- CfAPI、Shell 扩展或局域网加密协议的实现；
- 免费额度、版本保留期、分享范围等产品参数；
- 供应商、地域、价格和采购合同。

## 2. 选型原则

1. **文件身份先于传输协议**：文件使用稳定 ID、版本和内容哈希标识，不以路径或某个传输通道作为身份。
2. **可靠状态先于实时通知**：数据库和增量游标是事实来源，WebSocket、文件监听和局域网发现只提供提示。
3. **Core 单独拥有业务状态**：UI、CfAPI Helper 和 Shell 代码不直接访问云端凭据或写本地数据库。
4. **先模块化单体，后按压力拆分**：云端 MVP 不因预期规模提前引入微服务、Kafka 或服务网格。
5. **平台代码最小化**：加载进 Explorer 或处理 CfAPI 回调的原生组件只负责 Windows 集成，不承载同步业务。
6. **所有长任务可恢复**：上传、下载、水合、内网传输和同步任务必须持久化、幂等并能在重启后继续。
7. **成熟但先进并持续复审**：不沿用已归档、功能收缩或商业边界突变的旧默认，也不让 beta 热门项目直接进入核心数据路径；按当前证据、PoC 和退出方案选型。
7. **安全不信任局域网**：发现到设备不等于信任设备，文件内容在采用前必须验证身份、权限和哈希。

## 3. 推荐总体架构

```text
Windows 客户端
├─ easyshare.exe：Wails + Vue 3 管理界面
├─ easyshare-core.exe：Go 同步、任务、缓存、云端和局域网逻辑
├─ SQLite（WAL）：本地元数据、游标和任务
└─ Windows Helper（C++）：CfAPI、Sync Root、最小 Shell 集成

云端
├─ Go 模块化单体：认证、设备、元数据、上传、配额、分享和变更日志
├─ PostgreSQL：事务性元数据和增量序列
├─ S3 兼容对象存储：文件内容
└─ 后台 Worker：校验、清理、配额和其他异步任务
```

客户端仍保留现有 Core/UI 两进程边界。进入 CfAPI 阶段后新增 Windows Helper，但不将云端或同步逻辑复制到 Helper。

## 4. 选型矩阵

| 领域 | 首选方案 | 暂不选择 | 决策阶段 |
| --- | --- | --- | --- |
| 桌面 UI | 保留 Wails + Vue 3 | 重写为 Electron/.NET UI | 已有基线 |
| 客户端核心 | 保留 Go Core | 将同步逻辑放入 Vue 或 Shell | 已有基线 |
| 本地状态 | SQLite + WAL + 版本化迁移 | JSON 任务队列、多个进程共享写库 | 云端 MVP 前 |
| 云端应用 | Go 模块化单体 | 微服务、服务网格 | 云端 MVP 前 |
| 元数据 | PostgreSQL | 对象存储目录即元数据、仅 NoSQL | 云端 MVP 前 |
| 文件内容 | 自建 RustFS + S3 API 契约；生产启用受 ADR-0006 门禁约束 | 业务绑定 RustFS 特有 API、未验证 beta 承载唯一数据副本 | 已接受，生产待验证 |
| 上传 | 控制面会话 + S3 Multipart 预签名 URL | API 全量代理大文件 | 云端 MVP 前 |
| 完整性 | SHA-256；必要时增加 BLAKE3 本地快速扫描 | 使用 multipart ETag 作为内容哈希 | 云端 MVP 前 |
| 增量同步 | Change Journal + 持久化 Cursor | 仅全量扫描、仅 WebSocket | 云端 MVP 前 |
| 实时通知 | WebSocket/SSE 只发送拉取提示 | 将推送消息作为唯一事实来源 | 同步阶段 |
| 本地监听 | ReadDirectoryChangesW/fsnotify + 周期核对 | 只依赖 watcher | 同步阶段 |
| 大目录恢复 | 评估 USN Journal | MVP 立即依赖 USN | 同步成熟阶段 |
| Windows 云盘入口 | CfAPI + 独立 C++ Helper | 在 Go/Wails 中直接承载全部 COM 回调 | CfAPI 阶段 |
| 本机 IPC | UI→Core 保留 loopback HTTP；Helper→Core 优先 Named Pipe | 多进程共同写 SQLite | CfAPI 阶段 |
| 局域网发现 | 现有 UDP 协议升级或 mDNS，发现与认证分离 | 把广播 Device ID 当作可信身份 | 融合阶段 |
| 局域网传输 | 优先评估 QUIC + TLS 1.3；可退回认证后的 TLS/TCP | 未加密裸 TCP | 融合阶段 |
| 默认加密 | TLS + 对象存储服务端加密 | 未定密钥恢复方案就承诺全盘 E2EE | 产品决策后 |
| 去重 | 不做或仅用户内整文件去重 | 跨用户/分块去重 | 规模验证后 |
| 安装 | 当前 NSIS 延续到 CfAPI 可行性验证；企业部署再评估 WiX/MSI | 未验证生命周期就直接迁移 MSIX | CfAPI 阶段 |

## 5. 必须先冻结的产品输入

已经确认：中国大陆首发、对象存储自行建设，并以 RustFS 为首选实现；详见 [`ADR-0006`](adr/0006-rustfs-self-hosted-object-storage.md)。以下问题仍会改变技术设计，不能由代码实现自行假设：

1. 面向个人、家庭还是团队，是否存在组织、角色和共享空间；
2. 是否必须端到端加密，服务端是否需要预览、搜索、扫描和跨设备分享；
3. 除官方自建服务外，是否同时支持用户私有化部署；
4. 单文件上限、总空间、版本保留期和回收站期限；
5. 分享链接、协作者和审计的第一版范围；
6. 最低 Windows 版本、是否仅支持 NTFS、是否首发 ARM64；
7. 第一版是否必须交付 CfAPI，还是先以应用内云盘和普通同步目录验证需求。

未冻结时可按本文件的推荐默认值做原型，但不得把默认值写成无法迁移的数据约束。

## 6. 分阶段落地

### 阶段 1：云端 MVP

必须完成：

- 文件、版本、内容对象和变更日志模型；
- 设备会话与凭据保存方案；
- PostgreSQL 与对象存储提交状态机；
- 分片上传和断点续传；
- SQLite 本地任务持久化；
- 应用内目录浏览、上传和下载。

暂不要求 CfAPI、USN Journal、跨用户去重和 P2P 穿透。

### 阶段 2：同步文件夹

在阶段 1 的 file ID、version ID 和 cursor 协议上增加：

- 本地目录绑定和变更监听；
- 双向同步与冲突副本；
- 周期核对、任务恢复和缓存回收；
- 双机、断网、重启、大文件和重命名验收。

### 阶段 3：CfAPI

正式开发前先完成独立技术验证，覆盖 Sync Root 注册、占位文件、水合/脱水、取消、Core/Explorer 重启、Office 占用、升级和卸载清理。验证通过后再冻结 Helper 语言、IPC 消息和安装生命周期。

### 阶段 4：云端与内网融合

设备建立可撤销的密码学身份。附近设备只能作为相同 `content_hash` 的数据来源，最终内容必须经过完整哈希校验；内网失败按用户策略回退云端。

## 7. 当前明确不引入

在指标证明必要前，不引入：

- 微服务拆分、Kafka 和服务网格；
- 跨用户全局去重或内容定义分块；
- WebRTC/STUN/TURN；
- 多个客户端进程共同写本地数据库；
- 将 WebSocket、文件 watcher 或 UDP 广播作为可靠状态源；
- 在 `explorer.exe` 内进行网络请求或加载完整 Core。

## 8. ADR 与评审门禁

| ADR | 决策 |
| --- | --- |
| [`0001-file-identity-and-version-model.md`](adr/0001-file-identity-and-version-model.md) | 文件身份、目录树、版本和删除语义 |
| [`0002-metadata-object-storage-consistency.md`](adr/0002-metadata-object-storage-consistency.md) | PostgreSQL 与对象存储的一致性和上传状态机 |
| [`0003-incremental-sync-and-conflicts.md`](adr/0003-incremental-sync-and-conflicts.md) | 增量游标、幂等操作和冲突规则 |
| [`0004-local-state-and-task-persistence.md`](adr/0004-local-state-and-task-persistence.md) | SQLite 单写、任务模型和恢复 |
| [`0005-cfapi-helper-boundary.md`](adr/0005-cfapi-helper-boundary.md) | CfAPI Helper、Core IPC 和 Explorer 稳定性边界 |
| [`0006-rustfs-self-hosted-object-storage.md`](adr/0006-rustfs-self-hosted-object-storage.md) | 自建优先、RustFS 采用范围和生产门禁 |

每个实现迭代开始前，应在对应 ADR 中确认状态和开放问题。若实现选择与 ADR 不同，应先新增替代 ADR 或将原 ADR 标记为已取代，而不是静默偏离。

具体外部产品在 ADR 接受和进入生产前，还必须按 [`technology-evaluation.md`](technology-evaluation.md) 刷新维护状态、稳定版本、许可证、故障测试和退出方案；历史流行度不能代替当前证据。


