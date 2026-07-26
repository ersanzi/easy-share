# Knowledge Service（知识平台计算面）

EasyShare 的 Python 计算面服务。当前职责是从 RustFS 读取原始文件，完成解析、清洗、切块、Embedding、索引和 RAG 查询；它不负责桌面上传、多租户、权限或业务元数据。

总体设计见 [`../docs/knowledge-platform.md`](../docs/knowledge-platform.md)。

## 架构边界

```text
Go / Wails
  └─ 文件采集与上传 RustFS
          ↓ object_key
Python Knowledge Service
  ├─ 异步任务状态（当前 SQLite 过渡实现）
  ├─ 文档解析与结构化清洗
  ├─ clean.md / document.json / manifest.json
  ├─ 切块、Embedding、索引
  └─ 检索与生成
          ↑
Java Control Plane（后续）
  └─ 租户、账号、权限、文件元数据、业务编排与任务真相源
```

- **Go** 继续负责文件采集和上传，不把 Office 解析塞进 Core。
- **Python** 只做计算密集、AI 相关的文档处理能力。
- **Java 后置**；引入 Java 后，租户、权限、文件版本和业务任务由 Java 持有，Python SQLite 不升级为业务数据库。
- 当前版本只适合 **单个 Uvicorn worker**。进程内线程池和本地 SQLite/JSON 存储不是多实例调度方案。

## 当前处理闭环

```text
RustFS 原始对象
  → 下载与大小限制
  → 结构化解析
  → Unicode / 控制字符 / 空白清洗与相邻重复块去重
  → clean.md + document.json
  → 切块
  → Embedding
  → 按 file_id 原子替换当前向量索引
  → manifest.json
```

首批格式：

| 格式 | 当前能力 |
| --- | --- |
| TXT | UTF-8/GB18030 解码、段落清洗 |
| Markdown | 标题与段落结构保留 |
| DOCX | 尽量保持标题、段落、表格的文档顺序 |
| PDF | 提取文本层并保留页码 |
| XLSX | 保留工作表、行号和表格内容 |
| PPTX | 保留幻灯片、段落和表格内容 |

扫描 PDF 和图片 OCR 暂不处理；没有文本层的 PDF 会明确失败并提示需要 OCR，而不是静默生成空知识。

## 派生产物

每个文件版本写入固定前缀：

```text
derived/{fileId}/{versionId}/clean.md
derived/{fileId}/{versionId}/document.json
derived/{fileId}/{versionId}/manifest.json
```

- `clean.md`：便于人读、调试和后续切块的规范化文本。
- `document.json`：统一块结构，包含页码、工作表、幻灯片、段落、表格等来源定位。
- `manifest.json`：处理版本、源文件 SHA-256、字节数、块数、字符数、切块数、警告和产物键。

`file_id` 是稳定文档身份，`version_id` 标识内容版本。新版本索引成功后，以 `file_id` 替换旧版本向量记录，避免默认检索同时命中多个历史版本。

## 异步任务 API

### 提交处理

```http
POST /documents/process
Content-Type: application/json
```

```json
{
  "file_id": "file-001",
  "version_id": "v1",
  "object_key": "uploads/file-001/v1/制度.docx",
  "filename": "制度.docx",
  "force": false
}
```

返回 `202` 和任务对象。相同 `file_id + version_id` 默认返回已有任务；`force=true` 才创建新任务。

任务状态：

```text
queued → processing → completed
                    ↘ failed → queued（retry）
```

### 查询与重试

```http
GET  /jobs/{jobId}
POST /jobs/{jobId}/retry
```

只有 `failed` 任务可以重试。服务重启时，遗留的 `processing` 任务会恢复为 `queued` 并重新执行。

### 读取派生产物

```http
GET /documents/{fileId}/versions/{versionId}/artifacts
GET /documents/{fileId}/versions/{versionId}/artifacts/clean.md
GET /documents/{fileId}/versions/{versionId}/artifacts/document.json
GET /documents/{fileId}/versions/{versionId}/artifacts/manifest.json
```

### 兼容接口

- `GET /health`：服务、Embedding、LLM、索引记录和任务计数。
- `POST /ingest`：旧同步入库入口，仅保留用于手工验证。
- `POST /query`：检索与生成；`doc_ids` 预留给未来 Java 控制面传入已授权文档范围。

## 本地 Web 可视化实验台

启动服务后访问 `http://127.0.0.1:8000/lab`，可以上传 TXT、Markdown、DOCX、文本型 PDF、XLSX 和 PPTX，查看任务进度、最近任务及 `clean.md`、`document.json`、`manifest.json` 三类派生产物。

> **产品边界：** Local Lab 只用于本地开发和测试文档管线，不是 EasyShare 客户端功能，不接入当前 Wails/Vue 桌面界面，也不代表最终产品 UI。它没有生产认证、多租户隔离或 RBAC，必须只监听 `127.0.0.1` 并使用单个 Uvicorn worker。

```http
GET  /lab
POST /lab/api/uploads
GET  /lab/api/jobs?limit=20
```

上传 API 将文件写入 RustFS 后复用正式 `DocumentPipeline`，不会另建一套解析或清洗逻辑。可通过 `LOCAL_LAB_ENABLED=false` 完全关闭页面及其 API；关闭后返回 `404`，非回环来源返回 `403`。
## 快速开始

```powershell
cd knowledge
python -m venv .venv
.\.venv\Scripts\Activate.ps1
pip install -r requirements.txt
Copy-Item .env.example .env
uvicorn app.main:app --host 127.0.0.1 --port 8000 --workers 1
```

