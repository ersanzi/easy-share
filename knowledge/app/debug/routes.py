"""知识质量驾驶舱调试 API：单文档透视、检索调试、生成审计。

仅回环可访问，与 /lab 同策略。路径前缀 /debug，不进入 OpenAPI schema。
"""
from __future__ import annotations

import json
import re
from typing import Any

from fastapi import APIRouter, HTTPException, Request, status
from pydantic import BaseModel
from starlette.concurrency import run_in_threadpool

from app.services import AppServices

router = APIRouter(prefix="/debug", include_in_schema=False)
LOCAL_CLIENTS = {"127.0.0.1", "::1", "testclient"}


def _guard(request: Request) -> AppServices:
    services: AppServices = request.app.state.services
    if not services.config.local_lab_enabled:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "本地实验台未启用")
    client_host = request.client.host if request.client else ""
    if client_host not in LOCAL_CLIENTS:
        raise HTTPException(status.HTTP_403_FORBIDDEN, "仅允许回环地址访问")
    return services


# ---------------------------------------------------------------------------
# 文档列表
# ---------------------------------------------------------------------------

@router.get("/documents")
async def list_documents(request: Request) -> dict:
    """列出所有已入库文档（从任务存储中获取）。"""
    services = _guard(request)
    jobs = await run_in_threadpool(services.job_store.list_recent, 200)
    documents = []
    seen: set[str] = set()
    for job in jobs:
        if job.file_id in seen or job.status != "completed":
            continue
        seen.add(job.file_id)
        documents.append({
            "file_id": job.file_id,
            "version_id": job.version_id,
            "filename": job.filename,
            "status": job.status,
            "processed_at": job.finished_at,
        })
    return {"documents": documents}


# ---------------------------------------------------------------------------
# 单文档透视
# ---------------------------------------------------------------------------

@router.get("/document/{file_id}")
async def inspect_document(file_id: str, request: Request, version_id: str = "v1") -> dict:
    """返回文档的完整管线产物：结构化块、清洗后 Markdown、切块列表、manifest。"""
    services = _guard(request)
    pipeline = services.pipeline

    # 读取 manifest
    try:
        manifest = await run_in_threadpool(pipeline.read_manifest, file_id, version_id)
    except (KeyError, Exception):
        raise HTTPException(status.HTTP_404_NOT_FOUND, f"文档 {file_id} 未找到产物")

    # 读取 document.json（结构化块）
    try:
        doc_bytes, _ = await run_in_threadpool(pipeline.read_artifact, file_id, version_id, "document.json")
        document = json.loads(doc_bytes)
    except (KeyError, Exception):
        document = None

    # 读取 clean.md
    try:
        clean_bytes, _ = await run_in_threadpool(pipeline.read_artifact, file_id, version_id, "clean.md")
        clean_markdown = clean_bytes.decode("utf-8")
    except (KeyError, Exception):
        clean_markdown = ""

    # 从向量库获取切块
    chunks = await run_in_threadpool(services.vector_store.get_doc, file_id)
    chunk_summaries = []
    for chunk in chunks:
        meta = chunk.get("metadata", {})
        chunk_summaries.append({
            "id": chunk.get("id", ""),
            "text": chunk.get("text", ""),
            "char_count": len(chunk.get("text", "")),
            "block_ids": meta.get("block_ids", []),
            "source_locations": meta.get("source_locations", []),
            "extraction_methods": meta.get("extraction_methods", []),
        })

    return {
        "file_id": file_id,
        "version_id": version_id,
        "manifest": manifest,
        "document": document,
        "clean_markdown": clean_markdown,
        "chunks": chunk_summaries,
        "stats": {
            "blocks": manifest.get("blocks", 0),
            "characters": manifest.get("characters", 0),
            "chunks": manifest.get("chunks", 0),
            "ocr": manifest.get("ocr"),
            "cleaning": manifest.get("cleaning"),
        },
    }


# ---------------------------------------------------------------------------
# 检索调试
# ---------------------------------------------------------------------------

class DebugQueryRequest(BaseModel):
    question: str
    top_k: int = 5
    doc_ids: list[str] | None = None


@router.post("/query")
async def debug_query(body: DebugQueryRequest, request: Request) -> dict:
    """检索调试：返回详细的检索结果（含 score、metadata、chunk 全文）。"""
    services = _guard(request)

    results = await run_in_threadpool(
        services.retriever.retrieve, body.question, top_k=body.top_k, doc_ids=body.doc_ids
    )

    detailed = []
    for rank, record in enumerate(results, start=1):
        meta = record.get("metadata", {})
        detailed.append({
            "rank": rank,
            "score": round(record.get("score", 0.0), 4),
            "id": record.get("id", ""),
            "file_id": record.get("file_id", ""),
            "version_id": record.get("version_id", ""),
            "filename": meta.get("filename", ""),
            "text": record.get("text", ""),
            "char_count": len(record.get("text", "")),
            "block_ids": meta.get("block_ids", []),
            "source_locations": meta.get("source_locations", []),
            "extraction_methods": meta.get("extraction_methods", []),
        })

    return {
        "question": body.question,
        "strategy": "vector",
        "top_k": body.top_k,
        "result_count": len(detailed),
        "results": detailed,
        "embedder": type(services.embedder).__name__,
    }


