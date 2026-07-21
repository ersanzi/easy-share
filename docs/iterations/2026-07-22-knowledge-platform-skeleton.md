# 迭代：知识平台里程碑 0 — Python AI 服务最小骨架

> 日期：2026-07-22
> 阶段：企业知识管理平台 — 里程碑 0
> 状态：已完成（管线端到端跑通，待配置真实 embedding/LLM 做语义验收）

## 背景与目标

产品定位从消费级文件工具向企业知识管理平台演进。完整管线为：采集传输（已有）→ 存储 RustFS（已有）→ 解析清洗 → 知识库（RAG）→ AI 写作辅助 → WPS 插件交付。

本里程碑采用 walking skeleton 策略，只搭 Python 计算面的最薄版本，跑通"文档 → 知识 → 问答"端到端闭环，证明核心命题"文档能否变成可用知识"。账号、权限、WPS、多格式均后置。

总体愿景与架构决策见 [`../knowledge-platform.md`](../knowledge-platform.md)。

## 关键技术决策

1. **RAG 而非微调**：目标是基于企业文档提供帮助，靠检索注入知识，不做模型微调。成本低、可更新、可溯源。
2. **Java 控制面 + Python 计算面**：Java 管账号/权限/编排/大数据（复用 qn-trusted-connector 经验），Python 管解析/embedding/向量检索/RAG/LLM（生态优势）。共享 RustFS + 文件 ID 体系，REST 同步 + 消息队列异步。
3. **权限感知检索**：`/query` 的 `doc_ids` 参数是预留给 Java 控制面的缝——Java 算出用户可访问的文档 ID 传入，Python 只在该范围内检索。
4. **embedding/LLM 抽象为可替换网关**：现在接云端 OpenAI 兼容接口，以后可换本地模型，代码不改。未配置 embedding 时退回 HashEmbedder 占位（仅跑通管线，无语义）。
5. **向量库最薄实现**：内存 + numpy 余弦 + JSON 持久化，里程碑 1 换 Chroma/Milvus，接口（add/delete_doc/query）不变。

## 代码影响

新增 `knowledge/` Python 服务（与 Go 桌面端并列，互不干扰）：

| 文件 | 职责 |
| --- | --- |
| `app/main.py` | FastAPI 入口 |
| `app/config.py` | 环境变量配置（pydantic-settings，凭证不写死） |
| `app/storage/rustfs.py` | 从 RustFS（S3 兼容）读文件 |
| `app/parsing/extractor.py` | 文档 → 文本（txt/md/docx/pdf，未知格式退回文本解码） |
| `app/kb/chunker.py` | 按段落切块 + overlap |
| `app/kb/embedder.py` | 可替换向量化（OpenAIEmbedder / HashEmbedder 占位） |
| `app/kb/store.py` | 最薄向量库（内存 + numpy + JSON + doc_id 过滤） |
| `app/rag/retriever.py` | 检索 top-k 相关片段 |
| `app/rag/generator.py` | 拼 prompt 调 LLM 生成可溯源回答 |
| `app/api/routes.py` | `/health` `/ingest` `/query` |
| `samples/sample.md` | 测试样例文档 |

文档：新增 `docs/knowledge-platform.md`（愿景架构）、`docs/testing/knowledge-service-checklist.md`（测试清单）；更新 `docs/progress.md`、`README.md`。

## 验证记录

- 依赖安装成功（Python 3.13，venv）。
- 服务启动正常，`/health` 返回 ok。
- 场景 1（零配置）：入库文本 → 提问检索到含答案片段 → `doc_ids` 权限过滤生效（不匹配返回空）。
- 记录持久化到 `data/vector_store.json`，重启后仍在。
- **待用户验收**：配置真实 embedding + LLM 后的语义问答（场景 2）、真实 docx/pdf 解析（场景 3）。

## 排障方法（省的下次还有问题）

- **启动报 ModuleNotFoundError**：没激活 venv 或没装依赖。`.venv\Scripts\activate` + `pip install -r requirements.txt`。
- **配了 embedding/LLM 但 health 仍显示占位**：对应三个环境变量（BASE_URL/API_KEY/MODEL）必须全填，缺一即退回；改 `.env` 后须重启。
- **换 embedding 模型后结果异常**：向量维度变了，须删 `data/vector_store.json` 重新入库；`EMBEDDING_DIM` 要与模型实际维度一致。
- **docx/pdf 解析后无文本**：可能是扫描件 PDF（无文本层），骨架不含 OCR。
- **Windows bash 路径坑**：venv 路径用正斜杠 `./.venv/Scripts/python.exe`，反斜杠会被 bash 吞。
- 完整测试步骤见 [`../testing/knowledge-service-checklist.md`](../testing/knowledge-service-checklist.md)。

## 下一步（里程碑 1）

扩展解析格式、改进切块与清洗、向量库换 Chroma/Milvus；随后里程碑 2 接入 Java 控制面（账号/权限/权限感知检索）。
