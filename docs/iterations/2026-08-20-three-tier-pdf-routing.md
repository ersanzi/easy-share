# 三层解析路由实现：pdf-inspector 路由 + MinerU 深度解析 + PaddleOCR 兜底

> 设计依据：[`2026-08-20-mineru-parsing-provider-design.md`](2026-08-20-mineru-parsing-provider-design.md)（含 §4.8 pdf-inspector 增补）。
> 开工：2026-08-20。

## 用户问题

业务流跑通优先（2026-08-20 决策）：复杂 PDF（扫描/多栏/公式/表格结构）是现有自研 fitz 解析的短板，MinerU VLM 补深度解析，pdf-inspector 补快路由（文本型 PDF 不该浪费贵解析器），任何失败回退现有管线，永不丢文档。

## 目标

- PDF 到达后三层路由：pdf-inspector 分类（~20ms）→ 文本型本地提取 / 难文档 MinerU / 混合型页级路由 / 全部失败回退现有 `parse_document`。
- MinerU Provider 产出与现有管线完全相同的 `ParsedDocument`，下游零改动。
- manifest 增 `parsing` 字段（provider/backend/耗时/回退原因），驾驶舱可见。
- 默认全部关闭：`mineru_enabled=False`、`pdf_inspector_enabled=False`，行为与现状一致。

## 非目标

- 不动 office/文本/表格解析路径；不做 A/B 评测与压测（统一后置）。
- 不编排 MinerU 部署（docker-compose 生产阶段再做）。
- 不启用 pdf-inspector 自带 selective OCR（OCR 继续用 PaddleOCR）。

## 设计决策

按设计文档 §4 执行：模块结构照 `ocr/` Provider 模式（`app/parsing/mineru/` + `app/parsing/pdf_router.py`）；分流点在 `DocumentPipeline` 构造注入；映射表见设计 §4.5；降级链见 §4.3/§4.8。

## 测试计划

- 单元：MinerU adapter 映射（FakeProvider 预置 content_list → blocks 断言）；pdf_router 依赖缺失时返回 None；pipeline 路由回退路径 + manifest 字段；默认关闭行为与现状一致。
- Spike：pip 安装 pdf-inspector（Windows wheel 验证），黄金语料中文 PDF 分类与提取实测。
- 回归：`pytest -m "not integration"` 全绿。

## 完成记录

### 已完成（2026-08-20）

- **Spike 通过**：pdf-inspector Windows x64 wheel 安装成功；实测空 PDF → `scanned`（置信 0.9、页级路由正确）、手工文本 PDF → `text_based` + 7ms 提取正确 markdown（自动识别大字号标题为 H1）。中文 PDF 实测待真实语料（黄金语料暂无 PDF 样本）。
- **新增模块**：`app/parsing/mineru/`（base 协议与降级实现 / httpx 客户端对接 `/file_parse` / adapter 纯函数映射，表格 HTML 用标准库解析不引依赖）；`app/parsing/pdf_router.py`（pdf-inspector 包装，依赖缺失返回 None 自动退回）。
- **管线接入**：`DocumentPipeline` 注入 `mineru_provider` + `pdf_router`，`_parse_routed` 实现三层路由与逐级回退；manifest 新增 `parsing` 字段（provider/router 分类与置信度/backend/耗时/回退原因），驾驶舱可直接读。
- **配置**：`mineru_enabled/base_url/api_token/backend/timeout_seconds/max_pages` + `pdf_inspector_enabled`，全部默认关闭（留空即现状）。
- **测试**：新增 `tests/test_pdf_routing.py` 14 条（adapter 映射/markdown 兜底/表格解析/默认关闭一致性/三层路由各分支/回退留痕/非 PDF 不触路由）；全量 `pytest -m "not integration"` **84 passed, 1 skipped**。

### 与设计文档的差异（以实测为准）

- MinerU backend 枚举实为 `pipeline / vlm-engine / vlm-http-client / hybrid-*`（设计草稿写的 `vlm-http` 已过时）；默认取 `pipeline`（无 GPU 依赖、多语言、无幻觉）。
- pdf-inspector 定位调整为「分析+本地提取一体」（单次 `process_pdf_bytes` 同时给分类与 markdown），比设计稿的"先 classify 后提取"少一次调用。
- pdf-inspector 作为可选依赖不进 requirements（同 pymupdf 待遇），CI 测试全用 Fake 覆盖。

### 已知限制与后续工作

- **MinerU 真实服务冒烟待做**：本迭代以 Fake 覆盖协议与回退路径；待 mineru-api 部署（WSL2 Docker 或官方 API token）后跑设计 §6 第 4 步冒烟（复杂 PDF 肉眼比对 clean.md 与切块地图）。
- Mixed 混合型暂整文档交 MinerU，页级拆分（文本页本地/扫描页深度解析）留后续迭代。
- 驾驶舱单文档透视展示 `parsing` 字段（解析器来源/路由信息）待前端小改，随「解析即验收」获批想法一起做。
