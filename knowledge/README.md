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
  → 清洗规则引擎（结构噪声 + 可选 PII 脱敏，命中数写入 manifest）
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
| PDF | 优先提取文本层，空白/短文本页在 OCR 可用时执行页级识别，保留页码 |
| PNG/JPEG/BMP/TIFF | 通过可选 OCR provider 识别，保留置信度和边界框 |
| XLSX | 保留工作表、行号和表格内容 |
| PPTX | 保留幻灯片、段落和表格内容 |

扫描 PDF 和图片已接入可选 PaddleOCR。未安装 OCR 依赖时，文本型文档仍正常处理；图片或纯扫描 PDF 会明确提示安装 `requirements-ocr.txt`。

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
  "owner": "xiaowang",
  "force": false
}
```

返回 `202` 和任务对象。相同 `file_id + version_id` 默认返回已有任务；`force=true` 才创建新任务。

**文档归属（2b，2026-09-01 起）**：`owner`（用户名）决定检索可见性——空/缺失 = 共享文档（所有人可见），非空 = 仅本人与 admin。**带令牌调用时归属以令牌用户为准**，请求体 `owner` 仅服务无令牌的内部/测试调用（防伪造他人归属）；`/lab/api/uploads` 谁传归谁；watcher 监听目录入库为共享。owner 会写入任务响应、manifest 与索引 chunk metadata。

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

- `GET /health`：服务、Embedding、LLM、OCR capability、索引记录和任务计数。
- `GET /cleaning/rules`：当前生效的清洗规则集（只读）。
- `POST /ingest`：旧同步入库入口，仅保留用于手工验证。
- `POST /query`：检索与生成；contexts 携带 `file_id`/`version_id`、`block_ids`、`source_locations` 和 `extraction_methods` 供引用溯源；`doc_ids` 为显式文档范围。**权限感知（2c，2026-09-01 起）**：带令牌时服务端按用户裁剪可见集（共享文档所有人可见、owner 文档仅本人与 admin，与显式 `doc_ids` 求交集），未登录不过滤；桌面端/WPS 经 Core 网关透传令牌自动生效。

### 清洗规则引擎

清洗分两层：基础归一化（Unicode/控制字符/空白/相邻去重，始终执行）+ **可配置规则引擎**。规则集是 JSON 数据，`data/cleaning_rules.json` 按 `id` 覆盖内置默认（未给出的字段继承内置值），新 `id` 追加为自定义规则；里程碑 2 起由 Java 控制面按租户下发同一 schema。

内置规则与默认开关：

| 规则 id | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `header-footer` | 跨页频率检测 | **开** | 同一短行出现在 ≥max(3, 60% 页数) 个分页上即删除 |
| `page-number` | 整行正则删除 | **开** | 仅分页文档；`- 3 -`、`第 3 页/共 10 页` 等独立页码行 |
| `phone-mask` | 正则脱敏 | 关 | `138****5678`（保前 3 后 4） |
| `id-card-mask` | 正则脱敏 | 关 | 保行政区划 + 校验位 |
| `email-mask` | 正则脱敏 | 关 | 遮本地部分，保域名 |
| `address-mask` | 正则脱敏 | 关 | 实验性，正则误伤率高 |

PII 默认关：脱敏会伤问答召回（"张三电话多少"将不可答），属业务决策。开启示例：

```json
{"rules": [{"id": "phone-mask", "enabled": true},
           {"id": "watermark", "kind": "regex_replace", "pattern": "内部资料", "replacement": ""}]}
```

每次处理的逐规则命中数记录在 manifest 的 `cleaning.rules[]`，可与清洗前文档对账。外部规则有防护：正则长度 ≤200、规则数 ≤100、坏正则/坏配置降级为警告并跳过。

## 本地 Web 可视化实验台

启动服务后访问 `http://127.0.0.1:8000/lab`，可以上传 TXT、Markdown、DOCX、PDF、XLSX、PPTX 和常见图片，查看任务进度、最近任务及 `clean.md`、`document.json`、`manifest.json` 三类派生产物；页面底部的「知识问答」面板可对已入库文档提问，回答附引用片段并可一键溯源对应 `clean.md`（未配置 LLM 时降级为纯检索模式）。

