# EasyShare 生产检索升级 — 混合/重排回灌 /query + 查询日志贯通

## 用户问题

- 2026-09-01 完成 [Agent 知识库六年演进对标](../agent-knowledge-benchmark.md) 后确认一个落差：里程碑 1.9 建成的全部检索加固能力（RRF 混合检索、BM25、Reranker 精排、预算多跳）**只接在 `/debug` 驾驶舱路由上**，生产 `/query` 仍是"权限裁剪 → 单路向量 → 生成"。驾驶舱里四个策略并排对比做得再好，公司同事日常问答走的还是最弱的那条路。
- 生产 `/query` **从不写查询日志**（`QueryLog` 只有驾驶舱与多跳 hop 在记）。公司部署两周观察的核心度量——使用率、高频命中、盲区查询——数据源恰恰是生产查询日志；不补上，观察期最重要的仪表盘是空的。
- 顺带发现的隐藏 bug：`Retriever._ensure_bm25_index`、驾驶舱 BM25 懒构建、`/health`、健康度聚合四处直接读 `store.records`，而 **Milvus 后端没有 `records` 属性**（只有 JSON 实现）——生产 Milvus 部署下，embedding 故障降级、驾驶舱混合对比、`/health` 会直接 `AttributeError`。

## 目标

- 新增查询编排层 `QueryOrchestrator`：按 `QUERY_STRATEGY` 配置调度检索（vector / hybrid / hybrid_rerank / multi_hop），统一降级链与查询日志，`/query` 全面切换到编排层。
- 生产查询落 `QueryLog`（retrieval + generation 两类事件），观察期数据源补齐。
- 向量库双后端补 `count()` / `snapshot_records()` 协议，修掉 Milvus 下读 `records` 的隐藏 bug，BM25 懒构建在双后端下均可用。
- `QueryResponse` 透出实际执行策略与降级说明，检索行为可观测、可归因。

## 非目标

- 不改检索算法本身（RRF 融合、rerank、多跳逻辑全部复用 1.9 已建成组件，本切片只做"接线"）。
- 不把策略暴露为请求参数——部署级配置，终端用户无感知（技术参数自动推断原则）。
- 不做 contextual chunking / OKF frontmatter / 文档级路由（对标 P1 项，另行排期）。
- 驾驶舱 `/debug/query` 行为不变（管理员调参视角，继续多策略并排对比）。

## 设计决策

- **策略是部署配置不是用户选择**：`QUERY_STRATEGY` 环境变量（默认 `hybrid`），取值 vector / hybrid / hybrid_rerank / multi_hop。中小制造业用户不理解也不该理解 queryMode——由部署者（或未来控制面）按算力预算定一次。
- **编排独立成层**：`app/rag/orchestrator.py` 的 `QueryOrchestrator` 只做策略调度 + 降级 + 日志，不含检索算法。`/query` 路由保持薄（权限裁剪 → 编排 → 生成），检索策略细节不泄漏进路由层。
- **降级只降不炸**：multi_hop 未配置 LLM → 降 hybrid_rerank（rerank 未配置时 NoopReranker 等价 hybrid）；hybrid 下 embedding 故障 → `Retriever` 内部既有 BM25 降级兜住。响应 `strategy` 字段回告**实际**执行策略，`degraded` 字段说明降级原因，查询日志同样按实际策略记录——将来驾驶舱归因时不会把降级查询算进错误策略。
- **候选池配比**：混合召回每路 `top_k*2`，RRF 融合池 `top_k*3`，精排/截断回 `top_k`（与驾驶舱 reranked 策略同配比，已在 1.8 对比中验证）。
- **双后端协议**：`VectorStore`（JSON）与 `MilvusVectorStore` 各实现 `count()`（JSON 锁内 len；Milvus 走 `get_collection_stats`，兼容旧版 row_count 包列表的形态）与 `snapshot_records()`（JSON 锁内快照；Milvus 按 offset 分页全量拉，与 `doc_owners()` 同模式，不含 embedding 字段）。BM25 懒构建统一走协议：`n_docs != count()` 才重建。
- **生产生成日志不算忠实度**：`log_generation` 的 `faithfulness_avg`/`unsupported_ratio` 改为可空，生产链路传 None（逐句忠实度要多一轮 LLM 调用，属驾驶舱审计职责，不进生产计费路径），仅记 answer_length/命中——用量与命中可观测即可。

