"""MCP 工具的 HTTP 转发层：纯函数、不依赖 mcp SDK，便于单测与复用。

业务逻辑全部留在 FastAPI 服务内，本层只做协议转发（对标 TDB-AM 的
stdio→HTTP 薄桥模式）。
"""
from __future__ import annotations

import httpx

DEFAULT_BASE_URL = "http://127.0.0.1:8000"
_TIMEOUT_SECONDS = 60.0


def query_knowledge(
    question: str,
    top_k: int = 5,
    *,
    base_url: str = DEFAULT_BASE_URL,
    client: httpx.Client | None = None,
) -> dict:
    """POST /query：检索 + 生成，返回 answer/contexts/sources。"""
    owned = client is None
    client = client or httpx.Client(timeout=_TIMEOUT_SECONDS)
    try:
        response = client.post(f"{base_url}/query", json={"question": question, "top_k": top_k})
        response.raise_for_status()
        return response.json()
    finally:
        if owned:
            client.close()


def knowledge_health(*, base_url: str = DEFAULT_BASE_URL, client: httpx.Client | None = None) -> dict:
    """GET /health：索引规模与模型配置状态。"""
    owned = client is None
    client = client or httpx.Client(timeout=_TIMEOUT_SECONDS)
    try:
        response = client.get(f"{base_url}/health")
        response.raise_for_status()
        return response.json()
    finally:
        if owned:
            client.close()


def format_query_result(payload: dict) -> str:
    """把 /query 响应整理为给 AI 工具读的纯文本（含引用与文档时间）。"""
    lines = [payload.get("answer") or "（无回答）"]
    contexts = payload.get("contexts") or []
    if contexts:
        lines.append("")
        lines.append("引用来源：")
        for index, context in enumerate(contexts, start=1):
            when = ""
            ingested_at = context.get("ingested_at")
            if isinstance(ingested_at, str) and ingested_at:
                when = f" · 文档时间 {ingested_at[:10]}"
            score = context.get("score")
            score_text = f"（score {score}）" if score is not None else ""
            lines.append(f"[{index}] {context.get('filename') or context.get('doc_id') or '未知来源'}{when}{score_text}")
    return "\n".join(lines)


def format_health(payload: dict) -> str:
    """把 /health 响应整理为纯文本。"""
    llm = payload.get("llm")
    llm_text = "已配置" if llm == "configured" else "未配置（纯检索模式）" if llm else str(llm)
    return (
        f"知识服务状态：索引 {payload.get('records', 0)} 条 · "
        f"Embedder {payload.get('embedder', '未知')} · LLM {llm_text}"
    )
