# Knowledge Service（知识平台计算面）

EasyShare 知识平台的 **Python 计算面服务**，负责文档解析、知识库构建（RAG）与问答生成。
Java 控制面（账号/权限/编排）后续接入，本服务先跑通"文档 → 知识 → 问答"核心闭环。

总体方向见 [`../docs/knowledge-platform.md`](../docs/knowledge-platform.md)。

## 管线

```
RustFS/文本 → 解析(extractor) → 切块(chunker) → 向量化(embedder) → 向量库(store)
                                                                        ↓
                              问答 ← 生成(generator/LLM) ← 检索(retriever)
```

## 快速开始

```bash
cd knowledge

# 1. 建虚拟环境并装依赖
python -m venv .venv
.venv\Scripts\activate          # Windows
pip install -r requirements.txt

# 2. 配置环境变量
cp .env.example .env            # 然后按需填写

# 3. 启动
uvicorn app.main:app --reload
# 交互式文档: http://127.0.0.1:8000/docs
```

## 配置说明（.env）

所有连接参数走环境变量，**凭证不写死在代码里**。

| 变量 | 说明 | 必填 |
| --- | --- | --- |
| `RUSTFS_ENDPOINT` / `RUSTFS_ACCESS_KEY` / `RUSTFS_SECRET_KEY` / `RUSTFS_BUCKET` | RustFS（S3 兼容）连接，凭证取自 `deploy/rustfs/.env` | 从 RustFS 入库时必填 |
| `LLM_BASE_URL` / `LLM_API_KEY` / `LLM_MODEL` | LLM（OpenAI 兼容）。留空则 `/query` 只返回检索片段不生成 | 否（留空可跑通检索） |
| `EMBEDDING_BASE_URL` / `EMBEDDING_API_KEY` / `EMBEDDING_MODEL` | Embedding（OpenAI 兼容）。留空退回 HashEmbedder | 否（留空可跑通管线） |
| `EMBEDDING_DIM` | 向量维度，需与所选 embedding 模型一致 | 否（默认 1024） |
| `CHUNK_SIZE` / `CHUNK_OVERLAP` | 切块大小与重叠 | 否 |
| `VECTOR_STORE_PATH` | 向量库 JSON 持久化路径 | 否 |

> **关于 embedding**：真实语义检索需要一个支持 embedding 的 OpenAI 兼容端点。
> 未配置时服务用 HashEmbedder 占位，可跑通整条管线但**无语义能力**——仅用于验证流程，
> 接入真实 embedding 后自动切换，代码无需改动。

## 接口

### `GET /health`
返回服务状态、所用 embedder、LLM 是否配置、向量库记录数。

### `POST /ingest` — 入库一份文档
```json
{ "source": "text", "filename": "公司制度.md", "content": "……全文……" }
```
或从 RustFS 读取：
```json
{ "source": "rustfs", "key": "公司制度.docx" }
```
返回 `{ doc_id, filename, chunks, chars }`。

### `POST /query` — 提问
```json
{ "question": "请假流程是怎样的？", "top_k": 5, "doc_ids": null }
```
- `doc_ids`：权限范围过滤，由 Java 控制面算出"该用户可访问的文档 ID"后传入（权限感知检索）。
- 返回 `{ answer, sources, contexts }`，`contexts` 是检索到的原始片段（便于核对溯源）。

## 端到端验证（不依赖任何外部服务）

即使不配置 LLM 和 embedding，也能用 HashEmbedder 跑通管线、验证检索：

```bash
# 入库一段文本
curl -X POST http://127.0.0.1:8000/ingest -H "Content-Type: application/json" \
  -d "{\"source\":\"text\",\"filename\":\"note.txt\",\"doc_id\":\"doc1\",\"content\":\"EasyShare 是一个文件传输工具。它支持局域网传输和云盘上传。\"}"

# 提问（未配 LLM 时返回检索片段）
curl -X POST http://127.0.0.1:8000/query -H "Content-Type: application/json" \
  -d "{\"question\":\"EasyShare 支持什么功能？\",\"doc_ids\":[\"doc1\"]}"
```

配置真实 embedding + LLM 后，同样的请求即返回基于文档的语义回答。

## 目录结构

```
app/
  main.py            # FastAPI 入口
  config.py          # 环境变量配置
  api/               # /ingest /query /health
  parsing/           # 文档 → 文本（txt/md/docx/pdf）
  kb/                # 切块 / 向量化(可替换) / 向量库
  rag/               # 检索 / 生成
  storage/           # 从 RustFS 读文件
```

## 设计取舍（骨架阶段）

- **向量库**用内存 + numpy + JSON 持久化，最薄实现；里程碑 1 换 Chroma/Milvus，接口不变。
- **embedding/LLM** 抽象为可替换网关，现在接云端，以后可换本地模型。
- **权限**目前靠 `doc_ids` 过滤参数预留，真正的权限裁决在 Java 控制面（里程碑 2）。
- 解析仅支持 txt/md/docx/pdf，更多格式在里程碑 1 扩展。
