# MinerU 解析集成设计：PDF 专项可选 Provider

> 日期：2026-08-20
> 状态：设计定稿，实现待排期
> 背景：用户指示——不做 A/B 评测、压测统一后置，优先业务流跑通；MinerU 直接嵌入。
> 许可证事实（已核实）：Apache-2.0 基础 + 附加条款；月活超 1 亿或月收入超 2000 万美元需商业授权（远未触及）；基于它提供在线服务须显著标注"使用了 MinerU"（**有自动终止条款，署名不可漏**）。

## 1. 用户问题

业务流跑通是当前主线。现有管线 PDF 路径为自研（`_parse_pdf`：PyMuPDF 提取 + PaddleOCR 扫描页分流），复杂版式（多栏、公式、合并单元格表格、阅读顺序）是短板；MinerU（2.5 VLM 后端为开源 SOTA 梯队）恰好补这一环，且自带自建 API 与官方 API 两种形态。

## 2. 目标

- 启用后 PDF 走 MinerU，产出与现有管线**完全相同**的 `ParsedDocument`——下游清洗/渲染/切块/索引零改动。
- 自建 mineru-api 与官方 mineru.net 同一客户端，仅 endpoint/token 不同。
- 任何失败自动回退本地管线，**永不因 MinerU 不可用而丢文档**。
- 解析器来源进 manifest，驾驶舱单文档透视可见。

## 3. 非目标

- 不动 office/文本/表格解析路径（docx/xlsx/pptx 走现有 `_parse_*`）。
- 不做 A/B 评测与压测（统一后置）。
- 不编排本地 GPU 部署（生产 docker-compose 阶段再做）。
- 不改切块、清洗、检索逻辑。

## 4. 设计决策

### 4.1 集成面：统一块模型的输入侧

`DocumentPipeline._process_locked` 只消费 `parse_document()` 的输出 `ParsedDocument`，下游全部只认 `DocumentBlock`。因此 MinerU 的集成面是**一个函数的输出**：新增一条"产出同款 blocks 的解析路径"即可，现有代码零侵入。分流点放在 `DocumentPipeline`（构造注入 provider），不塞进 `extractor.py`——extractor 保持纯本地、无网络依赖。

### 4.2 模块结构（照 `app/ocr/` Provider 模式）

```text
app/parsing/mineru/
  __init__.py     # build_mineru_provider(settings) → Provider | UnavailableMinerUProvider
  base.py         # MinerUProvider Protocol、MinerUCapability、UnavailableMinerUProvider（默认关闭时的明确失败实现）
  client.py       # MinerUClient：HTTP 调用 mineru-api（/file_parse 或官方等价接口），token 可选
  adapter.py      # MinerU content_list → ParsedDocument 映射（纯函数，可单测）
```

`DocumentPipeline.__init__` 增加 `mineru_provider: MinerUProvider | None = None` 参数，与 `ocr_provider` 并列。

### 4.3 分流与回退（降级链）

```text
文件为 .pdf 且 mineru_enabled 且 provider.available
  → MinerU 解析成功                     → ParsedDocument + metadata.mineru
  → 不可达 / 超时 / 结果畸形 / 超页数上限  → 回退现有 _parse_pdf 全流程
                                            warnings 追加回退原因，manifest 记录 fallback_reason
非 PDF 或未启用                           → 现有 parse_document 原样
```

默认 `mineru_enabled=False`，留空即现状——与 Milvus「留空退回 JSON」、NoopReranker 同一哲学。

### 4.4 配置（Settings 追加，env 驱动）

| 字段 | 默认 | 说明 |
| --- | --- | --- |
| `mineru_enabled` | `False` | 总开关 |
| `mineru_base_url` | `http://127.0.0.1:8779` | 自建 mineru-api；官方 API 时换 mineru.net 域名 |
| `mineru_api_token` | `None` | 官方 mineru.net 时必填 |
| `mineru_backend` | `vlm-http` | `pipeline` / `vlm-http`（VLM 精度优先，需 vllm server） |
| `mineru_timeout_seconds` | `300` | 单文档超时 |
| `mineru_max_pages` | `300` | 超页数直接走本地管线（规避超长 PDF 内存问题） |

### 4.5 content_list → DocumentBlock 映射表

