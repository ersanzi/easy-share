# ADR-0007：账号控制面采用 RuoYi-Vue-Plus，与存储授权边界

- 状态：接受（P0–P2 已验证，不变量 1/2/3 有验收证据；P3/P4 待落地）
- 日期：2026-08-29
- 决策者：EasyShare maintainers
- 关联：[`../knowledge-platform.md`](../knowledge-platform.md) §3.2、[`../technical-selection.md`](../technical-selection.md) §4、[`0006-rustfs-self-hosted-object-storage.md`](0006-rustfs-self-hosted-object-storage.md)、[`../known-issues.md`](../known-issues.md) KI-2/KI-3

## 背景

EasyShare 云盘要从"单机单用户"演进为多用户：登录后进入个人空间、A 传的文件 B 看不到、管理员权限更高并能开关用户注册、后续区分个人网盘与共享盘。现状撑不起：无任何账号体系（Core API 仅一个静态 `apiToken`）；RustFS 凭据编译期写死且客户端二进制明文可提取（KI-2）；object key 扁平无用户隔离（KI-3）；Core 仅监听 loopback，按设计撑不起多用户。

两份文档对"账号后端语言"给了**不同选型**，需在此调和：

- `knowledge-platform.md` §3.2（主线二：企业知识平台）：**Java 控制面**（Spring Boot + MyBatis-Plus + PostgreSQL）承担账号、认证、RBAC、多租户、文件元数据。
- `technical-selection.md` §4（主线一：桌面产品→网络云盘）：**Go 模块化单体**承担认证、设备、元数据、上传、配额、分享。

两条主线都把"账号"写进各自路线，但"统一账号最终谁管"未定。用户方（含其技术顾问）选择 Java 方向并指定脚手架 **RuoYi-Vue-Plus 6.0**。

## 决策

1. **统一账号控制面采用 RuoYi-Vue-Plus 6.0**（Spring Boot 4.1 / JDK 21 / MyBatis-Plus / Sa-Token / PostgreSQL / Redis / Vue3 管理后台）。它开箱提供账号、RBAC、登录注册、注册开关（`sys.account.registerUser`）、多租户、管理员 Web 后台，逐条命中需求，技术栈与 `knowledge-platform.md` 一致。
2. **`technical-selection.md` 的"Go 云端单体承担认证/账号"被本 ADR 取代**：账号/认证/权限归 RuoYi 控制面。Go `easyshare-core` **退回为本机采集与传输层**——继续做局域网发现/传输、WebDAV 入口、任务，不再拥有账号与云端凭据。技术选型文档中"云端应用=Go 模块化单体"仅在其未被 RuoYi 覆盖的部分（若有）保留。
3. **RustFS 按用户隔离的存储授权是自建工作量，任何脚手架都不提供**：由控制面（RuoYi 内新增存储授权模块，复用其 S3/OSS 能力）在验证 JWT 后，按用户命名空间 `users/{userId}/...` 给 RustFS 签发短期预签名 URL。客户端凭预签名 URL 直传，不再持有 RustFS 静态密钥。
4. **客户端（Go Core + Vue）是控制面的客户端**：登录经 RuoYi 拿 JWT，上传/下载经控制面换预签名 URL。
   ~~管理员的管理界面 = RuoYi 自带 Web 后台，客户端仅加入口按钮。~~
   **已修订（P3 落地时）**：管理界面是**客户端内嵌的自绘页**（`AdminPanel.vue`，沿用客户端色调），
   直接调控制面 REST。原写法把管理体验外包给 plus-ui，与产品意图不符——管理员开账号、分空间
   属于日常操作，不该跳出客户端。RuoYi 自带后台退为**次要出口**，只覆盖本产品不复刻的运维动作
   （菜单/字典/定时任务），入口保留在 `config.adminConsoleUrl`。

## 不变量

1. RustFS 静态 AK/SK 只存在于控制面，绝不进入客户端二进制（修 KI-2）。
2. 每个用户的对象 key 带稳定用户命名空间前缀，服务端强制校验归属、拒绝跨用户 key（修 KI-3）。
3. `easyshare-core` 不拥有账号真相、不持云端长期凭据、不做权限裁决。
4. 账号、角色、注册开关、权限的真相源是 RuoYi 的 PostgreSQL；客户端只缓存展示态。
5. 接口加密与验证码在生产保持开启（`api-decrypt.enabled` / `captcha.enable`），仅本地 dev 关闭。

## 备选方案

- **自建 Go 模块化单体**（technical-selection.md 原选型）：复用 Go 栈、基础设施轻，但账号/RBAC/后台/注册控制全部自写，正是 RuoYi 免费提供的部分。用户选择不自造轮子。
- **RuoYi 5.x（Spring Boot 3）**：更稳更成熟。本轮按顾问指定用 6.0（最新主线，Spring Boot 4.1）。P0 已验证 6.0 可跑通 PostgreSQL，暂无回退必要；若 6.0 + Spring Boot 4.1 在后续阶段暴露框架兼容问题，可新增 ADR 回退 5.x。
- **账号放 Go Core**：拒绝。Core 仅 loopback、按设计撑不起多用户。

