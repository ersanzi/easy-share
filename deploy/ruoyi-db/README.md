# EasyShare 控制面本地开发环境（RuoYi-Vue-Plus 6.0）

账号体系（P0）的本地开发依赖：PostgreSQL + 专用 Redis（本目录 compose）+ RuoYi-Vue-Plus 6.0（在仓内 `platform/`，已 gitignore）。

## 组成

| 组件 | 位置 / 端口 | 说明 |
| --- | --- | --- |
| PostgreSQL 16 | 容器 `easyshare-ruoyi-pg`，宿主 `127.0.0.1:5433` → 容器 5432，库 `ryvue`，用户 `ruoyi/ruoyi123` | RuoYi 账号/权限数据；本机原生 PG 服务占用 5432（凭据不明，勿动），故宿主映射 5433 |
| 专用 Redis 7 | 容器 `easyshare-ruoyi-redis`，`127.0.0.1:6380`，密码 `ruoyi123` | 6380 避开本机原生无密码 Redis(6379) |
| RuoYi-Vue-Plus 6.0 | `platform/ruoyi-admin`，HTTP `8090` | 控制面后端；JDK 21（本机 `D:\Develop\java21`）、Spring Boot 4.1 |

## 首次搭建

```powershell
# 1) 起 PG + Redis
Set-Location deploy/ruoyi-db
docker compose up -d

# 2) 灌入 RuoYi 的 PostgreSQL 建表脚本（4 个基础 + 2 个 EasyShare 业务表）
Set-Location ../../platform/script/sql/postgres
foreach ($f in 'postgres_ry_vue.sql','postgres_ry_workflow.sql','postgres_ry_job.sql','postgres_ry_ai.sql') {
  docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < $f
}
Set-Location ../../..    # 回到仓库根
docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-space.sql
docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-app-release.sql
docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-plugin.sql
docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-file.sql

# 3) 构建（首建下依赖，走 pom 里的华为云镜像；可选模块 ruoyi-snailai-server 因
#    milvus/es 依赖下载损坏会失败，不影响 ruoyi-admin fat jar 产出）
Set-Location platform
$env:JAVA_HOME = 'D:\Develop\java21'
./mvnw clean package -DskipTests
```

## 日常启动

```powershell
deploy/ruoyi-db/run-ruoyi-admin.ps1
```

登录验证使用已开通的测试账号；口令由本机安全方式注入，不在仓库中维护：

```powershell
curl -X POST http://localhost:8090/auth/login -H "Content-Type: application/json" `
  -d '{"clientId":"e5cd7e4891bf95d1d19206ce24a7b32e","grantType":"password","username":"<测试账号>","password":"<本机注入口令>"}'
```

返回 `code:200` 且带 `access_token` 即成功。

## Windows个人CFAPI目录协议与迁移

此通路依赖本仓 `platform-drive`，不能只升级桌面源码。详情见 [Windows云端主存说明](../../docs/windows-cloud-primary.md)。

1. 按已有部署顺序准备 `easyshare-space.sql`、`easyshare-file.sql`、回收站及增量列举等业务迁移。
2. 备份数据库，在维护窗口中执行 `easyshare-directory.sql`：为 `es_file`、`es_file_change_log` 添加目录类型，并建立空间内文件/目录冲突及非空树保护。必须在 file/recycle/incremental-list 之后执行；可重复运行。
3. 构建并部署配套控制面。`scripts/ship-control-plane.ps1` 的SQL清单已把此迁移放在已有业务迁移之后；本次源码提交不自动运行该脚本或修改服务器。
4. 用测试账号认证调用 `GET /easyshare/drive/capabilities`，确认协议1与持久目录；测试持久空目录、旧 `/mkdir` 尾斜杠响应和 `includeDirs`，再启用新客户端。
5. 回退只回退程序并保留新增列和用户数据，不盲删目录列/触发器，不把旧本机状态覆盖新pending。

兼容说明：旧目录marker被列表认领为持久目录身份，默认对象清单返回 `directory:true`；`includeDirs=true` 为旧客户端保留尾斜杠展示。明确递归删除按子项到父项软删并保留文件正文，旧marker清理不删除文件内容；非递归非空目录拒绝。

### 可复现Java回归

先按下方接线要求准备RuoYi基础源码，父POM版本为6.0.0、本机安装JDK21。第三方 `platform/` 不提交本产品仓；配置只在本机或受保护环境维护。

```powershell
# 在仓库根执行，-am带上父工程与依赖模块；不要把BUILD SUCCESS当作已跑测试
mvn -B -ntp -f platform/pom.xml -pl ../platform-drive -am clean test `
  -Dmaven.test.skip=false -DskipTests=false -Drevision=6.0.0
```

