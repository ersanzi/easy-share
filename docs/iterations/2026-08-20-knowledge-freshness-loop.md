# 知识时效最小闭环：入库时间贯通检索与生成

> 获批想法 3（2026-08-20 批准）：chunk 入库时间 + 检索结果标注新旧 + 生成时提示时效，消灭新旧知识混答。
> 开工：2026-08-20。

## 用户问题

企业文档有寿命：同一主题常有新旧多份文档（如 2024 版与 2026 版采购制度）。当前检索不携带时间维度，生成可能拿作废旧制度回答用户——这是知识平台最伤信任的失败模式（"知识有寿命"愿景的第一块砖，见 knowledge-platform.md §1.2）。

## 目标

- chunk metadata 增加 `ingested_at`（该版本处理时间），管线写入、检索透出。
- `/query` contexts 携带时间，前端可展示"此内容来自 X 日期的文档"。
- 生成 prompt 注入文档日期并指示：优先依据较新文档；若依据可能被更新文档取代，回答中主动提示时效。

## 非目标

- 不做检索排序的新鲜度加权（等 1.9 数据驱动决定）。
- 不做知识替代/废止关系图谱（远期"知识有寿命"完整形态）。
- 不改向量库 schema 版本迁移逻辑之外的行为。

## 设计决策

- `ingested_at` 是**文档级**信息，注入点在 `DocumentPipeline` 组装 chunk items 处（与 manifest `processed_at` 同一时间戳），不进切块器（切块器只管内容切分）。
- 生成器抽出 `build_reference_block` / `build_messages` 模块级纯函数：带时间的片段标注 `文档时间`，无时间的旧数据不标注（向后兼容，不触发重建索引）。
- SYSTEM_PROMPT 增加时效指示：冲突时优先依据较新文档；可能被取代的内容须提示时效。
- `/query` 的 `RetrievedChunk` 与 `SourceRef` 新增 `ingested_at`（可空，additive，前端可展示"此内容来自 X 日期的文档"）。
- 不改检索排序（新鲜度加权留给 1.9 数据驱动决定）。

## 完成记录

### 已完成（2026-08-20）

- `DocumentPipeline`：`processed_at` 提前计算并写入 chunk metadata `ingested_at`（与 manifest 同源）。
- `rag/generator.py`：重写为可测结构（纯函数 + Generator），prompt 注入文档时间与时效指示；sources 携带 `ingested_at`。
- `api/schemas.py` + `api/routes.py`：`RetrievedChunk`/`SourceRef` 透出 `ingested_at`。
- 同步 `architecture.md` §11（含补上次三层路由的漏记）。
- 测试：新增 `tests/test_freshness.py` 5 条（prompt 指示/参考块标注/管线落戳/API 透出）；全量 `pytest -m "not integration"` **89 passed, 1 skipped**。

### 已知限制与后续工作

- 旧索引数据无 `ingested_at`，标注自动缺省，无需迁移；重新处理文档后即有。
- /lab 前端展示入库时间与时效提示 UI 随「解析即验收」迭代一起做。
- 检索层新鲜度加权（如时间衰减 boost）与"知识替代关系"图谱为远期，1.9 用数据决定。
