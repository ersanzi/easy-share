# 知识平台计算面服务 — 测试清单

> 本文用于手工验证 `knowledge/` Python AI 服务的端到端流程。
> 服务架构与接口见 [`../knowledge-platform.md`](../knowledge-platform.md) 和 [`../../knowledge/README.md`](../../knowledge/README.md)。

## 0. 准备

```bash
cd knowledge

# 虚拟环境与依赖（首次）
python -m venv .venv
.venv\Scripts\activate
pip install -r requirements.txt

# 配置
cp .env.example .env
```

启动服务（保持窗口开着）：

```bash
uvicorn app.main:app --reload
```

看到 `Uvicorn running on http://127.0.0.1:8000` 即成功。交互式文档在 http://127.0.0.1:8000/docs 。

---

## 场景 1：零配置跑通管线（不需要任何外部服务）

目的：验证"入库 → 切块 → 向量化 → 检索"整条管线连通。此时用 HashEmbedder 占位、无 LLM，**无语义能力**，只证明流程通。

**1.1 健康检查**

```bash
curl http://127.0.0.1:8000/health
```

预期：`{"status":"ok","embedder":"HashEmbedder","llm":"absent","records":0}`

**1.2 入库一段文本**

```bash
curl -X POST http://127.0.0.1:8000/ingest -H "Content-Type: application/json" -d "{\"source\":\"text\",\"filename\":\"company.md\",\"doc_id\":\"doc1\",\"content\":\"EasyShare 是一个面向 Windows 的文件传输与云盘工具。它支持局域网设备发现和 TCP 流式传输。网盘功能基于 RustFS 对象存储。请假流程：员工需提前一天在系统提交申请，主管审批后生效。报销需在事项发生后七天内提交发票。\"}"
```

预期：`{"doc_id":"doc1","filename":"company.md","chunks":1,"chars":...}`

**1.3 提问（返回检索片段）**

```bash
curl -X POST http://127.0.0.1:8000/query -H "Content-Type: application/json" -d "{\"question\":\"请假流程是怎样的？\",\"doc_ids\":[\"doc1\"]}"
```

预期：`answer` 为"（未配置 LLM，以下为检索到的相关片段）"，`contexts` 里能看到含"请假流程"的原文片段。

**1.4 权限过滤验证**

```bash
curl -X POST http://127.0.0.1:8000/query -H "Content-Type: application/json" -d "{\"question\":\"请假流程\",\"doc_ids\":[\"不存在的文档\"]}"
```

预期：`contexts` 为空数组，`answer` 为"知识库中没有找到与该问题相关的内容。"——证明 `doc_ids` 权限过滤生效。

---

## 场景 2：配置真实 embedding + LLM（验证语义问答）

目的：验证接入真实服务后，能基于文档做语义检索并生成可溯源回答。

**2.1 在 `.env` 填写**

```
EMBEDDING_BASE_URL=<你的 embedding 端点>
EMBEDDING_API_KEY=<密钥>
EMBEDDING_MODEL=<embedding 模型名>
EMBEDDING_DIM=<该模型向量维度，如 1024/1536>

LLM_BASE_URL=<你的 LLM 端点>
LLM_API_KEY=<密钥>
LLM_MODEL=<对话模型名>
```

> 注意：`EMBEDDING_DIM` 必须与所选 embedding 模型实际输出维度一致。
> embedding 与 LLM 可以是不同提供方，只要是 OpenAI 兼容接口即可。

**2.2 重启服务**，健康检查应变为：

```bash
curl http://127.0.0.1:8000/health
```

预期：`embedder` 为 `OpenAIEmbedder`，`llm` 为 `configured`。

**2.3 重新入库**（向量维度变了，旧数据需清掉重建）

删除旧向量库后重新执行场景 1.2 入库：

```bash
rm data/vector_store.json
# 再执行 1.2 的入库命令
```

**2.4 语义提问**

```bash
curl -X POST http://127.0.0.1:8000/query -H "Content-Type: application/json" -d "{\"question\":\"员工请假要怎么操作？\",\"doc_ids\":[\"doc1\"]}"
```

