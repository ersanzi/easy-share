# 2026-07-28 PaddleOCR 扫描件 OCR 与来源感知切块

## 状态

已完成并通过全量回归。真实 PaddleOCR 模型集成测试需要显式安装可选依赖并手动开启，本轮未执行。

## 背景

知识平台此前已完成文本型 TXT/Markdown/DOCX/PDF/XLSX/PPTX 的解析、清洗、Markdown 产物、Embedding、向量索引和检索闭环，但扫描 PDF 与图片仍无法进入同一条管线。本轮先补齐 OCR 最小闭环，再把解析块的来源信息传递到向量 metadata，保证后续问答能够回答“内容来自哪一页、哪一个块、哪种提取方式”。

## 目标

1. 支持 PNG/JPEG/BMP/TIFF 图片，以及 PDF 页面级 OCR。
2. OCR 依赖可选安装，普通测试不下载 PaddleOCR 模型。
3. 文本层 PDF 优先保留已有文本；空白页或短文本页在 OCR 可用时尝试 OCR，OCR 失败时保留已有文本。
4. OCR 块保留页码、稳定块 ID、置信度与边界框。
5. 来源感知切块保留 `block_ids`、`source_locations`、`extraction_methods`，并写入向量 metadata 与 `/query` context。
6. manifest 和 `/health` 声明 OCR provider、可用性、页级结果及低置信度统计。

## 非目标

- 本轮不引入 Java 控制面、WPS 插件、Milvus 或 Unstructured。
- 不在普通 CI 中下载 PaddleOCR 模型或要求 GPU。
- 不把 WebDAV、客户端 UI 与 OCR 逻辑耦合。
- 不改变已有文本型文档的对外 API 入口。

## 实施结果

### OCR Provider

新增 `knowledge/app/ocr/` 抽象层：

- `OCRProvider`：统一 capability 与单页图片识别接口。
- `PaddleOCRProvider`：PaddleOCR 2.x 适配器，依赖懒加载，初始化失败通过 capability 暴露。
- `UnavailableOCRProvider`：未安装或配置关闭时的明确失败实现。
- `OCRPageResult`/`OCRTextBlock`：承载正文、置信度、边界框和耗时。

可选依赖位于 `knowledge/requirements-ocr.txt`，不进入普通 `requirements.txt`。

### 解析与管线

- `parse_document()` 新增图片格式与 OCR 参数。
- PDF 使用 `pypdf` 提取文本层；需要 OCR 时使用 PyMuPDF 将单页渲染为 PNG，再交给 provider。
- 混合 PDF 只对缺少文本层或短文本页尝试 OCR，已有文本层在 OCR 失败时不会丢失。
- OCR 结果转换为 `DocumentBlock`，携带页码、`extraction_method=ocr`、置信度和 bbox。
- `DocumentPipeline` 使用结构化 `chunk_document()`，Embedding 仍只接收正文列表，现有索引替换和失败回滚逻辑不变。

### 来源与可观测性

- chunk metadata：`block_ids`、`source_locations`、`extraction_methods`。
- manifest 增加 `ocr` 报告：provider、总页数、成功 OCR 页、失败页、低置信度块数、OCR 耗时。
- `/health` 增加 OCR capability；`/query` contexts 返回来源字段。

## 验证

普通回归（不依赖 OCR 模型）：

```powershell
knowledge/.venv/Scripts/python.exe -m compileall -q knowledge/app knowledge/tests
knowledge/.venv/Scripts/python.exe -m pytest -m "not integration and not ocr_integration" knowledge/tests -q
# 60 passed, 2 deselected

knowledge/.venv/Scripts/python.exe -m pytest knowledge/tests -q
# 60 passed, 2 skipped

go test ./...
# 全部通过

npm --prefix frontend test
# 19 passed

npm --prefix frontend run build
# vue-tsc 与 Vite build 通过

powershell -ExecutionPolicy Bypass -File scripts/build.ps1
# 全量流水线通过，产出 easyshare.exe、easyshare-core.exe 与 NSIS 安装包
```

跳过项为显式集成测试：真实 RustFS 与真实 PaddleOCR 模型。普通回归不安装 OCR 可选依赖，不下载模型。

显式 OCR 集成回归（需要 PaddleOCR 依赖、模型和网络/本地缓存）：

```powershell
knowledge/.venv/Scripts/pip.exe install -r knowledge/requirements-ocr.txt
$env:EASYSHARE_OCR_INTEGRATION = "1"
knowledge/.venv/Scripts/python.exe -m pytest -m ocr_integration knowledge/tests
```

## 回滚

如需暂时关闭 OCR，可设置 `OCR_ENABLED=false`，文本型文档仍走原有解析路径；图片和纯扫描 PDF 会返回可操作的依赖提示。若来源感知切块出现问题，可先回退 `DocumentPipeline` 到 `chunk_text()`，保留 OCR 解析模块独立验证。

## 归档记录

- 归档日期：2026-07-28。
- 已同步 `README.md`、`docs/progress.md`、`docs/architecture.md`、`docs/knowledge-platform.md` 与 `knowledge/README.md`。
- 真实 PaddleOCR 模型测试保留为显式集成验证，不影响本轮功能与普通 CI 收口。

## 后续建议

下一步先实现结构感知切块的最小适配层：优先处理标题、段落、表格、列表和页面边界，比较 Unstructured 与当前 `DocumentBlock` 的来源字段映射；使用现有检索评测集验证 recall@5、hit@1、MRR 和引用来源完整性，确认无回归后再迁移 Milvus。
