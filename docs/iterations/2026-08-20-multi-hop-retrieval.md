# Agent 多跳检索：分轮检索 + 预算控制 + hop 级日志

> 里程碑 1.9 混合检索加固首个切片。设计引用 TDB-AM 对标报告 §5.4/§5.5 与 §7-P0（召回预算控制、hop 级日志）。
> 开工：2026-08-20。

## 用户问题

复杂问题（"新报销制度和旧的差多少"）单轮检索只能命中一个侧面，需要分轮检索逐步补全；多跳不加约束会雪崩式膨胀上下文；每跳的检索过程目前不可观察，无法归因质量。

## 目标

- 多跳检索最小闭环：LLM 判断上下文是否充分 → 不充分则生成补充查询 → 再检索，最多 N 跳；结果按 chunk 去重合并。
- 预算控制（TDB-AM P0-1）：max_hops / 总上下文条数 / 总字符预算三上限，超出丢弃低分溢出。
- hop 级日志（P0-4）：每跳的查询、命中数、是否收敛写入 QueryLog，健康度与驾驶舱可归因。
- 驾驶舱 `/debug/query` 新增 multi_hop 策略列，可视化各轮查询与命中。

## 非目标

- MCP Server 暴露（tools 两步自发现）另立迭代。
- 无 Embedding 的 BM25-only 降级模式另立迭代。
- 不改既有四策略（vector/bm25/hybrid/reranked）行为。

## 设计决策

- **每跳用混合检索**（向量 + BM25 + RRF），`hybrid_fusion` 从 debug 路由提升到 `app/kb/fusion.py` 公用，debug 四策略行为不变。
- **充分性裁判**：LLMHopJudge 用 OpenAI 兼容 LLM 输出 `{"sufficient", "next_query"}`；解析失败按"已充分"收敛（防死循环）；裁判输入限字符预算（默认 4000）控成本。
- **预算控制**（P0-1）：`max_hops`（默认 3）/ `max_contexts`（默认 10）/ `max_chars`（默认 12000）三上限，超限丢弃低分溢出；达到跳数上限视作收敛（兜底）。
- **去重合并**：chunk 按唯一 id 跨跳去重，保留最高融合分。
- **hop 级日志**（P0-4）：每跳调 `query_log.log_retrieval`，strategy 形如 `multi_hop:hop1`、question 为该跳实际查询——无需改表，健康度统计自然包含。
- LLM 未配置 → `build_multi_hop_retriever` 返回 None，驾驶舱该列显示"未配置 LLM，多跳不可用"，其余策略不受影响。

## 完成记录

### 已完成（2026-08-20）

- 新增 `app/kb/fusion.py`（RRF 提升公用）与 `app/rag/multi_hop.py`（MultiHopRetriever / LLMHopJudge / apply_context_budget）。
- `debug/routes.py` `/query` 新增 `multi_hop` 策略（含 hops 轮次记录与 total_candidates）；services 装配 `multi_hop`（LLM 配置存在时构建）。
- `cockpit.js` 检索对比升级五列，多跳列渲染轮次条（每跳查询/命中数/收敛态）；`cockpit.css` 新增轮次条样式。
- 配置新增 4 字段：`multi_hop_max_hops/hop_top_k/max_contexts/max_chars`。
- 测试：新增 `tests/test_multi_hop.py` 8 条（两跳收敛/单跳收敛/跳数上限/跨跳去重/预算截断/裁判解析变体/hop 日志/无 LLM 降级）；全量 `pytest -m "not integration"` **97 passed, 1 skipped**。

### 已知限制与后续工作

- 裁判与检索的质量增益需真实 LLM + 真实语料验证（归入统一测试；驾驶舱 D 列已可视化每轮查询，便于人工判断）。
- 多跳结果暂不喂给 `/debug/generate`（生成审计仍用混合策略），验证增益后再接。
- MCP Server 暴露（tools 两步自发现）与无 Embedding 的 BM25-only 降级为 1.9 后续切片。