# ---------------------------------------------------------------------------
# 生成审计
# ---------------------------------------------------------------------------

class DebugGenerateRequest(BaseModel):
    question: str
    top_k: int = 5
    doc_ids: list[str] | None = None


@router.post("/generate")
async def debug_generate(body: DebugGenerateRequest, request: Request) -> dict:
    """生成审计：返回完整 prompt、AI 回答、逐句忠实度分析。"""
    services = _guard(request)

    # 检索
    contexts = await run_in_threadpool(
        services.retriever.retrieve, body.question, top_k=body.top_k, doc_ids=body.doc_ids
    )

    if not contexts:
        return {
            "question": body.question,
            "answer": "",
            "faithfulness": [],
            "prompt": None,
            "contexts": [],
            "warning": "未检索到相关内容",
        }

    # 构建 prompt（复现 Generator 的逻辑）
    context_block = ""
    for i, ctx in enumerate(contexts, start=1):
        meta = ctx.get("metadata", {})
        source_label = meta.get("filename", ctx.get("file_id", "unknown"))
        context_block += f"[{i}] (来源: {source_label})\n{ctx.get('text', '')}\n\n"

    system_prompt = (
        "你是企业知识助手。只根据以下参考资料回答问题，不要使用参考资料之外的知识。"
        "回答中引用资料时标注 [编号]。如果参考资料不足以回答，明确说明。"
    )
    user_prompt = f"参考资料：\n{context_block}\n问题：{body.question}"

    # 生成
    if services.generator is None:
        answer = "（未配置 LLM，无法生成回答。以下为检索到的相关片段。）"
        faithfulness = []
    else:
        result = await run_in_threadpool(services.generator.generate, body.question, contexts)
        answer = result["answer"]
        faithfulness = _analyze_faithfulness(answer, contexts)

    # 构建 context 摘要
    context_summaries = []
    for i, ctx in enumerate(contexts, start=1):
        meta = ctx.get("metadata", {})
        context_summaries.append({
            "index": i,
            "file_id": ctx.get("file_id", ""),
            "filename": meta.get("filename", ""),
            "score": round(ctx.get("score", 0.0), 4),
            "text": ctx.get("text", ""),
            "source_locations": meta.get("source_locations", []),
        })

    return {
        "question": body.question,
        "answer": answer,
        "prompt": {"system": system_prompt, "user": user_prompt},
        "contexts": context_summaries,
        "faithfulness": faithfulness,
        "llm_configured": services.generator is not None,
    }


def _analyze_faithfulness(answer: str, contexts: list[dict]) -> list[dict]:
    """逐句忠实度分析：检查回答中每句是否在检索上下文中有依据。

    初版用关键实体/数值匹配，后续可升级为 NLI entailment。
    """
    # 按句拆分（中英文句号、问号、感叹号）
    sentences = re.split(r'(?<=[。！？.!?])\s*', answer)
    sentences = [s.strip() for s in sentences if s.strip() and len(s.strip()) > 2]

    # 合并所有 context 文本
    all_context_text = " ".join(ctx.get("text", "") for ctx in contexts)

    results = []
    for sentence in sentences:
        # 提取关键片段：数字、引号内容、2字以上中文词
        evidence_score = _compute_evidence_score(sentence, all_context_text)
        if evidence_score >= 0.6:
            verdict = "supported"
        elif evidence_score >= 0.3:
            verdict = "partial"
        else:
            verdict = "unsupported"

        results.append({
            "sentence": sentence,
            "verdict": verdict,
            "evidence_score": round(evidence_score, 2),
        })

    return results


def _compute_evidence_score(sentence: str, context_text: str) -> float:
    """计算句子在上下文中的证据覆盖度（0-1）。

    策略：提取句子中的关键 token（数字、英文词、2+ 中文字符片段），
    计算在 context 中命中的比例。
    """
    # 提取关键 token
    tokens: list[str] = []
    # 数字（含小数、百分比）
    tokens.extend(re.findall(r'\d+(?:\.\d+)?%?', sentence))
    # 英文词
    tokens.extend(re.findall(r'[a-zA-Z_]{2,}', sentence))
    # 中文 2-6 字词（简单滑窗）
    chinese_segments = re.findall(r'[\u4e00-\u9fff]{2,6}', sentence)
    tokens.extend(chinese_segments)

    if not tokens:
        return 0.5  # 无法判断时给中间值

    hits = sum(1 for token in tokens if token in context_text)
    return hits / len(tokens)
