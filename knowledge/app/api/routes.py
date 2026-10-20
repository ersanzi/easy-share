"""知识服务 API：异步文档处理、产物读取、兼容入库和检索。"""
from __future__ import annotations

import json
import logging
import uuid
from pathlib import PurePosixPath
from urllib.parse import quote

from botocore.exceptions import ClientError
from fastapi import APIRouter, HTTPException, Request, Response, status

from app.api.schemas import (
    ArtifactManifestResponse,
    DocumentListResponse,
    IngestRequest,
    IngestResponse,
    ProcessDocumentRequest,
    ProcessingJobResponse,
    QueryRequest,
    QueryResponse,
    RetrievedChunk,
)
from app.kb.chunker import chunk_text
from app.parsing.extractor import extract_text
from app.parsing.rules import load_rules
from app.rag.permissions import can_view_document, effective_doc_ids, visible_doc_ids
from app.services import AppServices

logger = logging.getLogger(__name__)
router = APIRouter()


def _services(request: Request) -> AppServices:
    return request.app.state.services


def _job_response(job) -> ProcessingJobResponse:
    return ProcessingJobResponse.model_validate(job.to_dict())


def _is_missing_object(exc: ClientError) -> bool:
    code = str(exc.response.get("Error", {}).get("Code", ""))
    return code in {"404", "NoSuchKey", "NoSuchObject", "NotFound"}


def _visible_manifest(request: Request, file_id: str, version_id: str) -> dict:
    """读取 manifest 并校验当前登录用户的文档可见性。

    产物体与检索走同一套归属/部门语义；未登录（本地 lab、auth 关闭）保持
    历史行为不过滤。
    """
    services = _services(request)
    try:
        manifest = services.pipeline.read_manifest(file_id, version_id)
    except FileNotFoundError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "派生产物不存在") from exc
    except ClientError as exc:
        if _is_missing_object(exc):
            raise HTTPException(status.HTTP_404_NOT_FOUND, "派生产物不存在") from exc
        raise
    if not can_view_document(manifest, getattr(request.state, "user", None)):
        raise HTTPException(status.HTTP_403_FORBIDDEN, "无权查看该文档")
    return manifest


@router.get("/stats")
def stats(request: Request, days: int = 30) -> dict:
    """管理员汇总页的聚合统计（QueryLog + 文档规模）。

    刻意只出聚合计数与文档 ID 热度，不返回盲区问题明细——问题文本的可见性
    沿用驾驶舱口径（回环 only），这里的数字走桌面端管理页（可能跨网段）。
    """
    services = _services(request)
    days = max(1, min(days, 90))
    summary = services.query_log.stats(days=days)
    return {
        "days": days,
        "total_queries": summary["total_queries"],
        "recent_queries": summary["recent_queries"],
        "blind_spot_count": len(summary["blind_spots"]),
        "generation": summary["generation"],
        "most_cited_docs": summary["most_cited_docs"],
        "documents": services.vector_store.count(),
        "llm": "configured" if services.generator else "absent",
    }


@router.get("/health")
def health(request: Request) -> dict:
    services = _services(request)
    asr_capability = _asr_capability(services)
    return {
        "status": "ok",
        "embedder": type(services.embedder).__name__,
        "llm": "configured" if services.generator else "absent",
        "auth": bool(services.config.auth_enabled),
        "watch_dirs": len(services.watcher.directories) if services.watcher else 0,
        "ocr": services.ocr.capability().to_dict() if services.ocr else {"available": False, "provider": "unknown", "reason": "OCR 服务未配置"},
        "asr": asr_capability,
        "records": services.vector_store.count(),
        # 文档数与索引块数分开：面板上的“多少份文档”过去误用了 records（块数）
        "documents": len(services.vector_store.doc_owners()),
        "jobs": services.job_store.counts(),
    }


