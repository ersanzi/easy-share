# Agent 知识库六年演进对标（2026-09-01）

> 背景一篇行业综述把 Agent 知识库 2020–2026 的技术路线摊在时间轴上：Vector RAG → GraphRAG → LightRAG → 树形索引（PageIndex/RAPTOR）→ LLM Wiki（Karpathy）→ OKF（Google Cloud），主张下半场收敛到"frontmatter + 正文 + Git"（渐进式披露、知识软件化、索引只是编译产物）。
> 本文做两件事：用外部一手资料交叉验证各代主张，再逐代给出**对本项目的判定**（借什么/不借什么/何时重评估）。
> 定位同 [`cloudreve-benchmark.md`](cloudreve-benchmark.md) 与 TDB-AM 对标：结论进路线，证据与推理留档。

## 0. 前提：我们与综述的目标场景不同

综述写的是"给 Agent 装的知识库"；我们是**中小制造业私有知识服务器**——用户不懂技术、算力/API 预算有限、语料天然结构化（工艺文件/设备手册/质检规范）、当前处于发芽观察期（公司部署看真实使用率）。所有判定都从这个前提出发：**成本敏感、用户能力有限、结构化语料红利**。

## 1. 六代速览与本项目判定

| 代际 | 解决什么 | 外部印证 | 本项目判定 |
| --- | --- | --- | --- |
| Vector RAG + Contextual Retrieval | 外部知识接入 | [Anthropic 实测](https://www.anthropic.com/engineering/contextual-retrieval)：LLM 为每 chunk 生成 50–100 token 文档级定位前缀，检索失败率降 30–49%（叠加重排 67%） | **基座保留，补 contextual 前缀（P1）**。我们 chunker 的 `[A > B]` 标题前缀是其简化版；制造业语料里型号编码/工艺参数脱离文档即失义，此法对我们收益高于大厂 |
| GraphRAG | 关系与全局主题 | 全量社区摘要构建成本高（综述与微软 LazyGraphRAG 均承认）；[PageIndex/JTR/RAPTOR](https://github.com/parthsarthi03/raptor) 证明"结构导航"可替代部分图需求 | **不现在上**。私有部署中小厂付不起构建成本；多跳需求未验证。重评估条件：query_log 盲区出现真实跨文档关系查询失败 |
| LightRAG / 轻量图 | 图谱成本与增量 | 双层检索 + 局部子图增量并入 | **将来若上图走此路**：预算式、查询时按需展开（与 TDB-AM 对标结论一致，我们 multi_hop 的三重预算已是同哲学） |
| 树形索引 | 结构丢失与推理导航 | PageIndex（商业）"在文档自己的逻辑结构里走路"；RAPTOR 递归摘要树 | **半借**：不做 RAPTOR 式递归摘要树（LLM 成本），做"文档级粗筛 → 块级精筛"两级检索（P1）。我们的 `document.json` 块树（精确到 page/table/row）是现成原料——制造业语料自带目录，无需让模型重新发明树 |
| LLM Wiki | 理解无法积累 | [Karpathy llm-wiki](https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f)：目录即知识库，agent 持续编纂；风险是模型误解固化 | **发芽观察后做最小闭环（P2）**：query_log 的高频问答自动沉淀 FAQ 页（人工确认后发布）。用户是普通消费者，Wiki 必须"系统自动维护、用户可读可改"，绝不能要求维护。与 progress.md 待开始第 3 条"问过的不再问第二遍"同一件事 |
| OKF | 知识无法跨工具流通 | [Google Cloud OKF v0.1](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md)：刻意极简——Markdown 目录 + YAML frontmatter，无 SDK 无运行时无中心权威 | **借导出格式，不借运行时（P1）**：`derived/` 产物 clean.md 加 frontmatter（owner/来源/版本/摘要/别名），知识不锁死在 Milvus 与我们的 schema，向量索引降格为"可重建的编译缓存" |
| frontmatter+正文+Git（综述主张的下半场） | 控制面/数据面分离、知识版本化 | Anthropic Agent Skills 的[三级渐进披露](https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills)（metadata → 正文 → 资源）验证了同一分层 | **借原则，狠狠简化形态（P2）**：中小厂用户不可能玩分支/PR。借的是结果不是流程——版本时间线 + diff + 一键回滚的普通人版本；**"静态知识归 Git、动态事实归运行时"边界直接采纳**（设备参数/库存/订单状态绝不烤进知识文件，MES/ERP 数据回答时现查） |

## 2. 三条采纳的架构原则（超越具体代际）

1. **知识源与编译产物分离**：`derived/{file_id}/{version_id}/` 下的 clean.md + document.json + manifest 就是知识源资产，向量库/BM25/图/树索引全部是从它派生的**可重建缓存**——索引坏了从源重建，模型换了重新 embedding，客户数据主权完整。本项目已有此分层的事实，缺的是把它当**资产承诺**（OKF 化导出即此承诺的落地）。
2. **控制面与数据面分离**：frontmatter（名称/别名/摘要/依赖/owner）是知识的控制面，正文是数据面；渐进式披露 = 先读控制面路由、命中才展开正文。对私有部署这不止省 token，是**控制上下文熵**——无关干扰越少，小模型越能用。
3. **哪个都不押，哪个都用**：查询按内容特征选后端——散点事实走向量+BM25、跨文档关系走多跳、结构长文档走树导航、重复问题走向导缓存。1.9 已把零件造齐，2026-09-01 [生产检索回灌](iterations/2026-09-01-production-retrieval-upgrade.md) 把零件接上产线（`QUERY_STRATEGY` 部署级配置，默认 hybrid，multi_hop 需 LLM 自动降级）。

## 3. 分级行动清单

| 优先级 | 事项 | 成本 | 状态 |
| --- | --- | --- | --- |
| P0 | `/query` 回灌混合+重排+多跳开关；生产查询日志贯通；修 Milvus records 隐藏 bug | 低（零件接线） | ✅ 2026-09-01 完成 |
| P1a | Contextual chunking：入库时 LLM 生成文档级摘要前缀注入每块 | 中低 | 待排期 |
| P1b | `derived/clean.md` OKF 化 frontmatter + 文档级摘要产物 | 低 | 待排期 |
| P1c | 文档级粗筛路由（frontmatter 摘要清单 → 块级精筛两级检索） | 中 | 待排期 |
| P2a | query_log 驱动的高频问答沉淀 FAQ（LLM Wiki 最小闭环） | 中 | 发芽观察后 |
| P2b | 图/多跳检索升级（视盲区真实失败案例） | 中高 | 条件触发 |
| P2c | 文档版本时间线 + diff + 一键回滚（Git 思想的普通人形态） | 中 | 发芽观察后 |
| 不做 | GraphRAG 全量社区摘要、RAPTOR 递归摘要树、Git PR 评审流、知识包生态 | — | 成本/用户能力/场景任一不成立 |

**重评估触发器**（写死，防伪需求也防漏需求）：驾驶舱健康度的盲区查询里出现可归因的多跳/关系类失败 → 启动 P2b；两周观察期使用率达标且高频问题集中 → 启动 P2a；出现"答案引用了过期版本"的真实事故 → 启动 P2c。

## 4. 与既有文档的关系

- 本文的 P0 即 [`iterations/2026-09-01-production-retrieval-upgrade.md`](iterations/2026-09-01-production-retrieval-upgrade.md)；里程碑视角见 [`knowledge-platform.md`](knowledge-platform.md) §5（1.9 检索加固）。
- TDB-AM 对标（预算控制/两步自发现/BM25 降级/hop 级日志）与本文 LightRAG 判定同源：预算式按需展开优于构建时全量建模。
- 综述原文的关键论断（相似≠相关、chunking 撕碎结构、知识现炒 vs 复利、索引应可重建）与本项目 1.8/1.9 的实践互相印证，无需另立证据。
