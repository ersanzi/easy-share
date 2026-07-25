# Python 文档入库与结构化清洗闭环

> 日期：2026-07-25
> 状态：已完成

## 1. 用户问题

EasyShare 长期采用 Go 客户端数据面、Java 云端控制面、Python AI 计算面的架构。当前优先完善 Go 已有能力和 Python 文档处理能力，Java 多租户、权限与业务编排后置。

用户确认本轮先完成 Python 独立闭环：

- 文件仍由 Go 上传到 RustFS；
- Python 不直接接收原始上传流，只接收 `fileId + versionId + objectKey`；
- 首批支持 TXT、Markdown、DOCX、文本型 PDF、XLSX、PPTX；
- 本轮不接 Go/Vue 界面，先通过 Python API 和自动化测试验收；
- 扫描 PDF 与图片 OCR 后续单独实现。

## 2. 架构结论

当前顺序是合理的：

1. **Go 先保持稳定**：继续负责桌面采集、局域网传输和 RustFS 上传，不承载 Office 解析与 AI 依赖。
2. **Python 先形成计算闭环**：从对象存储读取文件，产出结构化数据、可读清洗文本和索引。
3. **Java 最后接入业务治理**：租户、账号、RBAC、文件元数据、配额、审计和业务任务编排由 Java + PostgreSQL 持有。

Python 的 SQLite 只保存当前单进程执行状态，用于幂等、进度、重试和重启恢复；它不是未来业务真相源，也不应继续扩张为权限或文件数据库。

## 3. 已完成能力

### 3.1 异步任务

```text
queued → processing → completed
                    ↘ failed → queued（retry）
```

- 相同 `file_id + version_id` 的非强制提交返回已有任务；
- `force=true` 创建新任务；
- 失败任务可重试并增加 `retry_count`；
- 服务启动时将遗留 `processing` 恢复为 `queued`；
- 任务状态更新带前置状态约束，非法迁移明确失败。

### 3.2 多格式结构化解析

- TXT：UTF-8/GB18030 解码、段落清洗；
- Markdown：标题边界与段落保留，即使标题后没有空行也不会合并；
- DOCX：尽量保持标题、段落、表格的原始顺序；
- PDF：提取文本层并记录页码，无文本层时明确提示 OCR；
- XLSX：记录工作表、行号和表格内容；
- PPTX：记录幻灯片、段落和表格内容；
- 统一输出 `SourceLocation`、`DocumentBlock`、`ParsedDocument`。

### 3.3 清洗与派生产物

```text
derived/{fileId}/{versionId}/clean.md
derived/{fileId}/{versionId}/document.json
derived/{fileId}/{versionId}/manifest.json
```

清洗包含 Unicode 规范化、控制字符/空白处理和相邻重复块去重。manifest 记录处理器版本、源对象 SHA-256、字节数、结构块数、字符数、切块数、警告和产物键。

### 3.4 索引一致性

- `file_id` 作为稳定文档身份，`version_id` 写入向量记录和 chunk ID；
- 新版本成功时按 `file_id` 原子替换旧索引；
- 同一文件的不同处理任务在进程内串行执行；
- `manifest.json` 写入失败时恢复旧索引，失败任务不会污染默认查询结果；
- JSON 向量文件使用临时文件 + `os.replace` 保存。

## 4. API

新增：

```text
POST /documents/process
GET  /jobs/{jobId}
POST /jobs/{jobId}/retry
GET  /documents/{fileId}/versions/{versionId}/artifacts
GET  /documents/{fileId}/versions/{versionId}/artifacts/{name}
```

保留：

```text
POST /ingest
POST /query
GET  /health
```

新能力以 `file_id + version_id` 为身份；旧 `/ingest` 仅作为兼容和手工验证入口。

## 5. 代码影响

新增或重构：

- `knowledge/app/parsing/`：统一文档模型、Office/PDF 解析、清洗和 Markdown 渲染；
- `knowledge/app/jobs/`：SQLite 任务存储与线程执行器；
- `knowledge/app/pipeline/`：对象读取、解析、产物、切块、Embedding、索引和回滚；
- `knowledge/app/storage/`：可替换对象存储接口和 RustFS 读写；
- `knowledge/app/services.py`：依赖容器与生命周期；
- `knowledge/app/api/`：任务、产物、兼容入库和查询接口；
- `knowledge/tests/`：解析、任务、管线和 API 测试；
- `knowledge/requirements*.txt`、`.env.example`、`README.md`：依赖、配置和运行说明。

## 6. 故障注入与结果

1. 对象不存在：任务进入 `failed` 并保留错误；
2. 损坏 Office：显式解析失败，不回退乱码解码；
3. 空白 PDF：明确提示需要 OCR；
4. manifest 写入失败：处理不完成，并恢复旧版本索引；
5. 重复提交：不创建重复任务；
6. failed 重试：复用任务身份并增加次数；
7. 重启恢复：遗留 processing 恢复 queued；
8. 非法状态迁移：进度、完成、失败操作均拒绝非 processing 任务。

## 7. 验证结果

2026-07-25 完成：

- Python 源码无落盘语法检查：通过；
- `python -m pytest knowledge/tests -q`：**16 passed**；
- `go build ./...`：通过；
- `go test ./...`：通过；
- `npm --prefix frontend run build`：通过；
- `npm --prefix frontend test`：**4 files / 10 tests passed**；
- `wails build`：通过，生成 `build/bin/easyshare.exe`；
- `go build -o build/bin/easyshare-core.exe ./cmd/core`：通过。

本轮未执行真实 RustFS 集成读写，使用可注入 `FakeStorage` 验证对象读取、三类产物、写入故障和索引回滚。真实凭据与 Docker 环境恢复后再做外部依赖验收。

测试出现一条 Starlette `TestClient` 关于 httpx 的弃用警告，不影响通过结果；后续依赖升级时处理，不在本轮为警告盲目改动运行时版本。

## 8. 排障方法

### 8.1 任务一直停留在 queued

检查服务是否以单进程方式启动、任务执行器是否在 FastAPI lifespan 中启动，以及 `JOB_WORKERS` 是否大于零。当前不要使用多个 Uvicorn worker 共享同一个 SQLite/JSON 执行状态。

### 8.2 Office 文件解析失败

先确认扩展名与真实格式一致，再检查 `python-docx`、`openpyxl`、`python-pptx` 依赖。损坏文件必须返回明确错误，不能退回二进制文本解码。

### 8.3 PDF 清洗结果为空

当前只处理带文本层的 PDF。扫描件需要 OCR，空文本 PDF 会明确失败并提示 OCR，不能误判为成功入库。

### 8.4 清洗成功但查询不到

检查 manifest 的 `chunks` 和任务状态，确认 Embedding 成功，并检查当前索引中的 `file_id/version_id`。manifest 不存在时不得把该版本视为完成。

### 8.5 Windows 下 Python 文件出现 BOM 或并发缓存错误

PowerShell 5 的 `Set-Content -Encoding utf8` 会写 BOM；直接读取源码后调用 `compile()` 时应使用无 BOM UTF-8，或以 `utf-8-sig` 读取。并发验证时设置 `PYTHONDONTWRITEBYTECODE=1`，避免多个进程同时写 `__pycache__`。

## 9. 下一步

1. PaddleOCR：扫描 PDF 与图片；
2. Unstructured：复杂版面与更多格式的统一元素解析；
3. 结构感知切块与表格策略；
4. Milvus Standalone 替换 JSON 向量存储；
5. Java 控制面接入稳定 `file_id/version_id`、租户、权限和业务任务编排；
6. 最后再接 Go/Vue 的上传后触发和任务进度展示。