def _asr_capability(services: AppServices) -> dict:
    """/health 声明听记能力（照 OCR 模式）；funasr 探测不真正 import（避免拉起 torch）。"""
    from app.meetings.engine import funasr_importable

    if services.meetings is None:
        return {"available": False, "provider": "funasr", "reason": "未启用（ASR_ENABLED=false）"}
    if not funasr_importable():
        return {"available": False, "provider": "funasr", "reason": "funasr 未安装（requirements-asr.txt）"}
    return {"available": True, "provider": "funasr", "reason": ""}


@router.get("/documents", response_model=DocumentListResponse)
def list_documents(request: Request, limit: int = 200) -> dict:
    """知识库文档清单：最新任务状态 + 最近一次成功版本。

    与 /query 同一套可见性判据；ready 与 status 分开，更新失败时仍能读上一版。
    """
    services = _services(request)
    user = getattr(request.state, "user", None)
    documents = []
    for jobs in services.job_store.list_documents(limit=limit):
        latest = jobs[0]
        if not can_view_document({"owner": latest.owner, "visible_depts": latest.visible_depts}, user):
            continue
        last_completed = next((job for job in jobs if job.status == "completed"), None)
        result = (last_completed.result if last_completed else None) or {}
        documents.append({
            "job_id": latest.id,
            "file_id": latest.file_id,
            "version_id": latest.version_id,
            "filename": latest.filename,
            "status": latest.status,
            "stage": latest.stage,
            "progress": latest.progress,
            "error_code": latest.error_code,
            "error_message": latest.error_message,
            "owner": latest.owner,
            "updated_at": latest.updated_at,
            "ready": last_completed is not None,
            "read_version_id": last_completed.version_id if last_completed else None,
            "chunks": int(result.get("chunks") or 0),
            "characters": int(result.get("characters") or 0),
            "processed_at": result.get("processed_at"),
            "source_bytes": int(result.get("source_bytes") or 0),
        })
    return {"documents": documents}


@router.post(
    "/documents/process",
    response_model=ProcessingJobResponse,
    status_code=status.HTTP_202_ACCEPTED,
)
def process_document(req: ProcessDocumentRequest, request: Request) -> ProcessingJobResponse:
    services = _services(request)
    filename = req.filename or PurePosixPath(req.object_key.replace("\\", "/")).name
    if not filename:
        raise HTTPException(status.HTTP_422_UNPROCESSABLE_ENTITY, "无法从 object_key 推断文件名")

    # 2b 文件归属：带令牌时以令牌用户为准（防伪造他人归属）；仅无令牌的
    # 内部/测试调用可显式指定 owner；两者皆无为共享文档（None）
    token_user = getattr(request.state, "user", None)
    owner = (token_user or {}).get("username") or req.owner
    if token_user and req.owner and req.owner != owner:
        logger.info("入库归属以令牌用户为准: token=%s 请求 owner=%s 被忽略", owner, req.owner)

    job, created = services.job_store.create_or_get(
        file_id=req.file_id,
        version_id=req.version_id,
        object_key=req.object_key,
        filename=filename,
        owner=owner,
        # 声明者即归属者（owner 已被令牌锁定），部门串归一为逗号分隔
        visible_depts=",".join(req.visible_depts) if req.visible_depts else None,
        force=req.force,
    )
    if created or job.status == "queued":
        services.job_runner.submit(job.id)
    return _job_response(job)


@router.get("/jobs/{job_id}", response_model=ProcessingJobResponse)
def get_job(job_id: str, request: Request) -> ProcessingJobResponse:
    try:
        job = _services(request).job_store.get(job_id)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "任务不存在") from exc
    return _job_response(job)


@router.post("/jobs/{job_id}/retry", response_model=ProcessingJobResponse, status_code=202)
def retry_job(job_id: str, request: Request) -> ProcessingJobResponse:
    services = _services(request)
    try:
        services.job_store.get(job_id)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "任务不存在") from exc
    try:
        job = services.job_runner.retry(job_id)
    except ValueError as exc:
        raise HTTPException(status.HTTP_409_CONFLICT, str(exc)) from exc
    return _job_response(job)


@router.get(
    "/documents/{file_id}/versions/{version_id}/artifacts",
    response_model=ArtifactManifestResponse,
)
def get_artifact_manifest(file_id: str, version_id: str, request: Request) -> dict:
    return _visible_manifest(request, file_id, version_id)


