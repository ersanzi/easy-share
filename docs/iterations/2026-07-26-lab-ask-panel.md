# EasyShare 里程碑 1.5：/lab 知识问答页（检索 + 生成 + 引用溯源）

日期：2026-07-26 · 主题：让知识平台价值闭环从「产物可看」走到「知识可问」

## 用户问题

- 文档处理闭环已落地，但问答价值要等到里程碑 3（WPS 插件）才能被真实感知，链条太长。
- `/query` 接口一直存在却没有任何界面可用，开发者无法日常用真实文档验证检索与生成质量。

## 目标

- `/lab` 新增「05 / ASK 知识问答」面板：输入问题 → 展示回答 + 引用片段。
- 每条引用可溯源：显示来源文件名、score，并可一键打开对应 `clean.md` 派生产物。
- 未配置 LLM 时优雅降级为纯检索模式，面板明示当前服务能力（生成/纯检索、索引记录数）。
- `/query` 响应的 contexts 补充 `file_id`/`version_id`（增量、向后兼容），为引用溯源和未来 Java 契约提供稳定文档身份。

## 非目标

- 不做多轮对话、流式输出、会话历史——最简问答，验证价值闭环。
- 不改变 `/lab` 产品边界：仍仅回环访问、无认证、不进 Wails 客户端。
- 不动 `/query` 的权限模型（`doc_ids` 继续预留给 Java 控制面）。

## 设计决策

- **复用 `/query` 而非新建 lab 专用接口**：lab 页面已直接调用 `/documents/...` 产物接口，问答同样直调主 API；避免两套查询语义。服务本身仅监听 127.0.0.1，未扩大暴露面。
- **`RetrievedChunk` 增量加字段**：向量记录本就携带 `file_id`/`version_id`（管线写入），只是 API 没透出。加字段而非改结构，旧调用方不受影响；`/ingest` 兼容路径产生的记录无这两个字段，返回 null，前端相应隐藏溯源链接。
- **引用溯源直链 `clean.md`**：点击引用在新标签打开 `/documents/{file_id}/versions/{version_id}/artifacts/clean.md`，与产物检查器同一数据源，不复制状态。
- **能力自探测**：面板加载时调 `/health` 显示「生成模式/纯检索模式 + 索引记录数」，避免用户在未配置 LLM 时误判系统坏了。

## 兼容与迁移

- 纯增量：schema 加可选字段、路由透传、lab 静态资源扩展；无配置变化、无数据迁移。
- 旧向量记录（无 `file_id`）继续可检索，仅无溯源链接。

## 测试计划

- `test_api.py::test_query_citations_are_traceable_to_artifacts`：处理真实管线文档后，`/query` 降级回答含提示文案、contexts 携带 `file_id/version_id/filename` 且能据此取回 `clean.md`；注入桩 Generator 后走生成路径，sources/contexts 一致。
- `test_lab.py::test_lab_page_contains_ask_panel_with_citation_support`：页面含问答面板与引用列表，脚本含 `/query` 调用与 `clean.md` 溯源链接。
- 现有产品边界测试（仅回环、可关闭）继续覆盖新面板（同一 guard）。

## 发布与回滚

- 不进入安装包；回滚即还原本次改动。
- 服务端无状态变化，重启即生效。

## 完成记录

- 已完成：`/query` 溯源字段、问答面板（HTML/JS/CSS）、能力自探测、降级模式、引用直链 clean.md。
- 测试：`pytest -m "not integration"` 40 passed（2026-07-26）。
- 真机冒烟（2026-07-26，本机 `.env` 已配置百炼 embedding + SenseNova LLM）：`/health` 报 OpenAIEmbedder + llm configured；真实提问返回 LLM 生成回答（含 `[1]` 引用标注），contexts 携带完整 `file_id/version_id/filename`，`/lab` 页面渲染问答面板。
- 已知问题 / 后续工作：
  - 回答未做流式输出，长回答等待感明显；后续按需加 SSE。
  - 生成质量评测（答案忠实度/引用准确率）尚无标注集，可在检索评测集基础上扩展。