> **产品边界：** Local Lab 只用于本地开发和测试文档管线，不是 EasyShare 客户端功能，不接入当前 Wails/Vue 桌面界面，也不代表最终产品 UI。它没有生产认证、多租户隔离或 RBAC，必须只监听 `127.0.0.1` 并使用单个 Uvicorn worker。

```http
GET  /lab
POST /lab/api/uploads
GET  /lab/api/jobs?limit=20
```

上传 API 将文件写入 RustFS 后复用正式 `DocumentPipeline`，不会另建一套解析或清洗逻辑。可通过 `LOCAL_LAB_ENABLED=false` 完全关闭页面及其 API；关闭后返回 `404`，非回环来源返回 `403`。
### Office 文件格式要求

`.docx/.xlsx/.pptx` 必须是现代 Office Open XML（OOXML）文件，不支持把旧版 `.doc/.xls/.ppt` 直接改扩展名后上传。解析器会在进入第三方库前检查文件内容：

- 文件头 `D0 CF 11 E0 A1 B1 1A E1` 表示旧版 OLE Office 二进制格式；
- DOCX 必须包含 `word/document.xml`；
- XLSX 必须包含 `xl/workbook.xml`；
- PPTX 必须包含 `ppt/presentation.xml`。

遇到格式不一致时，请用 Word/WPS 打开原文件并通过“另存为”生成真正的 `.docx/.xlsx/.pptx`；只重命名扩展名不会转换格式。

## MCP Server

