# 迭代记录 — OKF 化切片一：derived/ 重建验证（2026-10-02，自动化）

> 触发：每日 14:00 自主迭代调度轮 A 分支（周五）。安全门通过（工作树仅自动化文档）；执行待办池 P2「OKF 化切片一：derived/ 为唯一知识源的重建验证」（大任务只做此片），并完成挂账两周的待办池去重（「汇总页进阶可视化」重复行）。
> 依据：[agent-knowledge-benchmark.md](../agent-knowledge-benchmark.md) 架构原则 1「知识源与编译产物分离」——`derived/` 下的 clean.md + document.json + manifest 是知识源资产，向量索引全部是**可重建缓存**；本切片把这一承诺从「事实」升级为「可验证的资产承诺」。

## 目标与口径选择

**任务**：写全量重建向量索引脚本，断言评测集指标不回退（tasks.md 完成标准：脚本 + 重建前后指标留档）。

实现前探查发现评测体系的关键事实：42 条评测集与黄金语料（`tests/retrieval/corpus.py`）构建的是**独立于生产索引的临时 setup**（`build_eval_setup` → 临时 VectorStore）。因此「对现网 vector_store.json 重建再评测」口径不成立（评测指标根本不依赖现网索引）。定稿口径：

**黄金语料双路对比**——同一语料走两条路建索引，用同一评测集打分对比：
- **基线路线**：完整入库管线（解析→清洗→contextual 摘要→切块→embedding→索引），即 `index_corpus(setup)`；
- **重建路线**：只读 derived 产物（document.json + manifest.json），不碰源文档、不重跑解析与清洗、**不调用 LLM**，重建出完整向量索引。

重建路线成立即证明：向量索引可以从知识源完整重建——这正是 OKF「索引只是编译产物」的核心主张。生产库替换（真部署场景的 `--apply`）不在本切片，留待公司部署后按需补充。

## 实现：`knowledge/scripts/rebuild_from_derived.py`

- **反序列化辅助** `document_from_payload`：`ParsedDocument.to_dict` 会弹出取默认值的字段（level/rows/text/metadata 与 source 内 None），辅助逐字段回填，保证与解析器直出对象等价。
- **构建参数的真相源是 manifest**：contextual 摘要不重新生成，直接取 `manifest.contextual` 留档的 `summary`/`provider`（`heuristic_mode = provider == "heuristic"`）——这是「derived 产物自带构建参数记录」设计（2026-09-05 contextual chunking 切片埋的伏笔）的第一次兑现。
- **items 结构复刻管线**（`pipeline/service.py` 的 items 字段一一对应），metadata（filename/object_key/owner/visible_depts/pipeline_version/ingested_at）从 manifest 恢复，块级 metadata 走同一 `chunk.metadata()`。
- **断言**：四指标（recall@k / hit@1 / mrr / snippet_hit_rate）重建后与基线**逐项相等**（HashEmbedder 确定性 + 同参数切块，应逐位一致而非容差内）；任一下降退出码 1。空语料兜底退出码 2。
- `--output` 支持把对比报告写 JSON 留档。

## 验证

```
语料文档: 10  重建文档: 10  重建块数: 29
基线   recall@5: 0.952  hit@1: 0.857  mrr: 0.892  snippet: 0.905
重建后 recall@5: 0.952  hit@1: 0.857  mrr: 0.892  snippet: 0.905
重建验证通过：四指标与管线直建基线逐项持平
```

- 重建验证**一次通过**（10 文档 29 块，42 条评测集口径不变）。
- pytest 轻量回归：**147 passed, 1 skipped**（与基线一致，无回归）。按冲刺期轻测试约定未新增单测——本切片的验证即脚本自身的重建断言。

## 边界与后续（不做清单）

- **不做** OKF frontmatter 导出（对标文档 P1b：clean.md 加 frontmatter，另一独立切片）；
- **不做** 文档级粗筛路由（P1c）；
- **不做** 生产库 `--apply` 替换与真实 embedding 口径的重建对比（前者待公司部署后有真实数据再做，后者可在脚本上加 `--real` 沿用 eval_retrieval.py 模式，属增强非本片）；
- Milvus 后端的重建（`MilvusVectorStore` 同接口）未覆盖——当前观察期默认 JSON+numpy，Milvus 默认关，切 Milvus 时再验证（其 `replace_doc` 协议已由生产检索回灌切片对齐）。