@router.get("/documents/{file_id}/versions/{version_id}/view")
def get_document_view(file_id: str, version_id: str, request: Request) -> dict:
    """阅读页数据：manifest + 结构化正文（document.json）。

    正文按块返回，块内自带 source（页码/工作表/幻灯片/段落），前端据此做
    引用定位与高亮，不需要再解析 Markdown。
    """
    manifest = _visible_manifest(request, file_id, version_id)
    try:
        content, _ = _services(request).pipeline.read_artifact(file_id, version_id, "document.json")
    except (KeyError, FileNotFoundError) as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "正文产物不存在") from exc
    except ClientError as exc:
        if _is_missing_object(exc):
            raise HTTPException(status.HTTP_404_NOT_FOUND, "正文产物不存在") from exc
        raise
    return {"manifest": manifest, "document": json.loads(content.decode("utf-8"))}


@router.get("/documents/{file_id}/versions/{version_id}/source")
def get_document_source(file_id: str, version_id: str, request: Request) -> Response:
    """下载入库原件；可见性判据与阅读页一致。"""
    services = _services(request)
    manifest = _visible_manifest(request, file_id, version_id)
    object_key = manifest.get("object_key")
    if not object_key:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "原件不存在")
    try:
        content = services.storage.read(object_key, max_bytes=services.config.max_source_bytes)
    except FileNotFoundError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "原件不存在") from exc
    except ValueError as exc:
        # max_source_bytes 超限（RustFSStorage/FakeStorage 都抛 ValueError）
        raise HTTPException(status.HTTP_413_REQUEST_ENTITY_TOO_LARGE, str(exc)) from exc
    except ClientError as exc:
        if _is_missing_object(exc):
            raise HTTPException(status.HTTP_404_NOT_FOUND, "原件不存在") from exc
        raise
    filename = manifest.get("filename") or f"{file_id}.bin"
    media_type = manifest.get("media_type") or "application/octet-stream"
    return Response(
        content=content,
        media_type=media_type,
        headers={"Content-Disposition": f"attachment; filename*=UTF-8''{quote(filename, safe='')}"},
    )


@router.get("/documents/{file_id}/versions/{version_id}/artifacts/{name}")
def get_artifact(file_id: str, version_id: str, name: str, request: Request) -> Response:
    _visible_manifest(request, file_id, version_id)
    try:
        content, content_type = _services(request).pipeline.read_artifact(file_id, version_id, name)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "不支持的派生产物名称") from exc
    except FileNotFoundError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "派生产物不存在") from exc
    except ClientError as exc:
        if _is_missing_object(exc):
            raise HTTPException(status.HTTP_404_NOT_FOUND, "派生产物不存在") from exc
        raise
    return Response(content=content, media_type=content_type)


@router.get("/cleaning/rules")
def cleaning_rules(request: Request) -> dict:
    """当前生效的清洗规则集（只读）。规则治理（按租户配置、审计）属于里程碑 2 的
    Java 控制面；当前通过 data/cleaning_rules.json 覆盖内置默认。"""
    services = _services(request)
    engine = load_rules(services.config.cleaning_rules_path)
    return {
        "source": services.config.cleaning_rules_path,
        "rules": [rule.to_dict() for rule in engine.rules],
        "warnings": engine.load_warnings,
    }


