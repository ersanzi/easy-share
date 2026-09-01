# EasyShare 2b 文件归属 / 2c 权限感知检索（最小切片）

## 用户问题

- 公司部署在即：多账号（admin/同事）各自真实使用时，知识服务 `/query` 会把**所有人**的文档混在一起检索与回答——同事 A 的提问可能引用同事 B 的私有文档（合同/报价/人事），轻则尴尬，重则大家不敢往知识库放文档，两周使用率观察失真。
- 当前知识服务已有完整账号体系（`app/auth`：users/tokens/role=admin|member）与 `/query` 的 `doc_ids` 过滤钩子（`QueryRequest.doc_ids` → `retriever.retrieve`），但没有任何一层记录「文档属于谁」，也就无法按用户裁剪检索范围。

## 目标

- 2b 文件归属：`ProcessingJob`、manifest、向量索引 chunk metadata 均记录 `owner`（登录用户名）；入库三条路径（`/documents/process`、`/lab/api/uploads`、watcher 目录监听）各自明确 owner 来源。
- 2c 权限感知检索：`/query` 从请求令牌解析用户，服务端计算可见 doc_id 集合（共享文档所有人可见；owner 文档仅本人与 admin 可见），与请求显式 `doc_ids` 取交集后检索；未登录（auth 关闭的本地 /lab）行为不变。
- 权限边界有自动化测试覆盖（入库落 owner、隔离检索、admin 全见、旧数据兼容）。

## 非目标

- 不做租户/部门级 ACL、分享链接、按目录授权（里程碑 2 Java 控制面范畴）。
- 不动 RuoYi 账号体系：知识服务沿用自身 users 表，长期仍按 ADR 走 Java 统一账号。
- 不改桌面端/前端：Core 知识网关已代理登录令牌，服务端解析即可生效。
- watcher 监听目录本切片一律 owner=共享（None），不按目录配置归属。

## 设计决策

- **owner 语义**：`owner: str | None`（知识服务用户名）。None/缺失 = 共享文档（所有人可见，存量数据与 watcher 监听目录天然落入此语义）；非空 = 仅本人与 admin 可见。不引入租户/部门/分享等更复杂模型。
- **身份源**：知识服务自身 users 表（`app/auth`，role=admin|member）。长期仍按 ADR 走 Java 统一账号，本切片不动 RuoYi——桌面端知识页/ WPS 加载项本来就登录知识服务账号，闭环内自洽。
- **归属优先级（防伪造）**：`/documents/process` 带令牌时**一律以令牌用户为准**，请求体显式 `owner` 仅服务无令牌的内部/测试调用；lab 上传（要求登录）谁传归谁；watcher 进程内调用落 None（共享）。放弃了"显式指定 + 服务端校验权限"方案——多一种路径多一种绕过面。
- **注入点唯一**：`/query` 服务端（`app/rag/permissions.py`）。Core 知识网关已透传 `Authorization: Bearer`，桌面端、WPS、浮窗问答链路**零改动**生效。`/debug/query` 驾驶舱不做过滤（回环限定的管理员调参视角，过滤反而干扰策略对比）。
- **空交集必须短路**：`VectorStore.query` 对空列表 `if doc_ids:` 不过滤——把"无可见文档"直接传入会把语义放大成"可见全部"。短路逻辑集中在 `effective_doc_ids`，路由层显式判 `== []`。
- **可见集数据源**：向量库新增 `doc_owners()`（JSON 实现：内存 records 聚合；Milvus 实现：标量查询 + offset 分页）。放弃从 manifest（对象存储）聚合——每次 /query 读对象存储太重。
- **`ProcessingJob.owner` 放 dataclass 末尾带默认值**：5 处既有构造点全部关键字传参，零改动。

## 兼容与迁移

- SQLite `processing_jobs` 表通过 `PRAGMA table_info` 检查 + `ALTER TABLE ADD COLUMN owner TEXT` 自动迁移，旧库打开即升级，存量任务 owner=NULL（共享）。
- 存量向量库记录无 owner 字段 → 按「共享文档」处理，无迁移脚本。
- manifest 增加 `owner` 字段（可选），`schema_version` 保持 1，旧 manifest 读取不受影响。
- auth 关闭或未带令牌的 `/query` 行为与之前完全一致（不过滤）。
- config.json 无新增字段。

## 测试计划

- pytest：权限切片集中测试（owner 落 manifest/索引、令牌优先于显式 owner、member 隔离、admin 全见、显式 doc_ids 交集、无登录兼容、旧记录共享语义、旧任务库迁移）。
- 既有回归全绿（`-m "not integration"`）。
- 手工冒烟：本地起服务，双账号验证问答隔离。

## 发布与回滚

- 纯知识服务（Python）改动，无桌面端/构建产物变化。
- 回滚 = 还原 knowledge/ 代码并重启服务；数据无破坏性变更（只增列/增字段）。

## 完成记录

**已完成（2026-09-01，当日完成）**：

- `app/jobs/store.py`：`ProcessingJob.owner`（dataclass 末尾默认 None）+ 建表带列 + 旧库 `ALTER TABLE` 自动迁移 + `create_or_get(owner=)` + `_from_row`
- `app/api/schemas.py`：`ProcessDocumentRequest.owner`（无令牌内部调用用）、`ProcessingJobResponse.owner`、`ArtifactManifestResponse.owner`
- `app/pipeline/service.py`：owner 落 chunk `metadata.owner` 与 `manifest.owner`
- `app/kb/store.py` / `app/kb/milvus_store.py`：`doc_owners()` 聚合接口（双后端）
- `app/rag/permissions.py`（新）：`visible_doc_ids` / `effective_doc_ids`，空交集短路语义集中一处
- `app/api/routes.py`：`/documents/process` 令牌用户优先解析 owner（含忽略日志）；`/query` 服务端注入可见集
- `app/lab/routes.py`：上传按登录用户落 owner
- watcher 不动（监听目录 = 共享文档语义，符合公司共享知识库主场景）

**测试结果**：新增 `tests/test_permission_visibility.py` 8 用例全绿；全量回归 128 passed / 1 skipped / 1 deselected（2026-09-01）。手工冒烟（本地真实服务，auth 开启 + 真实百炼 embedding + SenseNova LLM）：smoke-a/smoke-b 各传一份文档，`/query` 互相不可见对方文档、job 响应 owner 正确落库——隔离在真实链路生效。

**已知问题与边界**：

- LLM 供应商限流（429 TPM）时 `/query` 整体 500——既有行为（生成异常不降级），冒烟时撞上一次，与本切片无关；可作为后续小切片（生成失败降级纯检索）。
- `/debug/query`（驾驶舱）不做权限过滤，属管理员调参视角，已在 architecture.md 标注。
- 冒烟数据留在本地 dev 实例（smoke-a/smoke-b 用户、两份 smoke 文档）：owner 私有语义下不干扰其他账号，admin 视角可见可手动清理。
- 排障记录（环境，非代码）：本地 knowledge/.env 的 RustFS 凭据与 `easyshare-rustfs` 容器实际 env 漂移（`InvalidAccessKeyId`）——以 `docker inspect easyshare-rustfs` 的 env 为准更新 .env 即恢复；另见真机验收环境清单。
