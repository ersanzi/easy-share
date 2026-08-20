# 薄控制面 2a：账号与登录

> 发芽路线第一步（定位见 [`../product-positioning.md`](../product-positioning.md) §三）。开工：2026-08-20。

## 用户问题

知识平台是多人产品（用户判断："没有控制面就没有意义"）。种子发芽的前提是 30 人公司能以各自账号使用，且为 2b 文件归属、2c 权限感知检索（`doc_ids` 钩子已就位）打地基。

## 目标

- SQLite 用户库 + PBKDF2 口令哈希（零新依赖）；首管理员 bootstrap、登录发令牌（Bearer）。
- `AUTH_ENABLED=true` 时保护写路径与 `/query`；**默认关闭**，关闭时行为与现状逐字节一致。
- 管理员可创建成员账号；成员不能管理用户。

## 非目标

- 不做 RBAC 细粒度、多租户、组织架构（2c 及以后）。
- 不做文件归属（2b）与权限过滤（2c）。
- 不动桌面端（登录入口随桌面端集成迭代做）。

## 设计决策

- 令牌为随机串存 SQLite（带过期），重启存活；与 job_store/query_log 同模式（per-call 连接 + 锁）。
- 保护用 FastAPI HTTP 中间件而非逐路由依赖：白名单 `/auth/* /health /docs /lab /debug`，保护前缀 `/documents /query /ingest /lab/api/uploads`；`/debug` 仅回环可达已有守卫，暂不叠加令牌。
- Java 控制面推迟：接口按可拆设计（UserStore 只依赖 SQLite，未来可平移 PostgreSQL/Java）。

## 测试计划

- bootstrap 首建/重复拒绝；登录成功/口令错误；令牌访问受保护路由（带/不带）；管理员建用户、成员建用户被拒；默认关闭不影响现有行为（既有 109 条回归兜底）。

## 完成记录

### 已完成（2026-08-20）

- `app/auth/store.py`：UserStore（users + tokens 两表，PBKDF2 100k 迭代，hmac.compare_digest 防时序比较，过期令牌惰性清理）；`app/auth/routes.py`：bootstrap/login/me/users 四组端点。
- `app/main.py`：HTTP 中间件——带合法令牌一律解析 `request.state.user`（auth 关闭时 /auth/me 仍可用），仅 `AUTH_ENABLED=true` 时强制保护业务前缀；白名单 `/auth` `/health` `/docs` `/lab` `/debug`（后者另有回环守卫）。
- 配置三字段：`auth_enabled`（默认 False）/`auth_db_path`/`auth_token_expiry_hours`；helpers 支持 `config_overrides` 便于后续测试。
- 修复一处循环导入（auth `__init__` 只导出存储层，路由由 main 挂载）。
- 测试：新增 `tests/test_auth.py` 5 条（bootstrap 一次性/匿名 401+登录全链路+白名单/口令错误/管理员建成员+成员权限边界/默认关闭保持旧行为）；全量 `pytest -m "not integration"` **114 passed, 1 skipped**。
- 同步 `architecture.md` §11。

### 已知限制与后续工作

- 2b 文件归属（入库记录 owner）与 2c 权限感知检索（用户→可见 doc_ids 注入 /query）按发芽路线推进。
- 桌面端登录入口随"桌面端集成知识问答"迭代做。
- 令牌无吊销列表、无修改口令端点——多人真实使用前补。