## 影响

正面：账号/RBAC/后台/注册控制开箱即用，大幅省去自建；与知识平台（主线二）将来共用同一控制面成为可能。
成本与风险：引入 Java + PostgreSQL + Redis 运行时与运维；RuoYi 6.0 用 Spring Boot 4.1（很新）存在前沿框架风险；两条主线"统一账号"的收敛细节仍需设计；存储授权模块与 KI-2/KI-3 修复是必须自建的实打实工作量。

## 开放问题

1. 多租户是否启用（当前 6.0 基础 schema 未建 `sys_tenant`，登录不带 tenantId）。
2. JWT 在桌面客户端的安全存储（Windows Credential Manager / macOS Keychain，参 client-workbench §18）。
3. 存储授权模块形态：RuoYi 内 Java 模块 vs 独立服务（倾向前者，复用其 OSS 与 AWS SDK v2）。
4. 部署拓扑（本地 dev / 服务器）与 RustFS 生产门禁（受 ADR-0006 约束）。
5. RuoYi Vue 管理后台（plus-ui 独立仓）的接入时机（P3 管理员控制台）。
6. ~~**空间授权与配额模型未设计**（P2 复盘暴露，决策 4 的缺口）。~~
   **已落地（P3）**，模型见下「空间授权与配额」一节。原文记录的产品意图——每个账号一个空间、
   用户的空间由管理员设置、共享空间的实际权限由管理员把控——已由 `es_space` /
   `es_space_member` 与 `SpaceController` 实现。遗留的**未决问题**是配额的强制力度，见该节
   「已知弱点」。

   （原判断成立并已生效：挂载重做的前置是本模型，P4 的滑动开关只是展示层。）

## 空间授权与配额

落地于 P3，补上决策 4 的缺口（原开放问题 6）。

**两类空间，一张表。** `es_space` 一行一个空间，`space_type` 区分 `personal` / `shared`；
个人空间由 `owner_id` 独占，共享空间的成员授权在 `es_space_member`（`read` / `write`）。
个人空间不需要成员行——归属由对象键前缀 `users/{userId}/` 强制（不变量 2），共享空间用
`shared/` 前缀，两者互不包含。DDL 在 `deploy/ruoyi-db/easyshare-space.sql`。

**配额只存上限，用量不落库。** `quota_bytes`：`0` 未分配（客户端显示「待开空间」）、
`-1` 不限。已用量**不存**：预签名 URL 签发后客户端直传 RustFS，字节不经控制面，库里的
用量字段必然与真实脱节。用量一律实时 `ListObjectsV2` 聚合，按前缀缓存 60 秒。

**强制点只有一个：`presign-put`。** 客户端拿不到 RustFS 静态密钥（不变量 1），绕不开签发
环节，因此这里是唯一能拦住写入的地方。

**管理入口是一处。** 客户端管理页的「空间」页同时设定共享容量与逐账号个人配额；
「开通空间」与「改配额」是同一个动作（无空间行则建、有则改）。

### 池上限与物理容量（2026-08-30 补）

逐空间配额挡不住「承诺总量超过物理磁盘」。补此层前的实测状态：
容器内 `df` 报 1007 GB 可用，而宿主实际只剩 34 GB；已承诺 60 GB，**超配 1.75 倍**。
后果是用户看到「配额还剩 80 GB」但传不上去——配额数字变成谎言。

**两层并存，不改成纯池化。** 逐空间配额挡「一个人吃光」，池上限挡「超过物理盘」。

- 物理容量**不落库**，实时探测（`CapacityService`，缓存 60 秒）。
  探测路径由 `easyshare.drive.capacity-path` 配置，必须是**宿主侧**路径：
  RustFS 在容器内，容器 `df` 看到的是 WSL2 稀疏 vhdx 的虚数。
  留空则不启用，行为与补此层之前完全一致。
- `reserved-bytes`（默认 5 GB）是预留水位：磁盘写满会连带影响 PG/Redis 与系统本身。
- **允许超配，但必须可见**。管理页显示「物理可用 / 可分配 / 已承诺 / 实际已用」，
  超配时警告而不阻止——多数账号用不满，禁止超配会浪费容量。
- **两种「满」的错误信息必须分开**，这是本层的主要价值：
  「你的空间已满」用户删自己的文件可解，「服务器存储不足」删自己的文件无用。
  混成一句会让用户白忙。
- 「不限」配额同样受池上限约束，否则「不限」等于绕过池判定。

### 已知弱点

