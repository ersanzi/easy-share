# EasyShare 控制面本地开发环境（RuoYi-Vue-Plus 6.0）

账号体系（P0）的本地开发依赖：PostgreSQL + 专用 Redis（本目录 compose）+ RuoYi-Vue-Plus 6.0（在仓内 `platform/`，已 gitignore）。

## 组成

| 组件 | 位置 / 端口 | 说明 |
| --- | --- | --- |
| PostgreSQL 16 | 容器 `easyshare-ruoyi-pg`，`127.0.0.1:5432`，库 `ryvue`，用户 `ruoyi/ruoyi123` | RuoYi 账号/权限数据 |
| 专用 Redis 7 | 容器 `easyshare-ruoyi-redis`，`127.0.0.1:6380`，密码 `ruoyi123` | 6380 避开本机原生无密码 Redis(6379) |
| RuoYi-Vue-Plus 6.0 | `platform/ruoyi-admin`，HTTP `8090` | 控制面后端；JDK 21、Spring Boot 4.1 |

## 首次搭建

```powershell
# 1) 起 PG + Redis
Set-Location deploy/ruoyi-db
docker compose up -d

# 2) 灌入 RuoYi 的 PostgreSQL 建表脚本（4 个）
Set-Location ../../platform/script/sql/postgres
foreach ($f in 'postgres_ry_vue.sql','postgres_ry_workflow.sql','postgres_ry_job.sql','postgres_ry_ai.sql') {
  docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < $f
}

# 3) 构建（首建下依赖，走 pom 里的华为云镜像）
Set-Location ../../..    # 回到 platform/
$env:JAVA_HOME = 'C:\Program Files\Microsoft\jdk-21.0.8.9-hotspot'
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

1. **数据源改 PostgreSQL**：`platform/ruoyi-admin/src/main/resources/application-dev.yml` 主库 driver/url 改 PG；`platform/ruoyi-admin/pom.xml` 解开 postgresql 驱动依赖。
2. **Redis 6380 + 密码**：本机原生 Redis(6379) 无密码，RuoYi 的 Redisson 即使空密码也发 AUTH 会被拒；故用带密码的专用容器(6380)。dev.yml 已配 6380/ruoyi123。
3. **端口 8090**：本机 Hyper-V/WSL 保留了 TCP 7987-8086 区间（`netsh interface ipv4 show excludedportrange protocol=tcp`），8080 落在里面 bind 会报"已占用"。故 dev 用 8090（run 脚本 `--server.port=8090`）。
4. **dev 关验证码与接口加密**：`--captcha.enable=false --api-decrypt.enabled=false`，方便本地用明文调 API。生产用 `application-prod.yml`，两者仍开启。

> RuoYi 的 Vue 管理后台（plus-ui，独立仓）尚未接入——P3 做管理员控制台时再搭。当前 P0 用 API 验证登录即可。
