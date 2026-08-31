# 2026-08-29 账号控制面 P0：RuoYi-Vue-Plus 6.0 环境落地

> 关联：[ADR-0007](../adr/0007-account-control-plane-ruoyi.md)、[方案（P0-P4）见批准计划]、[`../../deploy/ruoyi-db/README.md`](../../deploy/ruoyi-db/README.md)

## 用户问题

云盘要多用户（登录、A/B 文件隔离、管理员、注册开关、后续共享盘）。现状无账号体系，且有两个前置缺陷 KI-2（凭据编译期写死）/ KI-3（object key 无用户隔离）。经 ADR-0007 决策：账号控制面用 RuoYi-Vue-Plus 6.0（Java）。本迭代是分阶段路线的 **P0：把控制面环境在本地跑起来**。

## 目标与非目标

### 目标
- RuoYi-Vue-Plus 6.0 在本地 PostgreSQL + Redis 上启动成功。
- 登录可用（返回 JWT），默认管理员/普通用户可用。
- 环境可复现（compose + 建表脚本 + 运行脚本 + 文档）。

### 非目标
- 不接入桌面客户端登录（P1）。
- 不做 RustFS 按用户隔离 / 存储授权（P2，修 KI-2/KI-3）。
- 不搭 RuoYi 的 Vue 管理后台（plus-ui 独立仓，P3 再做）。
- 不接多租户；不上服务器（本地 dev）。

## 完成记录

已完成并验证。

### 落地内容
- **依赖服务**：`deploy/ruoyi-db/compose.yaml` —— PostgreSQL 16（库 `ryvue`，用户 `ruoyi/ruoyi123`，:5432）+ 专用 Redis 7（密码 `ruoyi123`，:6380）。
- **RuoYi 工程**：仓内 `platform/`（`git clone` 自 gitee `6.X` 分支，revision 6.0.0；已加入 `.gitignore`，自带 .git 不纳入 Go 仓）。
- **改数据库为 PostgreSQL**：`platform/ruoyi-admin/src/main/resources/application-dev.yml` 主库 driver→`org.postgresql.Driver`、url→`jdbc:postgresql://localhost:5432/ryvue`、账号 ruoyi/ruoyi123；redis→6380/ruoyi123。`platform/ruoyi-admin/pom.xml` 解开 postgresql 驱动依赖。
- **建表**：`script/sql/postgres/` 的 4 个脚本（ry_vue/workflow/job/ai）灌入 `ryvue`，0 报错，81 张表。
- **构建/运行**：`./mvnw clean package -DskipTests`（JDK 21，pom 华为云镜像）→ `ruoyi-admin.jar`；`deploy/ruoyi-db/run-ruoyi-admin.ps1` 固化 dev 运行参数。

### 验证结果
```text
docker compose up -d            PG healthy + Redis PONG（带密码）
建表脚本 x4                      0 ERROR，public schema 81 表
./mvnw clean package -DskipTests  BUILD SUCCESS（4:48，含首次下依赖）
java -jar ...(dev flags)         RuoYi-Vue-Plus 启动成功，8090 监听，OSS 配置初始化成功
POST /auth/login admin/admin123  code:200，返回合法 JWT（userName=admin）
默认账号                          admin/admin123（超管）、test|test1/666666（普通用户）
```

### 踩的坑（本机相关，均已在配置/脚本处理）
1. **Redis AUTH 失败**：本机原生 Redis(6379) 无密码，RuoYi 的 Redisson 即便空密码仍发 AUTH 被拒（`ERR Client sent AUTH, but no password is set`）。→ 起带密码的专用 Redis 容器(6380)，不动本机原生。
2. **8080 端口"已占用"但无进程**：Hyper-V/WSL 保留了 TCP `7987-8086`（`netsh interface ipv4 show excludedportrange protocol=tcp`），8080 落在其中，bind 报已占用。→ dev 改 8090。
3. **登录 403「没有访问权限」**：`/auth/login` 带 `@ApiEncrypt`，`api-decrypt.enabled=true` 时要求 RSA/AES 加密报文；明文 curl 被 CryptoFilter 拒。→ dev `--api-decrypt.enabled=false`（生产保持开启）。验证码同理 dev `--captcha.enable=false`。
4. **多次快速重启致 8080 短暂重叠**：等前一 java 进程退干净再启。

### 已知限制与待验收项
- dev 关闭了接口加密与验证码；生产以 `application-prod.yml` 为准（保持开启）。
- 未接桌面客户端、未做存储隔离、未搭 Vue 后台——分别是 P1/P2/P3。
- RuoYi 6.0 用 Spring Boot 4.1（很新）；启动日志有 liteflow 的兼容告警（非致命，未影响启动）。若后续阶段暴露框架问题，按 ADR-0007 备选可回退 5.x。

## 回滚方式
`platform/` 已 gitignore、独立 .git；删除 `platform/` 与 `deploy/ruoyi-db/`、`docker compose down -v` 即可完全移除，不影响 Go/Wails 主体。Go 仓仅新增 `.gitignore` 一行与 `deploy/ruoyi-db/`、`docs/` 文档。

## 后续
P1 桌面登录 → P2 存储隔离（修 KI-2/KI-3）→ P3 管理员+注册开关（含搭 plus-ui 后台）→ P4 滑动开关+共享盘。