检查Surefire输出/报告中的实际用例数、Failures/Errors/Skipped。目录反射、预签重试及兼容回归均不连接生产数据库；SQL迁移回归只能在新建测试库执行。此次基于已准备的RuoYi6.0基础工程验证，不宣称已经完成全新服务器安装演练。

## 长时间登录（2026-10-20）

桌面端会把账号令牌落盘到 `%LOCALAPPDATA%\EasyShare\account-session.json`（用户级目录，0600；不存密码），重启后自动恢复。控制面 PC 客户端默认是 7 天绝对有效期、30 分钟空闲失效，关掉客户端半小时后仍会要求重登；执行 `easyshare-client-session.sql` 把该客户端改为 **30 天绝对有效期 + 30 天空闲有效期**：

```powershell
docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-client-session.sql
```

`scripts/ship-control-plane.ps1` 已把它加入自动投递的 SQL 列表，后续部署会自动重放；已有部署需手动执行一次或重跑投递脚本。如果控制面已缓存 PC 客户端（Redis `sys_client#30d`），执行后还需在后台「客户端管理」保存一次触发缓存失效，或清理对应缓存；否则仍按旧的 30 分钟空闲。

## 关键改动与本机坑（都已在配置/脚本里处理）

1. **数据源改 PostgreSQL**：`platform/ruoyi-admin/src/main/resources/application-dev.yml` 主库 driver/url 改 PG（`jdbc:postgresql://localhost:5433/ryvue`）；`platform/ruoyi-admin/pom.xml` 解开 postgresql 驱动依赖、加 easyshare-drive 模块依赖；`platform/pom.xml` 注册 `<module>../platform-drive</module>`（克隆后需重新做这三处接线）。
2. **PG 宿主端口 5433**：本机装有原生 PostgreSQL 服务占 5432（凭据不明），compose 映射宿主 5433（见 compose.yaml 注释）。
3. **Redis 6380 + 密码**：本机原生 Redis(6379) 无密码，RuoYi 的 Redisson 即使空密码也发 AUTH 会被拒；故用带密码的专用容器(6380)。dev.yml 已配 6380/ruoyi123。
4. **端口 8090**：本机 Hyper-V/WSL 保留了 TCP 7987-8086 区间（`netsh interface ipv4 show excludedportrange protocol=tcp`），8080 落在里面 bind 会报"已占用"。故 dev 用 8090（run 脚本 `--server.port=8090`）。
5. **dev 关验证码与接口加密**：`--captcha.enable=false --api-decrypt.enabled=false`，方便本地用明文调 API。生产用 `application-prod.yml`，两者仍开启。
6. **JDK21 路径**：本机在 `D:\Develop\java21`；系统全局 JAVA_HOME 是 17，run 脚本已强制覆盖（报 `class file version 65.0` 就是没吃到 21）。
7. **匿名升级接口白名单**：`easyshare-drive.yml` 定义 `security.excludes`（整列表替换 jar 内基线，已逐条核对 6.0.0），放行 `/easyshare/app/latest` 与 `/easyshare/app/assets/*/url`——RuoYi 的路由级 checkLogin 不认 `@SaIgnore`，只能走白名单。
8. **镜像拉取**：Docker Hub 直拉易超时，postgres:16 用 `docker pull docker.m.daocloud.io/library/postgres:16` 再 `docker tag`。

> RuoYi 的 Vue 管理后台（plus-ui，独立仓）尚未接入——P3 做管理员控制台时再搭。当前 P0 用 API 验证登录即可。
