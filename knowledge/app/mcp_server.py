"""MCP Server：把知识服务 HTTP API 暴露为 Model Context Protocol 工具。

stdio 传输 + HTTP 转发的薄桥（对标 TDB-AM MemoryKnowledge 的 MCP 接入模式）：
AI 工具（Claude Code / Cursor / 内部 OA 助手）以子进程方式拉起本模块，
工具调用转发到 FastAPI 服务的 /query 与 /health，业务逻辑零复制。

启动：python -m app.mcp_server（可选依赖 pip install mcp）
环境变量：KNOWLEDGE_BASE_URL 覆盖知识服务地址（默认 http://127.0.0.1:8000）
"""
from __future__ import annotations

import os

import anyio
import mcp.types as types
from mcp.server import Server
from mcp.server import stdio as stdio_transport

from app.mcp_tools import (
    DEFAULT_BASE_URL,
    format_health,
    format_query_result,
    knowledge_health,
    query_knowledge,
)

BASE_URL = os.environ.get("KNOWLEDGE_BASE_URL", DEFAULT_BASE_URL)

QUERY_SCHEMA = {
    "type": "object",
    "properties": {
        "question": {"type": "string", "description": "要检索的企业知识问题"},
        "top_k": {"type": "integer", "default": 5, "minimum": 1, "maximum": 100, "description": "引用片段数量"},
    },
    "required": ["question"],
}
HEALTH_SCHEMA = {"type": "object", "properties": {}}


def _text(content: str, *, is_error: bool = False) -> types.CallToolResult:
    return types.CallToolResult(
        content=[types.TextContent(type="text", text=content)],
        is_error=is_error,
    )


async def _list_tools(_ctx, _params) -> types.ListToolsResult:
    return types.ListToolsResult(
        tools=[
            types.Tool(
                name="knowledge_query",
                description="检索企业知识库并生成回答，返回答案与引用来源（含文档时间，可判断内容新旧）",
                input_schema=QUERY_SCHEMA,
            ),
            types.Tool(
                name="knowledge_health",
                description="查询知识服务状态：索引规模、Embedder 与 LLM 配置",
                input_schema=HEALTH_SCHEMA,
            ),
        ]
    )


async def _call_tool(_ctx, params: types.CallToolRequestParams) -> types.CallToolResult:
    name = params.name
    args = params.arguments or {}
    try:
        if name == "knowledge_query":
            question = str(args["question"])
            top_k = int(args.get("top_k", 5))
            # 同步 httpx 放线程池，不阻塞事件循环
            text = await anyio.to_thread.run_sync(
                lambda: format_query_result(query_knowledge(question, top_k, base_url=BASE_URL))
            )
        elif name == "knowledge_health":
            text = await anyio.to_thread.run_sync(lambda: format_health(knowledge_health(base_url=BASE_URL)))
        else:
            return _text(f"未知工具：{name}", is_error=True)
    except KeyError:
        return _text("缺少必填参数 question", is_error=True)
    except Exception as exc:  # 服务不可达等：错误作为工具结果返回，进程不崩
        return _text(f"知识服务调用失败：{exc}", is_error=True)
    return _text(text)


def build_server() -> Server:
    return Server("easyshare-knowledge", on_list_tools=_list_tools, on_call_tool=_call_tool)


async def serve() -> None:
    server = build_server()
    async with stdio_transport.stdio_server() as (read_stream, write_stream):
        await server.run(read_stream, write_stream, server.create_initialization_options())


def main() -> None:
    anyio.run(serve)


if __name__ == "__main__":
    main()