@router.post("/ingest", response_model=IngestResponse)
def ingest(req: IngestRequest, request: Request) -> IngestResponse:
    """保留旧同步接口供手工验证；正式文件管线使用 /documents/process。"""
    services = _services(request)
    doc_id = req.doc_id or str(uuid.uuid4())

    if req.source == "text":
        if not req.content:
            raise HTTPException(status.HTTP_400_BAD_REQUEST, "source=text 需要 content")
        filename = req.filename or f"{doc_id}.txt"
        text = req.content
    else:
        if not req.key:
            raise HTTPException(status.HTTP_400_BAD_REQUEST, "source=rustfs 需要 key")
        filename = req.filename or PurePosixPath(req.key.replace("\\", "/")).name
        content = services.storage.read(req.key, max_bytes=services.config.max_source_bytes)
        text = extract_text(filename, content)

    chunks = chunk_text(text, services.config.chunk_size, services.config.chunk_overlap)
    if not chunks:
        raise HTTPException(status.HTTP_422_UNPROCESSABLE_ENTITY, "文档解析后无可用文本")

    embeddings = services.embedder.embed(chunks)
    if len(embeddings) != len(chunks):
        raise HTTPException(status.HTTP_502_BAD_GATEWAY, "Embedding 返回数量不正确")
    items = [
        {
            "id": f"{doc_id}:{index}",
            "doc_id": doc_id,
            "text": chunk,
            "metadata": {"filename": filename},
            "embedding": embedding,
        }
        for index, (chunk, embedding) in enumerate(zip(chunks, embeddings))
    ]
    services.vector_store.replace_doc(doc_id, items)
    logger.info("入库完成: doc_id=%s filename=%s chunks=%d", doc_id, filename, len(chunks))
    return IngestResponse(doc_id=doc_id, filename=filename, chunks=len(chunks), chars=len(text))


@router.post("/query", response_model=QueryResponse)
def query(req: QueryRequest, request: Request) -> QueryResponse:
    services = _services(request)
    # 2c 权限感知：从请求令牌解析用户，服务端裁剪可见文档集合；
    # 与显式 doc_ids 求交集，交集为空时短路（不进检索层，避免空列表被当作不过滤）
    user = getattr(request.state, "user", None)
    doc_ids = effective_doc_ids(req.doc_ids, visible_doc_ids(services.vector_store, user))
    outcome = (
        services.orchestrator.retrieve(req.question, top_k=req.top_k, doc_ids=doc_ids)
        if doc_ids != []
        else None
    )
    contexts = outcome.contexts if outcome else []
    chunks = [
        RetrievedChunk(
            doc_id=context.get("doc_id"),
            file_id=context.get("file_id"),
            version_id=context.get("version_id"),
            filename=(context.get("metadata") or {}).get("filename"),
            score=context.get("score"),
            ingested_at=(context.get("metadata") or {}).get("ingested_at"),
            text=context["text"],
            block_ids=(context.get("metadata") or {}).get("block_ids", []),
            source_locations=(context.get("metadata") or {}).get("source_locations", []),
            extraction_methods=(context.get("metadata") or {}).get("extraction_methods", []),
        )
        for context in contexts
    ]
    strategy = outcome.strategy if outcome else ""
    degraded = outcome.degraded if outcome else None
    # 快搜模式（全局搜索面板）：只做检索，跳过生成与生成日志——响应即时
    if req.mode == "search":
        return QueryResponse(answer="", contexts=chunks, strategy=strategy, degraded=degraded)
    if not contexts:
        return QueryResponse(answer="知识库中没有找到与该问题相关的内容。", strategy=strategy, degraded=degraded)
    if services.generator is None:
        return QueryResponse(
            answer="（未配置 LLM，以下为检索到的相关片段）", contexts=chunks, strategy=strategy, degraded=degraded
        )
    result = services.generator.generate(req.question, contexts)
    # 生产生成日志：用量与命中可观测（逐句忠实度分析属驾驶舱职责，生产链路不重复调 LLM）
    try:
        services.query_log.log_generation(
            question=req.question,
            strategy=strategy,
            top_k=req.top_k,
            result_count=len(contexts),
            top_score=contexts[0].get("score") or 0.0,
            file_ids_hit=[c.get("file_id", "") for c in contexts if c.get("file_id")],
            answer_length=len(result.get("answer", "")),
            faithfulness_avg=None,
            unsupported_ratio=None,
        )
    except Exception:  # 日志失败不影响问答主流程
        logger.warning("生产生成日志写入失败", exc_info=True)
    return QueryResponse(
        answer=result["answer"], sources=result["sources"], contexts=chunks, strategy=strategy, degraded=degraded
    )