预期：`answer` 是基于文档生成的自然语言回答（如"员工需提前一天在系统提交申请，经主管审批后生效[n]"），`sources` 标注引用来源。即使提问用词与原文不同（"请假要怎么操作" vs "请假流程"），语义检索也应命中。

---

## 场景 3：从 RustFS 入库真实文件（docx / pdf）

目的：验证解析层能从对象存储读取并解析真实文档。

**3.1 配置 RustFS**（`.env`，凭证取自 `deploy/rustfs/.env`）

```
RUSTFS_ENDPOINT=http://127.0.0.1:9000
RUSTFS_ACCESS_KEY=<凭证>
RUSTFS_SECRET_KEY=<密钥>
RUSTFS_BUCKET=easyshare
```

确保 RustFS 已启动且 bucket 中有文件（可先用 EasyShare 网盘上传一份 .docx 或 .pdf）。

**3.2 按对象键入库**

```bash
curl -X POST http://127.0.0.1:8000/ingest -H "Content-Type: application/json" -d "{\"source\":\"rustfs\",\"key\":\"你的文件.docx\"}"
```

预期：返回 `chunks > 0`、`chars > 0`，说明 docx/pdf 被成功解析成文本并切块。

**3.3 用样例 Markdown 文件入库**（无需 RustFS）

仓库附带样例文件 `knowledge/samples/sample.md`：

```bash
curl -X POST http://127.0.0.1:8000/ingest -H "Content-Type: application/json" -d "{\"source\":\"text\",\"filename\":\"sample.md\",\"doc_id\":\"sample\",\"content\":\"$(cat samples/sample.md)\"}"
```

> Windows cmd 下 `$(cat ...)` 不可用时，可直接把 sample.md 内容粘进 content 字段，或用场景 1 的内联文本。

---

## 场景 4：多文档与溯源

**4.1 入库第二份文档**（不同 doc_id）

```bash
curl -X POST http://127.0.0.1:8000/ingest -H "Content-Type: application/json" -d "{\"source\":\"text\",\"filename\":\"finance.md\",\"doc_id\":\"doc2\",\"content\":\"财务报销制度：差旅费需附行程单与发票，餐费每日上限一百元，超出部分自理。\"}"
```

**4.2 跨文档提问**（不传 doc_ids，全库检索）

```bash
curl -X POST http://127.0.0.1:8000/query -H "Content-Type: application/json" -d "{\"question\":\"出差吃饭能报多少？\"}"
```

预期（配 LLM 后）：回答来自 doc2 的餐费上限内容，`sources`/`contexts` 显示 `doc_id: doc2`，证明溯源正确、未张冠李戴。

---

## 常见问题排障

| 现象 | 原因与解法 |
| --- | --- |
| 启动报 `ModuleNotFoundError` | 没激活虚拟环境或没装依赖。`.venv\Scripts\activate` 后 `pip install -r requirements.txt` |
| `/ingest` 报"文档解析后无可用文本" | 文件是扫描件 PDF（无文本层）或空文件。骨架不含 OCR，换有文本层的文档 |
| 配了 embedding 但 `health` 仍显示 HashEmbedder | 三个变量 `EMBEDDING_BASE_URL/API_KEY/MODEL` 必须都填，缺一即退回占位 |
| 配了 LLM 但回答仍是"以下为检索片段" | 同上，`LLM_BASE_URL/API_KEY/MODEL` 三者都要填；改 `.env` 后须重启服务 |
| 语义检索命中不准 | 检查 `EMBEDDING_DIM` 是否与模型实际维度一致；维度不符会显著影响相似度 |
| 换 embedding 模型后查询报错或结果异常 | 向量维度变了，须 `rm data/vector_store.json` 清空后重新入库 |
| RustFS 入库报签名/连接错误 | 核对 `RUSTFS_ENDPOINT` 与凭证；确认 RustFS 已启动、bucket 存在、对象键正确 |
| 重启服务后记录还在 | 正常，向量库持久化在 `data/vector_store.json`；想从零开始就删掉它 |

## 验收要点

- [ ] 场景 1 全部通过（零配置管线连通 + 权限过滤生效）
- [ ] 场景 2 语义问答可用（换词提问仍能命中，回答带溯源）
- [ ] 场景 3 至少一种真实格式（docx 或 pdf）解析入库成功
- [ ] 场景 4 多文档溯源正确
