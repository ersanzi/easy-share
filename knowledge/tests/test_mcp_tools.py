from __future__ import annotations

import httpx
import pytest

from app.mcp_tools import (
    format_health,
    format_query_result,
    knowledge_health,
    query_knowledge,
)


def _mock_client(handler) -> httpx.Client:
    return httpx.Client(transport=httpx.MockTransport(handler))


# ---------- HTTP 转发层（不依赖 mcp SDK） ----------


def test_query_knowledge_posts_question() -> None:
    captured: dict = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["url"] = str(request.url)
        captured["body"] = request.read()
        return httpx.Response(200, json={"answer": "答案", "contexts": []})

    with _mock_client(handler) as client:
        payload = query_knowledge("报销标准", top_k=3, base_url="http://svc", client=client)

    assert captured["url"] == "http://svc/query"
    assert b'"question"' in captured["body"]
    assert payload["answer"] == "答案"


def test_knowledge_health_gets_status() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        assert str(request.url) == "http://svc/health"
        return httpx.Response(200, json={"records": 42, "embedder": "OpenAIEmbedder", "llm": "configured"})

    with _mock_client(handler) as client:
        payload = knowledge_health(base_url="http://svc", client=client)

    assert payload["records"] == 42


def test_format_query_result_includes_citations_and_time() -> None:
    text = format_query_result({
        "answer": "单笔上限五千元。",
        "contexts": [
            {"filename": "报销制度.pdf", "ingested_at": "2026-08-01T10:00:00+00:00", "score": 0.83},
            {"filename": "旧版制度.pdf", "doc_id": "d2"},
        ],
    })

    assert "单笔上限五千元。" in text
    assert "[1] 报销制度.pdf · 文档时间 2026-08-01（score 0.83）" in text
    assert "[2] 旧版制度.pdf" in text
    assert "文档时间" not in text.split("[2]")[1]  # 无时间不标注


def test_format_health_summary() -> None:
    assert "42 条" in format_health({"records": 42, "embedder": "OpenAIEmbedder", "llm": "configured"})
    assert "纯检索模式" in format_health({"records": 0, "embedder": "HashEmbedder", "llm": "unconfigured"})


# ---------- MCP Server（SDK 存在时冒烟） ----------

mcp_sdk = pytest.importorskip("mcp.server", reason="mcp SDK 未安装")


def test_server_lists_two_tools() -> None:
    import anyio

    from app.mcp_server import _list_tools

    async def run() -> list[str]:
        result = await _list_tools(None, None)
        return [tool.name for tool in result.tools]

    assert set(anyio.run(run)) == {"knowledge_query", "knowledge_health"}


def test_call_tool_unknown_and_missing_args() -> None:
    import anyio
    import mcp.types as types

    from app.mcp_server import _call_tool

    async def call(name: str, args: dict | None):
        return await _call_tool(
            None,
            types.CallToolRequestParams(name=name, arguments=args),
        )

    unknown = anyio.run(call, "nope", {})
    assert unknown.is_error
    missing = anyio.run(call, "knowledge_query", {})
    assert missing.is_error and "question" in missing.content[0].text


def test_call_tool_service_unreachable_returns_error_result() -> None:
    import anyio
    import mcp.types as types

    from app.mcp_server import _call_tool

    async def call():
        return await _call_tool(
            None,
            types.CallToolRequestParams(name="knowledge_health", arguments={}),
        )

    # BASE_URL 指向必然未监听的端口：错误应作为工具结果返回而非抛异常
    import app.mcp_server as server_module

    original = server_module.BASE_URL
    server_module.BASE_URL = "http://127.0.0.1:9"
    try:
        result = anyio.run(call)
    finally:
        server_module.BASE_URL = original

    assert result.is_error
    assert "调用失败" in result.content[0].text
