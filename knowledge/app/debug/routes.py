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
# 检索调试（多策略对比）
# ---------------------------------------------------------------------------

class DebugQueryRequest(BaseModel):
    question: str
    top_k: int = 5
    doc_ids: list[str] | None = None
    strategies: list[str] | None = None  # ["vector", "bm25", "hybrid"]，默认全部


def _ensure_bm25_index(services: AppServices) -> None:
    """懒加载 BM25 索引：首次调用时从向量库记录构建。"""
    if services.bm25.n_docs == 0:
        with services.vector_store.lock:
            records = list(services.vector_store.records)
        services.bm25.rebuild(records)


def _format_results(results: list[dict]) -> list[dict]:
    """统一格式化检索结果。"""
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
    return detailed


def _hybrid_fusion(
    vector_results: list[dict],
    bm25_results: list[dict],
    top_k: int,
    alpha: float = 0.6,
) -> list[dict]:
    """RRF（Reciprocal Rank Fusion）混合排序。alpha 为向量权重。"""
    k = 60  # RRF 常数
    scores: dict[str, float] = {}
    records_map: dict[str, dict] = {}

    for rank, record in enumerate(vector_results, start=1):
        rid = record.get("id", "")
        scores[rid] = scores.get(rid, 0.0) + alpha / (k + rank)
        records_map[rid] = record

    for rank, record in enumerate(bm25_results, start=1):
        rid = record.get("id", "")
        scores[rid] = scores.get(rid, 0.0) + (1 - alpha) / (k + rank)
        if rid not in records_map:
            records_map[rid] = record

    ranked = sorted(scores.items(), key=lambda x: x[1], reverse=True)[:top_k]
    results = []
    for rid, score in ranked:
        record = dict(records_map[rid])
        record["score"] = round(score, 6)
        results.append(record)
    return results


@router.post("/query")
async def debug_query(body: DebugQueryRequest, request: Request) -> dict:
    """检索调试：多策略并排对比（vector / bm25 / hybrid）。"""
    services = _guard(request)
    strategies = body.strategies or ["vector", "bm25", "hybrid"]

    # 向量检索
    vector_results = []
    if "vector" in strategies or "hybrid" in strategies:
        vector_results = await run_in_threadpool(
            services.retriever.retrieve, body.question, top_k=body.top_k * 2, doc_ids=body.doc_ids
        )

    # BM25 检索
    bm25_results = []
    if "bm25" in strategies or "hybrid" in strategies:
        await run_in_threadpool(_ensure_bm25_index, services)
        bm25_results = await run_in_threadpool(
            services.bm25.query, body.question, body.top_k * 2, body.doc_ids
        )

    # 组装各策略结果
    response: dict[str, Any] = {
        "question": body.question,
        "top_k": body.top_k,
        "strategies": {},
        "embedder": type(services.embedder).__name__,
    }

    if "vector" in strategies:
        response["strategies"]["vector"] = {
            "label": "向量检索（Milvus cosine）",
            "result_count": min(len(vector_results), body.top_k),
            "results": _format_results(vector_results[:body.top_k]),
        }

    if "bm25" in strategies:
        response["strategies"]["bm25"] = {
            "label": "关键词检索（BM25）",
            "result_count": min(len(bm25_results), body.top_k),
            "results": _format_results(bm25_results[:body.top_k]),
        }

    if "hybrid" in strategies:
        hybrid_results = _hybrid_fusion(vector_results, bm25_results, body.top_k)
        response["strategies"]["hybrid"] = {
            "label": "混合检索（RRF 融合）",
            "result_count": len(hybrid_results),
            "results": _format_results(hybrid_results),
        }

    if "reranked" in strategies:
        # 取混合结果（扩大范围）后用 Reranker 精排
        pool = _hybrid_fusion(vector_results, bm25_results, body.top_k * 3)
        if pool:
            documents = [r.get("text", "") for r in pool]
            rerank_scores = await run_in_threadpool(
                services.reranker.rerank, body.question, documents, body.top_k
            )
            reranked = []
            for item in rerank_scores:
                record = dict(pool[item["index"]])
                record["score"] = item["score"]
                reranked.append(record)
            response["strategies"]["reranked"] = {
                "label": "混合 + Reranker 精排",
                "result_count": len(reranked),
                "results": _format_results(reranked),
            }
        else:
            response["strategies"]["reranked"] = {
                "label": "混合 + Reranker 精排",
                "result_count": 0,
                "results": [],
            }

    # 记录查询日志（取混合或向量策略的结果）
    best_results = hybrid_results if "hybrid" in strategies else vector_results
    if best_results:
        services.query_log.log_retrieval(
            question=body.question,
            strategy="hybrid" if "hybrid" in strategies else "vector",
            top_k=body.top_k,
            result_count=len(best_results[:body.top_k]),
            top_score=best_results[0].get("score", 0.0) if best_results else 0.0,
            file_ids_hit=[r.get("file_id", "") for r in best_results[:body.top_k]],
        )

    return response


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

    # 记录生成日志
    faith_avg = (
        sum(f["evidence_score"] for f in faithfulness) / len(faithfulness)
        if faithfulness else 0.0
    )
    unsup_ratio = (
        sum(1 for f in faithfulness if f["verdict"] == "unsupported") / len(faithfulness)
        if faithfulness else 0.0
    )
    services.query_log.log_generation(
        question=body.question,
        strategy="vector",
        top_k=body.top_k,
        result_count=len(contexts),
        top_score=contexts[0].get("score", 0.0) if contexts else 0.0,
        file_ids_hit=[c.get("file_id", "") for c in contexts],
        answer_length=len(answer),
        faithfulness_avg=faith_avg,
        unsupported_ratio=unsup_ratio,
    )

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


