# MCP Server 暴露：知识库成为任何 AI 工具可调用的服务

> 里程碑 1.9 第三切片（收官）。设计引用 TDB-AM 对标报告 §5.5（stdio→HTTP 薄桥、tools 两步自发现）；knowledge-platform.md §1.2「MCP 基础设施」愿景的第一步。
> 开工：2026-08-20。

## 用户问题

知识库目前只能被自家 /lab 和驾驶舱消费。愿景是企业知识的 API 层——任何 AI 工具（Claude、Cursor、内部 OA 助手）都可调用。MCP 是当前事实标准。

## 目标

- stdio MCP Server（`python -m app.mcp_server`）：AI 工具以子进程方式拉起，转发到知识服务 HTTP API（薄桥，业务逻辑零复制）。
- 初始工具集两个：`knowledge_query`（检索+生成，含引用与文档时间）、`knowledge_health`（服务状态/索引规模）。
- `mcp` SDK 为可选依赖（同 pymupdf/pdf-inspector 待遇），缺失时给出明确安装指引。

## 非目标

- 不做 Streamable HTTP 挂载（主服务内嵌 MCP 端点，后续按需）。
- 不做写类工具（入库/删除）——先读后写，权限模型等里程碑 2。
- 不改 FastAPI 服务本体（MCP 层纯消费现有 /query、/health）。

## 设计决策

- **mcp SDK 2.0 官方低层 API**（`Server(on_list_tools=…, on_call_tool=…)` 构造注入 + `stdio_server`）：FastMCP 已从官方 SDK 拆分为独立框架，不引入；薄桥层不需要框架。
- 分层：`app/mcp_tools.py`（纯 httpx 转发 + 格式化，无 SDK 依赖可单测）与 `app/mcp_server.py`（协议层）分离；httpx 同步调用走线程池不阻塞事件循环。
- 工具调用失败（服务不可达/超时）**作为 is_error 工具结果返回**，进程不崩——AI 工具能看到错误信息并决定重试。
- `mcp` 为可选依赖；知识服务本体零改动。

## 完成记录

### 已完成（2026-08-20）

- `app/mcp_tools.py`：query_knowledge / knowledge_health / 两个格式化函数（引用含文档时间）。
- `app/mcp_server.py`：stdio Server，工具 `knowledge_query` + `knowledge_health`，`python -m app.mcp_server` 启动，`KNOWLEDGE_BASE_URL` 覆盖地址。
- `knowledge/README.md` 新增 MCP Server 章节（安装、工具集、客户端配置示例、env 透传坑）。
- 测试：新增 `tests/test_mcp_tools.py` 7 条（MockTransport 转发/格式化 + SDK 冒烟：工具清单/未知工具/缺参/服务不可达降级）；全量 **109 passed, 1 skipped**。
- **真实端到端冒烟通过**：官方 mcp 客户端拉起 Server → 握手 → tools/list → knowledge_health 返回真实状态（21 条索引）→ knowledge_query 返回真实 RAG 回答带引用（开发机 .env 配置了真实 Embedding/LLM）。

### 已知限制与后续工作

- 仅读类工具；写类（入库/删除）等里程碑 2 权限模型。
- Streamable HTTP 挂载（主服务内嵌 /mcp 端点）按需再加，当前 stdio 覆盖主流客户端。
- SDK 2.0 较新，API 契约关注 upstream 变化。