Swagger：`http://127.0.0.1:8000/docs`

Local Lab：`http://127.0.0.1:8000/lab`（仅本地开发测试）

> 开发时可以使用 `--reload`，但不要同时添加多个 worker。

## 配置

所有凭证来自环境变量或 `.env`，不要写入代码或提交真实密钥。

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `LOCAL_LAB_ENABLED` | 是否启用仅回环可访问的 Local Lab 测试页面与上传 API | `true` |
| `RUSTFS_ENDPOINT` / `RUSTFS_ACCESS_KEY` / `RUSTFS_SECRET_KEY` / `RUSTFS_BUCKET` | RustFS（S3 兼容）连接 | 本地 9000 / `easyshare` |
| `LLM_BASE_URL` / `LLM_API_KEY` / `LLM_MODEL` | OpenAI 兼容生成模型；留空不生成 | 空 |
| `EMBEDDING_BASE_URL` / `EMBEDDING_API_KEY` / `EMBEDDING_MODEL` | OpenAI 兼容 Embedding；留空使用流程占位实现 | 空 |
| `EMBEDDING_DIM` | 向量维度，必须与模型一致 | `1024` |
| `CHUNK_SIZE` / `CHUNK_OVERLAP` | 切块长度与重叠 | `800` / `120` |
| `VECTOR_STORE_PATH` | 当前 JSON 向量存储 | `./data/vector_store.json` |
| `JOB_STORE_PATH` | 当前 SQLite 执行任务库 | `./data/jobs.db` |
| `JOB_WORKERS` | 进程内任务线程数 | `2` |
| `MAX_SOURCE_BYTES` | 单个源对象读取上限 | `104857600` |

未配置真实 Embedding 时使用 `HashEmbedder`，只能验证端到端流程，**不代表语义检索质量**。

## 测试

```powershell
cd knowledge
pip install -r requirements-dev.txt
$env:PYTHONDONTWRITEBYTECODE='1'
.\.venv\Scripts\python.exe -m pytest tests -q
```

测试覆盖多格式解析、清洗、损坏文件、OCR 提示、任务幂等/重试/恢复、三类派生产物、版本替换、Local Lab 页面/上传/访问边界和 API 行为。真实 RustFS 测试默认跳过，因此普通回归不依赖 Docker。

### Office/PDF 黄金语料

首批黄金样本覆盖 DOCX、XLSX、PPTX 和文本型 PDF。二进制文档由 `tests/golden/builders.py` 确定性构造，人工可审查预期保存在 `tests/golden/cases.json`：

```powershell
.\.venv\Scripts\python.exe -m pytest tests/golden -q
```

如需用 Office/PDF 阅读器人工查看，可物化到已忽略的 `tests/golden/generated/`：

```powershell
.\.venv\Scripts\python.exe scripts/build_golden_corpus.py
```

不要提交生成的二进制文件。调整解析行为时，应同时审查结构块、来源位置、Markdown 片段和重复生成的语义一致性。

### 真实 RustFS 集成测试

测试要求 RustFS 已启动且目标 bucket 已存在；它不会自动创建或删除 bucket，只会创建唯一测试源对象并精确清理该对象与三个派生产物：

```powershell
$env:EASYSHARE_RUSTFS_INTEGRATION = '1'
$env:EASYSHARE_RUSTFS_ENDPOINT = 'http://127.0.0.1:9000'
$env:EASYSHARE_RUSTFS_ACCESS_KEY = '<access-key>'
$env:EASYSHARE_RUSTFS_SECRET_KEY = '<secret-key>'
$env:EASYSHARE_RUSTFS_BUCKET = 'easyshare'
.\.venv\Scripts\python.exe -m pytest tests/integration -q -m integration
```

测试验证 `RustFS 源对象 → 解析清洗 → clean.md/document.json/manifest.json → 版本化索引`，同时检查派生产物 Content-Type。未设置 `EASYSHARE_RUSTFS_INTEGRATION=1` 时会明确跳过。

## 目录结构

```text
knowledge/
├─ app/
│  ├─ api/          # FastAPI 路由与模型
│  ├─ jobs/         # SQLite 任务状态与线程执行器
│  ├─ lab/          # 仅供本地测试的上传与管线可视化页面
│  ├─ parsing/      # 多格式解析、清洗、统一结构、Markdown 渲染
│  ├─ pipeline/     # 下载 → 解析 → 产物 → 切块 → 索引编排
│  ├─ storage/      # RustFS / 可替换对象存储接口
│  ├─ kb/           # 切块、Embedding、向量存储与检索
│  ├─ rag/          # LLM 生成
│  ├─ services.py   # 依赖组装与生命周期
│  └─ main.py       # FastAPI 入口
├─ scripts/          # 黄金语料物化等开发脚本
├─ tests/
│  ├─ golden/        # Office/PDF 确定性样本与可审查预期
│  └─ integration/   # 显式开启的真实 RustFS 测试
├─ requirements.txt
└─ requirements-dev.txt
```

## 当前限制与下一步

1. 真实生产任务编排需迁移到 Java + 消息队列/任务系统，Python 任务必须继续保持幂等。
2. JSON 向量存储是当前验证实现，后续按路线图迁移 Milvus。
3. 增加 PaddleOCR，覆盖扫描 PDF 和图片。
4. 引入更强的结构感知解析/切块，同时保留当前统一 `DocumentBlock` 和派生产物契约。
5. Go/Java 接入时只传对象身份和业务上下文，不通过 HTTP 重传整个 Office 文件。
