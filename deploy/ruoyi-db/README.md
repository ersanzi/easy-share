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

登录验证（默认账号 admin/admin123；普通用户 test/test1 密码 666666）：

```powershell
curl -X POST http://localhost:8090/auth/login -H "Content-Type: application/json" `
  -d '{"clientId":"e5cd7e4891bf95d1d19206ce24a7b32e","grantType":"password","username":"admin","password":"admin123"}'
```

返回 `code:200` 且带 `access_token` 即成功。

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