| MinerU type | DocumentBlock | 说明 |
| --- | --- | --- |
| `title`（含 level） | `type="heading"`, `level=n` | 直接对接结构感知切块的层级上下文 |
| `text` | `type="paragraph"` | |
| `table`（HTML body） | `type="table"`, `rows`=解析后的二维表 + `metadata.table_html` 原文 | 复用表格完整性切块逻辑 |
| `equation`（LaTeX） | `type="paragraph"`, `metadata={"latex": ...}` | 文本放 LaTeX 源码，保留可检索与溯源 |
| `image`/figure | 有 caption → `type="paragraph"`（caption 为文本）；无 caption 暂跳过 | 图片深度 OCR 后续迭代再议 |
| `page_idx` | `source.page` | 与 OCR 页级溯源对齐 |
| `bbox` | `metadata.bbox` | 与 OCR bbox 惯例一致 |

### 4.6 manifest 与驾驶舱

`manifest["parsing"] = {"provider": "mineru" | "local", "backend", "duration_ms", "fallback_reason"}`。单文档透视 Tab 直接展示解析器来源，业务流验收时可肉眼比对两种解析器的产出差异。

### 4.7 单元测试（非压测；项目规矩的新行为必须有自动化测试）

- `FakeMinerUProvider`（预置 content_list）→ adapter 映射断言：heading 层级、表格 rows、页码、bbox、LaTeX。
- 回退路径：provider 抛错/超时 → 流程正常完成且 `manifest.parsing.fallback_reason` 存在。
- 默认关闭：行为与现状逐字节一致。
- 真实 MinerU 端到端验证归入后续统一测试（用户决策）。

### 4.8 增补（同日）：pdf-inspector 作为路由层

评估 [firecrawl/pdf-inspector](https://github.com/firecrawl/pdf-inspector)（`841513d`，MIT，Rust + Python 绑定）后，将 §4.3 的分流升级为三层路由。它做三件事：**分类**（TextBased/Scanned/ImageBased/Mixed，~20ms，带置信度与页级 `pages_needing_ocr`）、**编码破损检测**（坏字体自动标记建议走 OCR）、**文本型 PDF 的本地高质量提取**（opendataloader-bench 总分 0.875、表格 TEDS 0.814，显著高于 PyMuPDF 系的 0.401；单文档 <200ms）。

```text
PDF 到达
  → pdf-inspector 检测（~20ms）
     ├─ TextBased 高置信 → 本地快速提取（pdf-inspector markdown → 复用 _parse_markdown 转 blocks）
     ├─ Scanned / ImageBased → MinerU VLM（未启用 MinerU 时走 PaddleOCR 现有路径）
     ├─ Mixed → 页级路由（文本页本地提取，扫描页 MinerU/OCR）
     └─ 编码破损标记 → OCR 路径
  → 检测本身失败/依赖缺失 → 回退现有 ocr_min_text_chars 启发式（永远有兜底）
```

价值：MinerU VLM 昂贵（GPU/远程调用），不该花在文本型 PDF 上——Firecrawl 自述约 54% 的 PDF 不需要重型解析。pdf-inspector 让"贵解析器只吃难文档"。其分类结果（文本型/扫描/混合占比）同时是「质量体检报告」的第一页素材。

边界与风险：① 年轻项目（0.2.6），按 `technology-evaluation.md` 硬门禁先 Spike 验证（Windows wheel、中文 CJK 语料实测），不直接进默认生产路径——但它只占路由位，失败即回退现有启发式，风险敞口小；② 不启用其自带 selective OCR（PP-OCRv6 + PDFium/ONNX 外部依赖），OCR 继续用已集成的 PaddleOCR；③ 其 markdown 输出经 `_parse_markdown` 复用转 blocks，不新写解析器。

分工定位：**pdf-inspector = 快而廉的路由 + 文本型提取；MinerU = 难文档深度解析；PaddleOCR = 页级兜底。**

## 5. 部署形态

- **开发期（Windows 本机）**：官方 mineru.net API（token），或 WSL2 Docker 自建（官方镜像不支持原生 Windows）。
- **生产**：docker-compose 增 `mineru-api`（8779）+ 可选 `mineru-vllm-server`，与 etcd/milvus 并列；知识服务只依赖 HTTP，符合"计算面可替换网关"架构。
- **署名义务**：关于页/对外文档标注"文档解析能力基于 MinerU"。

## 6. 实现顺序建议（待排期）

0. pdf-inspector Spike：pip 装通（Windows wheel）、黄金语料中文 PDF 跑分类与提取、比对现有 fitz 路径产出（§4.8）
1. `base.py` + `client.py`（协议与 HTTP，能力探测）
2. `adapter.py` + 单元测试（纯函数，先行）
3. `DocumentPipeline` 注入与分流回退（含 pdf-inspector 路由层）+ manifest 字段
4. `/lab` 冒烟：一份复杂 PDF 走 MinerU，肉眼比对 clean.md 与切块地图
