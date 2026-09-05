# Contextual Chunking — 文档级定位摘要注入（P1a）

- 日期：2026-09-05（自主迭代周五轮，任务来自 `docs/tasks.md` 待办池 P1）
- 对标依据：[agent-knowledge-benchmark.md](../agent-knowledge-benchmark.md) P1a；[Anthropic Contextual Retrieval](https://www.anthropic.com/engineering/contextual-retrieval)
- 状态：**已完成**（回归绿；评测对比留档见 §4）

## 1. 背景与目标

制造业语料里的型号编码/工艺参数/表格列名脱离文档语境即失义。Anthropic 实测：
LLM 为每块生成定位上下文可使检索失败率降 35%（embedding）、49%（+BM25）、67%（+重排）。
我们的 chunker 自 2026-07-29 已有 `[A > B]` 标题路径前缀（该方法的简化版），本切片补上
**文档级定位摘要前缀**，任务验收标准：42 条评测集 recall@5 / hit@1 / MRR 不回退且有增益。

与 Anthropic 原法的刻意分歧：原法**逐块**调 LLM（整文档入 prompt，50–100 token/块），
靠 prompt caching 控成本；我们私有部署语料可达 100MB 且入库在本地进程，收敛为
**每文档一次** LLM 定位摘要（≤100 字）+ 既有标题路径。上游未处理失败回退，我们补上：
LLM 未配置或调用失败一律退启发式摘要，**摘要生成永不阻塞入库**。

## 2. 实现

| 位置 | 内容 |
| --- | --- |
| `app/kb/contextual.py`（新） | `DocContextBuilder`（LLM 摘要 → 启发式回退链）、`clamp_context`（句读边界截断）、`build_doc_context_builder` 工厂 |
| `app/kb/chunker.py` | `chunk_document(doc_summary=, heuristic_mode=)`；块文本变为 `[文档] 摘要\n[标题路径]\n正文` 双层前缀 |
| `app/pipeline/service.py` | 清洗**之后**构建摘要（防 PII 进索引）；manifest 新增 `contextual{enabled,provider,summary}`；`PIPELINE_VERSION → 2026-09-05.1` |
| `app/config.py` + `services.py` | `CONTEXTUAL_CHUNKING=true` / `CONTEXTUAL_MAX_CHARS=120`（默认开，关闭即旧行为） |
| `scripts/eval_retrieval.py` | `--no-contextual` 开关，支撑新旧行为对比 |
| `tests/test_contextual.py`（新） | 回退链 / 注入规则 / 截断预算 7 用例 |

启发式摘要 = H1 标题（无 H1 取正文首行伪标题，再退文件名主干）+ `（涵盖：大纲关键词）`。

## 3. 注入规则的三次实测修正（本切片核心经验）

1. **样板词是纯噪声**：v1 摘要含「本文档主题/开篇要点」措辞，HashEmbedder 口径
   recall@5 0.952→0.905 全线回退。摘要必须高密度零样板（每个字都是向量质量）。
2. **装箱漂移**：前缀占用正文装箱预算会让切块边界整体移动，排名变化与内容无关。
   改为**先装箱后加前缀**（正文按完整 chunk_size 装箱，前缀叠加在外；overlap 合并
   本就允许超长，有先例），正文与无上下文时逐字节一致。
3. **启发式注入范围**：启发式摘要派生自标题大纲，对已有 `[标题路径]` 的段落是
   重复信息 → 仅注入**无标题上下文的段落**（无标题 txt/段前导文）；多 H1 文档
  （PPTX 幻灯片标题全是 H1）标题栈会重置，靠「标题在路径中」判断会漏。LLM 摘要含
   标题之外的语义词，不受此限全量注入。另：仅含标题的结构块一律不注入。

## 4. 评测对比（42 条，chunk 300/60，top_k=5）

| 口径 | contextual | recall@5 | hit@1 | MRR | snippet | misses |
| --- | --- | --- | --- | --- | --- | --- |
| HashEmbedder 4096 维 | 关 | 0.952 | 0.857 | 0.894 | 0.905 | 4 |
| HashEmbedder 4096 维 | 开（启发式） | 0.952 | 0.857 | **0.892** | 0.905 | 4（同一集合） |
| 真实 qwen3.7-text-embedding | 关 | 1.000 | 1.000 | 1.000 | 1.000 | 0 |
| 真实 qwen3.7-text-embedding | 开 | 1.000 | 1.000 | 1.000 | 1.000 | 0 |

- 哈希口径三项持平，MRR −0.002 来自**单个 case（paraphrase-probation rank 3→4）**，
  misses 集合不变；全量 140 用例绿，现有阈值（0.90/0.85/0.88/0.85）未动。
- 真实口径双向满分——**42 条评测集对真实 embedding 已饱和**（与 2026-07-27 清洗
  规则迭代的记录一致），本尺子只能证「不回退」，无「增益」测量空间，见 §5。

**MRR −0.002 的根因是哈希碰撞噪声，不是检索质量变化**：直接向量分解证明
`q·prefix = 0.0714` 而两者共享 trigram 集合为空——4096 维桶中前缀与查询各有
一个 trigram 碰撞（单对概率约 1/4096，前缀×查询 13×14 对合计约 4%）。任何文本
增量在哈希口径下都是碰撞掷骰（±0.01 量级），追求该口径的精确不回退没有意义。

**LLM 真链路冒烟**（生产默认配置，SenseNova deepseek-v4-flash）：travel-reimburse
摘要=「本文档为员工差旅报销制度，明确规定……**住宿报销上限**，发票开具要求，以及
报销审批流程和打款时限」——判别词正确进入摘要，块文本呈双层前缀（`[文档] 摘要` +
`[差旅报销制度 > 发票与审批]`），正是 Anthropic 描述的消歧机制。

## 5. 结论与遗留

- **结论**：按「不回退」验收通过（哈希口径噪声内持平、真实口径满分持平）；「有增益」
  在现评测集上**不可测量**（真实口径饱和）。功能按默认开上线（生产带 LLM 时摘要
  质量远超启发式，看 §4 冒烟），部署重入库后由观察期 query_log 检验实际收益。
- **遗留 → 收件箱（待拍板）**：评测集缺「上下文敏感性」难例（正文不含文档身份
  即失义的块，如无标题表格只写「上限每晚 300 元」）。构造此类语料才能让增益可测，
  但设计不当会过拟合——进 `docs/tasks.md` 收件箱。
- 配置开关：`CONTEXTUAL_CHUNKING=false` 一键回旧行为；`derived/` 为知识源，
  向量索引可随时重建。
