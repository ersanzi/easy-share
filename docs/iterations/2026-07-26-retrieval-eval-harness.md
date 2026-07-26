# EasyShare 检索质量评测集与知识面 CI

日期：2026-07-26 · 主题：为 RAG 链路建立可回归的质量基线

## 用户问题

- 知识管线即将进入切块策略调整、真实 embedding 对比、Milvus 迁移和 reranker 评估阶段，但没有任何客观指标衡量"改动之后检索是变好还是变坏"，只能靠人工抽查手感。
- `knowledge/` 的 Python 测试（含解析黄金语料）没有进入任何 CI；Windows/macOS workflow 只覆盖 Go 与前端，Python 回归只能靠本地自觉。

## 目标

- 建立标注评测集：30 条「问题 → 应命中文档（含期望片段与可选权限范围）」，语料混合首批 Office/PDF 黄金样本与 6 篇主题互异的企业文档。
- 提供可复用评测器，输出 recall@k、hit@1、MRR、片段命中率与未命中明细。
- pytest 基线测试进入常规回归：语料经真实 `DocumentPipeline` 索引（FakeStorage + HashEmbedder，全程确定性、离线、无 API 费用），指标低于阈值即失败。
- 提供 `scripts/eval_retrieval.py`：同一评测集可切换 `.env` 配置的真实 embedding（`--real`）或调整切块参数，输出对比报告。
- 新增 `knowledge-tests.yml` CI：ubuntu runner 上跑全部非 integration Python 测试，`dev`/`master` push 与 PR 触发（按 `knowledge/**` 路径过滤）。

## 非目标

- 不评测生成（LLM 回答）质量，只评测检索；生成评测在接入真实问答场景后另立迭代。
- 不在 CI 中调用真实 embedding/LLM API（费用与凭据问题）；语义质量对比通过脚本人工触发。
- 不引入评测框架（ragas 等），30 条规模自写百行内评测器足够且零依赖。
- 不改动生产切块默认值（800/120）；评测固定 300/60 是为了让短篇语料产生多块、检验块级排序。

## 设计决策

- **评测器位置**：`app/eval/retrieval.py`。它是链路的长期"尺子"，测试与脚本共用，不属于某个测试目录。
- **语料两类来源**：黄金 Office 样本复用 `tests/golden/builders.py`（不新造二进制），文本语料以 `.md/.txt` 明文放在 `tests/retrieval/corpus/`，人工可审查、可扩充。
- **走真实管线索引**：语料通过 `DocumentPipeline.process` 入库而非直接写向量库，因此解析、清洗、切块、索引替换的任何回归都会反映到检索指标上。
- **HashEmbedder 作 CI 基线口径**：字符 trigram 哈希是确定性的词面匹配基线，指标稳定可设阈值；它衡量的是"链路是否回归"，不代表语义检索质量。语义质量用 `--real` 跑真实 embedding 对比。
- **权限范围进入评测**：2 条用例带 `doc_ids` 过滤 + 1 条专项测试断言范围外文档绝不返回，为里程碑 2 的权限感知检索（Java 传入授权范围）提前守住行为。
- **放弃的替代方案**：直接对向量库手工插桩（绕过管线，测不到解析/切块回归）；用生产默认 800/120 切块（短语料整篇一块，块级排序退化为平凡命中）。

## 兼容与迁移

- 纯新增，不改任何生产代码路径与配置字段；`config.json`、API、端口均不受影响。
- `pytest -m "not integration"` 与现有 pytest.ini 标记约定一致，RustFS 集成测试维持显式开启。
- 后续迁移 Milvus / 更换 embedding / 调整切块时：先跑 `python scripts/eval_retrieval.py` 留存新旧报告，再更新基线阈值并在迭代记录中说明。

## 测试计划

- `tests/retrieval/test_retrieval_eval.py`：
  - 评测集与语料引用一致性（≥30 条、file_id 均存在）；
  - HashEmbedder 基线指标不低于校准阈值（recall@5 / hit@1 / MRR / 片段命中率），失败时输出未命中用例明细；
  - `doc_ids` 范围过滤永不返回范围外文档。
- 全量 `python -m pytest -m "not integration"` 保持通过。
- CI：knowledge-tests workflow 在 GitHub Actions ubuntu runner 上首跑通过。

## 发布与回滚

- 无产物变化（不进入安装包）；回滚即删除新增文件与 workflow。
- CI 失败信号：Actions「Knowledge Tests」红灯即表示 Python 管线或检索指标回归。

## 完成记录

- 已完成：评测器、语料（10 篇文档）、30 条标注、pytest 基线、评测脚本、knowledge-tests CI workflow。
- 基线指标（HashEmbedder 4096 维，top_k=5，chunk 300/60，2026-07-26 实测）：
  - recall@5 = 1.000，hit@1 = 0.867，mrr = 0.912，片段命中率 = 1.000
  - 测试阈值按此校准并留余量：recall@5 ≥ 0.95、hit@1 ≥ 0.80、mrr ≥ 0.85、snippet ≥ 0.95
- 校准过程发现：哈希维度 256 时向量过密（300 字符块 ≈ 300 个 trigram 全部落桶），recall@5 仅 0.767；升到 4096 维后词面区分度恢复。该结论只影响评测基线口径，不涉及生产 embedding。
- 测试结果：`pytest tests/retrieval -q` 3 passed；全量 `pytest -m "not integration"` 38 passed, 1 deselected（2026-07-26，Windows 本地 venv）。
- 已知问题 / 后续工作：
  - 评测集应随语料库扩充持续增长（目标 50+），尤其在接入 OCR 扫描件后补充图片 PDF 用例；
  - 真实 embedding 的语义基线报告待配置 `.env` 后首次留存；
  - GitHub Actions「Knowledge Tests」首跑验证待下次推送后确认。