1. **配额对不诚实的客户端无强制力。** 校验用的是客户端申报的 `size`：申报 1 字节、
   实际传 50 GB，本次放行，且一次签名 15 分钟内可反复使用，超额只能在**下一次**签发时
   被发现——那时盘已经满了。当前唯一客户端是本项目的 Go 二进制，所以实践中不构成问题，
   但这是部署现状，不是安全属性。
   彻底的解法有三条，均未采用：把配额下推到 RustFS 桶级策略（强制发生在字节落地处）；
   对配额敏感的空间改走控制面代理上传（放弃直传收益）；或签发时即按申报量预扣、
   事后对账冲正。**此项保持开放**，需要产品侧决定「配额是硬约束还是运营指标」。
2. **共享空间当前是全局单例**（`space_id = 1` 由 DDL 预置，前缀固定 `shared/`）。
   表结构本身支持 N 个共享空间，只有前缀与该常量把它锁成一个。若将来要按团队/项目分多个
   共享空间，需改前缀为 `shared/{spaceId}/` 并做一次数据迁移。
3. **管理页的全桶用量聚合无缓存**：为在一屏显示每个账号的已用量，`sumBytesGrouped()`
   单次遍历全桶按前缀分组，但未走 60 秒缓存，每次打开管理页都会全量遍历一次。
   对象量大时会成为该页的加载瓶颈。

## 验证

- **P0（已完成 2026-08-29）**：RuoYi-Vue-Plus 6.0 在 PostgreSQL 16 + 专用 Redis(6380) 上启动成功，`POST /auth/login`（admin/admin123）返回 200 与合法 JWT。环境与复现见 [`../../deploy/ruoyi-db/README.md`](../../deploy/ruoyi-db/README.md) 与迭代记录 [`../iterations/2026-08-29-account-control-plane-p0.md`](../iterations/2026-08-29-account-control-plane-p0.md)。
- 后续阶段（P1 桌面登录 / P2 存储隔离 / P3 管理员+注册开关 / P4 滑动开关+共享盘）各自建迭代记录并在本 ADR 更新验证状态。
- **P1（已完成 2026-08-29）**：桌面客户端登录门禁 + 登录态贯通（主界面与悬浮窗头像跟随账号、登出、点头像进设置）。见 [`../iterations/2026-08-29-account-p1-desktop-login.md`](../iterations/2026-08-29-account-p1-desktop-login.md)。
- **P2（已完成 2026-08-29）**：按用户隔离的存储授权落地，KI-2 关闭、KI-3 的用户隔离部分关闭。见 [`../iterations/2026-08-29-account-p2-storage-isolation.md`](../iterations/2026-08-29-account-p2-storage-isolation.md)。

  不变量逐条验证状态：

  | 不变量 | 状态 | 证据 |
  | --- | --- | --- |
  | 1 静态 AK/SK 只在控制面 | **已验证** | 用 `deploy/rustfs/.env` 真实凭据字节搜索产物：`easyshare.exe`(11.5MB)、`core.exe`(10.6MB) 均不含 AccessKey/SecretKey。`internal/cloud/defaults.go` 已删，`cmd/core/main.go` 不再构造 `s3store` |
  | 2 用户命名空间前缀 + 服务端强制校验 | **已验证** | `scripts/verify-drive-isolation.sh` 9/9：B 列表不含 A 的文件、B 同名相对路径落回自己空间、三种穿越路径（`../`、`../../users/{id}/`、`/`）全被拒、未登录 401。`DriveKeysTest` 13/13 含 `users/1` vs `users/11` 前缀相似边界。活栈集成测试另断言列举结果不泄漏 `users/` 前缀 |
  | 3 Core 不持云端长期凭据、不做权限裁决 | **已验证** | 同不变量 1 的字节搜索。Core 的 `/api/cloud/*` 已无生产调用方（`server.cloud` 为 nil，一律 503），但路由与死代码仍在产物中——已登记 [KI-5](../known-issues.md#ki-5core-侧云盘路由已无调用方但仍可绕过用户隔离) |
  | 4 账号真相源在 RuoYi PostgreSQL | 沿用 P1 状态 | 本轮未改动账号存储；`DriveController` 的身份一律取自 `LoginHelper.getUserId()`，不接受客户端传入的用户标识 |
  | 5 生产开启接口加密与验证码 | **未验证** | 本地 dev 仍以 `--api-decrypt.enabled=false --captcha.enable=false` 启动。生产加密适配仍未做，与 P1 同状态 |

  开放问题第 3 条（存储授权模块形态）**已收敛**：取"RuoYi 内 Java 模块"，即 `platform-drive/`（顶层独立 Maven 模块，父 POM 指向 `platform/pom.xml`），复用其 OSS 依赖与 AWS SDK v2。选顶层而非塞进 `ruoyi-system` 的理由是 `platform/` 被 gitignore，模块代码需纳入版本管理。