知识库可暴露为 [Model Context Protocol](https://modelcontextprotocol.io) 服务，供 Claude Code、Cursor、内部 OA 助手等任何 AI 工具直接检索企业知识（stdio 薄桥，转发到本服务的 `/query` 与 `/health`，业务逻辑零复制）。

```powershell
pip install mcp   # 可选依赖
cd knowledge
python -m app.mcp_server
# 环境变量 KNOWLEDGE_BASE_URL 可覆盖服务地址（默认 http://127.0.0.1:8000）
```

工具集：`knowledge_query`（检索 + 生成，返回答案与引用来源，含文档时间可判断新旧）、`knowledge_health`（索引规模与模型配置状态）。客户端配置示例（Claude Code / Cursor 等，按各工具的 MCP 配置格式填入）：

```json
{
  "mcpServers": {
    "easyshare-knowledge": {
      "command": "E:/myProgrom/有趣项目/easyShare/knowledge/.venv/Scripts/python.exe",
      "args": ["-m", "app.mcp_server"],
      "cwd": "E:/myProgrom/有趣项目/easyShare/knowledge"
    }
  }
}
```

> 注意：部分客户端拉起子进程时不透传父进程环境变量，需要显式在服务器配置的 `env` 里写 `KNOWLEDGE_BASE_URL`。

## 快速开始

> 面向公司内部部署（非开发者）：**一键向导** `powershell -ExecutionPolicy Bypass -File scripts\deploy.ps1`（Python 检查/RustFS/依赖/配置/账号/防火墙/自启全代劳，约 5 分钟）；逐步说明与同事使用一页纸见 [`../docs/company-rollout-guide.md`](../docs/company-rollout-guide.md)。

```powershell
cd knowledge
python -m venv .venv
.\.venv\Scripts\Activate.ps1
pip install -r requirements.txt
# 可选：需要扫描件/图片 OCR 时安装
pip install -r requirements-ocr.txt
Copy-Item .env.example .env
uvicorn app.main:app --host 127.0.0.1 --port 8000 --workers 1
# 或用启动脚本（公司部署）：powershell -File scripts\start_server.ps1；开机自启：scripts\install_autostart.ps1
```

多人与共享盘（公司使用要点，详见部署手册）：

- `AUTH_ENABLED=true` 开启账号登录（首管理员 `/auth/bootstrap`，之后管理员建同事账号）；/lab 内置登录条。
- `WATCH_DIRS=D:\共享盘\知识库入库` 开启目录监听自动入库：新文件/改动自动入知识库，同内容不重复入库，失败自动重试。

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
| `OCR_ENABLED` / `OCR_LANG` / `OCR_MIN_TEXT_CHARS` | 是否启用 OCR、PaddleOCR 语言与 PDF 短文本页触发阈值 | `true` / `ch` / `20` |
| `CHUNK_SIZE` / `CHUNK_OVERLAP` | 切块长度与重叠 | `800` / `120` |
| `CLEANING_RULES_PATH` | 清洗规则集 JSON（不存在则用内置默认） | `./data/cleaning_rules.json` |
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

测试覆盖多格式解析、清洗、Office OLE/OOXML 格式签名与类型错配、损坏文件、fake OCR 图片/扫描 PDF/混合 PDF、来源感知切块、OCR health/manifest/query 字段、任务幂等/重试/恢复、三类派生产物、版本替换、Local Lab 页面/上传/访问边界和 API 行为。真实 RustFS 与 PaddleOCR 测试默认跳过，因此普通回归不依赖 Docker 或 OCR 模型。

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

### 检索质量评测

`tests/retrieval/` 维护标注评测集（30 条「问题 → 应命中文档/片段」，语料 = 黄金 Office 样本 + `tests/retrieval/corpus/` 下的企业文档），经真实 `DocumentPipeline` 索引后计算 recall@5、hit@1、MRR 与片段命中率。常规回归中以 HashEmbedder 确定性基线运行，指标跌破阈值即失败：

```powershell
.\.venv\Scripts\python.exe -m pytest tests/retrieval -q
```

对比语义检索质量或切块参数改动时，用脚本跑同一评测集（`--real` 使用 `.env` 配置的真实 Embedding，会产生少量 API 调用）：

```powershell
.\.venv\Scripts\python.exe scripts/eval_retrieval.py
.\.venv\Scripts\python.exe scripts/eval_retrieval.py --real --output data/eval-report.json
```

任何切块、Embedding、向量库或检索策略改动，都应先留存新旧评测报告再合入；阈值调整需同步更新对应迭代记录。

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
│  ├─ ocr/          # 可选 OCR Provider 与 PaddleOCR 适配
│  ├─ parsing/      # 多格式解析、清洗、统一结构、Markdown 渲染
│  ├─ pipeline/     # 下载 → 解析 → 产物 → 切块 → 索引编排
│  ├─ storage/      # RustFS / 可替换对象存储接口
│  ├─ kb/           # 切块、Embedding、向量存储与检索
│  ├─ eval/         # 检索质量评测器（recall@k / MRR / 片段命中率）
│  ├─ rag/          # LLM 生成
│  ├─ services.py   # 依赖组装与生命周期
│  └─ main.py       # FastAPI 入口
├─ scripts/          # 黄金语料物化、检索评测等开发脚本
├─ tests/
│  ├─ golden/        # Office/PDF 确定性样本与可审查预期
│  ├─ retrieval/     # 检索质量评测集（语料 + 标注 + 基线测试）
│  └─ integration/   # 显式开启的真实 RustFS 测试
├─ requirements.txt
├─ requirements-dev.txt
└─ requirements-ocr.txt  # 可选 PaddleOCR 扫描件依赖
```

## 当前限制与下一步

1. 真实生产任务编排需迁移到 Java + 消息队列/任务系统，Python 任务必须继续保持幂等。
2. JSON 向量存储是当前验证实现，后续按路线图迁移 Milvus。
3. 引入更强的结构感知解析/切块，同时保留当前统一 `DocumentBlock`、OCR metadata 和派生产物契约。
4. Go/Java 接入时只传对象身份和业务上下文，不通过 HTTP 重传整个 Office 文件。
