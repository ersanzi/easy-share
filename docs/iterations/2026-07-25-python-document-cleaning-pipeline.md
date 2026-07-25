# Python 文档入库与结构化清洗闭环

> 日期：2026-07-25
> 状态：进行中

## 1. 用户问题

EasyShare 已确定长期采用 Go 客户端数据面、Java 云端控制面、Python AI 计算面的架构。当前优先完善 Go 已有能力和 Python 文档处理能力，Java 多租户、权限与业务编排后置。

用户确认本轮先完成 Python 独立闭环：

- 文件仍由 Go 上传到 RustFS；
- Python 不直接接收原始上传流，只接收 `fileId + versionId + objectKey`；
- 首批支持 TXT、Markdown、DOCX、文本型 PDF、XLSX、PPTX；
- 本轮不接 Go/Vue 界面，先通过 Python API 和自动化测试验收；
- 扫描 PDF 与图片 OCR 后续单独实现。

## 2. 目标

1. 建立异步文档处理任务，调用方提交对象引用后立即获得任务 ID。
2. Python 从 RustFS 读取原文件，解析为统一、可追溯的结构化文档。
3. 执行基础清洗，保留标题、段落、表格、页码、工作表与幻灯片来源。
4. 把 `clean.md`、`document.json`、`manifest.json` 写回 RustFS。
5. 清洗成功后切块、向量化并替换当前文件版本的索引。
6. 支持任务查询、幂等提交、失败重试和进程重启后的未完成任务恢复。
7. 保留现有 `/ingest` 与 `/query` 骨架能力，避免破坏已有验证方式。

## 3. 非目标

- 本轮不修改 Go Core、Wails 绑定或 Vue 界面。
- 不实现 Java 账号、租户、RBAC、配额和审计。
- 不实现扫描 PDF、图片 OCR、复杂版面还原和公式识别。
- 不在本轮引入 RabbitMQ/Kafka、Celery、Milvus 或完整 Unstructured 管线。
- Python 的本地任务库不是未来 Java 控制面的业务真相源。

## 4. 数据与产物模型

### 4.1 提交请求

```json
{
  "file_id": "稳定文件 ID",
  "version_id": "内容版本 ID",
  "object_key": "RustFS 对象键",
  "filename": "可选显示名称",
  "force": false
}
```

### 4.2 任务状态

```text
queued -> processing -> completed
                     -> failed -> queued（retry）
```

相同 `fileId + versionId` 的非强制重复提交返回已有任务，避免重复解析和重复索引。

### 4.3 RustFS 派生产物

```text
derived/{fileId}/{versionId}/clean.md
derived/{fileId}/{versionId}/document.json
derived/{fileId}/{versionId}/manifest.json
```

- `clean.md`：供人工检查和后续 LLM 使用的规范化文本。
- `document.json`：带来源定位的结构化块。
- `manifest.json`：处理器版本、原对象、字符数、块数、告警与产物位置。

## 5. API 兼容策略

新增：

```text
POST /documents/process
GET  /jobs/{jobId}
POST /jobs/{jobId}/retry
GET  /documents/{fileId}/versions/{versionId}/artifacts
```

保留：

```text
POST /ingest
POST /query
GET  /health
```

新能力以 `fileId + versionId` 为身份；旧 `/ingest` 的 `doc_id` 仅作为兼容入口。

## 6. 代码影响

预计新增或修改：

- `knowledge/app/parsing/`：统一文档模型、Office/PDF 解析、文本清洗和 Markdown 渲染。
- `knowledge/app/jobs/`：SQLite 任务存储与进程内执行器。
- `knowledge/app/pipeline/`：读取、解析、保存产物、切块与索引的编排。
- `knowledge/app/storage/rustfs.py`：增加派生产物写入与读取。
- `knowledge/app/api/`：任务提交、查询、重试、产物查询。
- `knowledge/tests/`：格式解析、幂等任务、失败重试和端到端管线测试。
- `knowledge/requirements.txt`、`.env.example`、`README.md`：依赖与运行说明。

## 7. 故障注入方案

至少验证：

1. RustFS 对象不存在时任务进入 `failed`，保留明确错误。
2. 不支持或损坏的 Office 文件不退回乱码文本，而是显式失败。
3. 文本型 PDF 无可提取文本时给出 OCR 尚未启用的告警或失败说明。
4. 派生产物写入中途失败时不标记完成、不替换索引。
5. 相同文件版本重复提交不创建重复任务。
6. 重试失败任务时复用原任务身份并增加重试次数。
7. 服务重启后把遗留的 `processing` 任务恢复为待执行状态。

## 8. 验证结果

实施完成后补充：

- Python 单元测试与 API 测试；
- 本地样例文件端到端处理；
- Go、前端与 Wails 既有完整构建回归；
- RustFS 可用时的真实对象读写验证，无法使用时记录替代验证。

## 9. 排障方法

### 9.1 任务一直停留在 queued

检查服务是否以单进程方式启动、任务执行器是否在 FastAPI lifespan 中启动，以及 `JOB_WORKERS` 是否大于零。骨架阶段不要使用多个 Uvicorn worker 共享同一个本地执行器。

### 9.2 Office 文件解析失败

先确认扩展名与真实格式一致，再检查 `python-docx`、`openpyxl`、`python-pptx` 依赖是否安装。损坏文件必须返回明确错误，不能退回二进制文本解码。

### 9.3 PDF 清洗结果为空

当前只处理带文本层的 PDF。扫描件需要 OCR，空文本 PDF 应从任务错误或 manifest 告警中识别，不能误判为成功入库。

### 9.4 清洗成功但查询不到

检查 manifest 中的 `chunks`，确认 Embedding 调用成功，并确认向量记录使用 `fileId + versionId` 作为版本化文档身份。重新处理同一版本前应先删除旧索引。
