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
docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-user-permission.sql
docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-user-profile.sql

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

## 长时间登录（2026-10-20；2026-10-25 改滑动窗口）

桌面端会把账号令牌落盘到 `%LOCALAPPDATA%\EasyShare\account-session.json`（用户级目录，0600；不存密码），重启后自动恢复。控制面 PC 客户端默认是 7 天绝对有效期、30 分钟空闲失效，关掉客户端半小时后仍会要求重登；执行 `easyshare-client-session.sql` 把该客户端改为 **365 天绝对上限 + 30 天空闲有效期**——实际语义是滑动窗口：只要 30 天内用过一次就一直免登录，连续 30 天不用才要求重登（2026-10-25 前是 30 天绝对有效期，天天用的员工每满 30 天仍要重登一次，不满足「一个月内登录过即免登录」）。长寿命令牌的安全前提：桌面端每次启动都向控制面校验会话，停用账号/后台踢会话立即生效。

```powershell
docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-client-session.sql
```

改动只影响新签发的令牌：已在线令牌的 TTL 在签发时已定，老令牌自然到期后重登一次即进入新窗口。免登录还要求桌面端 ≥ 0.5.1（此前版本无会话落盘恢复，每次启动必重登）。

`scripts/ship-control-plane.ps1` 已把它加入自动投递的 SQL 列表，后续部署会自动重放；已有部署需手动执行一次或重跑投递脚本。如果控制面已缓存 PC 客户端（Redis `sys_client#30d`），执行后还需在后台「客户端管理」保存一次触发缓存失效，或清理对应缓存；否则仍按旧的 30 分钟空闲。

## 功能权限与个人资料（2026-10-27）

三张新表，都幂等（`CREATE TABLE IF NOT EXISTS`），`scripts/ship-control-plane.ps1` 会自动重放：

```powershell
docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-user-permission.sql
docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-user-profile.sql
docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-permission-role.sql
```

- `es_user_permission`：一行一账号的五项功能开关（知识问答 / 会议听记 / 网盘 / 共享空间 / 插件中心）。**没有行 = 五项全开**，存量账号不需要迁移；只有管理员关过某项的账号才会落行。网盘关闭时共享空间一并失效。接口：`/easyshare/permission/mine`（登录即可读自己）、`/easyshare/permission/admin/list` 与 `PUT /easyshare/permission/admin`（超管）。
- `es_permission_role` + `es_user_role`：**角色/权限组与子管理员**（2026-10-27 第三轮；2026-11-05 管理范围表达式化）。成员挂了角色后，五项开关取所挂角色的并集，逐账号行暂不参与；没挂角色时仍走 `es_user_permission`，再没有行则全开。`admin_scope` 非 `none` 的角色是「子管理员」，成员能进管理页的权限矩阵，范围按档位裁剪：`dept`（本部门精确匹配）/ `dept_children`（本部门及下级，按 `sys_dept.parent_id` 内存 BFS）/ `specified`（`scope_dept_ids` 逗号分隔的部门 ID，2026-11-05 新增列，幂等补列）/ `all`（全部成员）；多个管理角色取并集。角色定义与分配仅超管可改；删除角色时，只挂该角色的成员会先落一行逐账号快照，不会被放大成全开。接口：`GET /easyshare/permission/admin/members`（超管/子管理员，按范围裁剪）、`/admin/roles` 增删改与 `PUT /admin/members/{userId}/roles`（超管）。
- `es_user_profile`：头像字节（≤2MB，按文件头嗅探，拒绝 SVG）。不用 RuoYi 的 `sys_user.avatar` + `sys_oss`，原因是标准上传接口需要 `system:oss:upload` 权限、普通成员没有。读接口 `/easyshare/profile/avatars/{userId}` 匿名可访问（`<img>` 发不了 Authorization），带 7 天缓存与版本号；写接口要求登录本人。昵称与密码仍走 RuoYi 标准个人中心 `/system/user/profile`。

## 文档级成员角色（2026-11-05，企业权限第三片）

一张新表，幂等，`scripts/ship-control-plane.ps1` 自动重放：

```powershell
docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-file-member.sql
```

- `es_file_member`：一行 = 一个主体（user/dept）对一份共享空间文件的显式角色，四档 `manage/edit/download/read`（删除是 manage 独占，对齐钉钉）。裁决收口 `FileRoleService.ladder`：上传者恒 manage → 成员行取高 → `es_file.visible_depts` 命中=download → 空间默认（write 成员 manage / read 成员 download）→ 不可见。列表过滤、下载签发、删除、改名、覆盖上传、改可见性六处共用同一份梯子。**未配角色的文件行为与本表引入前完全一致**。护栏：不能修改自己的角色行；仅上传者/manage 档可授权（`/easyshare/file-member/*`）。成员行随回收站 purge 摘行清理。

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