# ---------------------------------------------------------------------------
# 知识健康度仪表盘
# ---------------------------------------------------------------------------

@router.get("/health")
async def knowledge_health(request: Request) -> dict:
    """知识健康度五维指标：规模、新鲜度、使用率、盲区、覆盖。"""
    services = _guard(request)

    # 从向量库获取所有记录
    with services.vector_store.lock:
        all_records = list(services.vector_store.records)

    # 基础规模
    doc_ids: set[str] = set()
    filenames: dict[str, str] = {}
    for record in all_records:
        did = record.get("doc_id", "")
        doc_ids.add(did)
        meta = record.get("metadata", {})
        if meta.get("filename"):
            filenames[did] = meta["filename"]

    total_chunks = len(all_records)
    total_docs = len(doc_ids)

    # 每文档 chunk 数分布
    chunks_per_doc: dict[str, int] = {}
    for record in all_records:
        did = record.get("doc_id", "")
        chunks_per_doc[did] = chunks_per_doc.get(did, 0) + 1

    # 新鲜度：从任务存储获取处理时间
    jobs = await run_in_threadpool(services.job_store.list_recent, 500)
    completed_jobs = [j for j in jobs if j.status == "completed"]
    processed_at_list = [j.finished_at for j in completed_jobs if j.finished_at]

    # 使用率与盲区：从查询日志获取真实数据
    log_stats = services.query_log.stats(days=30)

    # 覆盖：按文件扩展名分组
    ext_coverage: dict[str, int] = {}
    for fname in filenames.values():
        ext = fname.rsplit(".", 1)[-1].lower() if "." in fname else "unknown"
        ext_coverage[ext] = ext_coverage.get(ext, 0) + 1

    # 从未被命中的文档
    cited_ids = {item["file_id"] for item in log_stats["most_cited_docs"]}
    never_cited = [did for did in doc_ids if did not in cited_ids]

    return {
        "scale": {
            "total_documents": total_docs,
            "total_chunks": total_chunks,
            "avg_chunks_per_doc": round(total_chunks / max(total_docs, 1), 1),
            "documents": [
                {"file_id": did, "filename": filenames.get(did, ""), "chunks": chunks_per_doc.get(did, 0)}
                for did in sorted(doc_ids)
            ],
        },
        "freshness": {
            "total_processed": len(completed_jobs),
            "latest_processed_at": max(processed_at_list) if processed_at_list else None,
        },
        "coverage": {
            "by_extension": ext_coverage,
        },
        "usage": {
            "total_queries": log_stats["total_queries"],
            "recent_queries_30d": log_stats["recent_queries"],
            "most_cited_docs": log_stats["most_cited_docs"],
            "never_cited_docs": never_cited[:20],
            "generation": log_stats["generation"],
        },
        "blind_spots": {
            "unanswered_queries": log_stats["blind_spots"],
            "count": len(log_stats["blind_spots"]),
        },
    }