## 兼容与迁移

- `QUERY_STRATEGY` 未配置时默认 `hybrid`：未配置 rerank/LLM 的部署自动退化为纯混合（无 rerank）或混合（Noop 即原序截断），**行为单调变好不变坏**；配置 `vector` 可完全回到旧单路行为（回滚开关）。
- `QueryResponse` 新增 `strategy`/`degraded` 字段：JSON 加字段向后兼容，Core 知识网关透传无需改动，桌面端/WPS 忽略新字段零改动。
- `/health` 的 `records` 改走 `count()`，语义不变（Milvus 下从"崩溃"变"可用"）。
- 非法 `QUERY_STRATEGY` 启动即 `ValueError`（fail fast，部署错误要响亮不要静默）。

## 测试计划

- 新增 `tests/test_query_orchestrator.py` 5 用例：默认 hybrid 策略与查询日志落库、vector 策略保持旧行为、multi_hop 无 LLM 降级 hybrid_rerank（degraded 说明含"降级"）、混合检索双路（向量+BM25）同守 doc_ids 过滤口径、非法策略构造即拒。
- 全量回归 `-m "not integration"`；评测集（`tests/retrieval/`）直接调 `Retriever` 不经 `/query`，指标不受本切片影响。

## 发布与回滚

- 纯知识服务（Python）改动，无桌面端/构建产物变化。
- 回滚 = 还原 knowledge/ 代码并重启服务；或仅设 `QUERY_STRATEGY=vector` 即恢复旧检索路径（无需回代码）。

## 完成记录

**已完成（2026-09-01，当日完成）**：

- `app/config.py`：`query_strategy` 配置（默认 hybrid）
- `app/rag/orchestrator.py`（新）：`QueryOrchestrator` + `RetrievalOutcome`（contexts/strategy/requested/degraded/hops）
- `app/api/routes.py`：`/query` 切换编排层；生成后记生产 generation 日志（失败仅告警不影响问答）
- `app/api/schemas.py`：`QueryResponse.strategy` / `QueryResponse.degraded`
- `app/kb/store.py` / `app/kb/milvus_store.py`：`count()` + `snapshot_records()` 双后端协议
- `app/rag/retriever.py` / `app/debug/routes.py`×2 / `app/api/routes.py`(/health)：BM25 懒构建与健康度聚合改走协议（修 Milvus 隐藏 bug）
- `app/kb/query_log.py`：`log_generation` 忠实度参数可空
- `app/services.py` / `tests/helpers.py`：装配 orchestrator（helpers 同时修了测试装配里 retriever 与容器 bm25 实例分裂的问题，与生产对齐为同一实例）

**测试结果**：新增 5 用例全绿；全量回归 133 passed / 1 skipped / 1 deselected（2026-09-01）。

**遗留与边界**：

- 真实 embedding + rerank + LLM 全链路下的 hybrid/hybrid_rerank/multi_hop 策略增益对比，归入公司部署观察期（驾驶舱策略对比 + 生产 strategy 字段分布即可归因，不再单独立项）。
- Milvus `snapshot_records()` 的 offset 分页在超大库（>10 万 chunk）下的分页成本未压测——发芽期万级文档以内无虞，量级上来后可换 query_iterator 流式。
- 生成异常（如 LLM 429）整体 500 的既有行为未动（见 2b/2c 迭代已知问题，候选后续切片：生成失败降级纯检索）。
