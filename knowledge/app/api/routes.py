"""知识服务 API：异步文档处理、产物读取、兼容入库和检索。"""
from __future__ import annotations

import logging
import uuid
from pathlib import PurePosixPath

from botocore.exceptions import ClientError
from fastapi import APIRouter, HTTPException, Request, Response, status

from app.api.schemas import (
    ArtifactManifestResponse,
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


@router.get("/health")
def health(request: Request) -> dict:
    services = _services(request)
    return {
        "status": "ok",
        "embedder": type(services.embedder).__name__,
        "llm": "configured" if services.generator else "absent",
        "auth": bool(services.config.auth_enabled),
        "watch_dirs": len(services.watcher.directories) if services.watcher else 0,
        "ocr": services.ocr.capability().to_dict() if services.ocr else {"available": False, "provider": "unknown", "reason": "OCR 服务未配置"},
        "records": len(services.vector_store.records),
        "jobs": services.job_store.counts(),
    }


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

    job, created = services.job_store.create_or_get(
        file_id=req.file_id,
        version_id=req.version_id,
        object_key=req.object_key,
        filename=filename,
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
    try:
        return _services(request).pipeline.read_manifest(file_id, version_id)
    except FileNotFoundError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "派生产物不存在") from exc
    except ClientError as exc:
        if _is_missing_object(exc):
            raise HTTPException(status.HTTP_404_NOT_FOUND, "派生产物不存在") from exc
        raise


@router.get("/documents/{file_id}/versions/{version_id}/artifacts/{name}")
def get_artifact(file_id: str, version_id: str, name: str, request: Request) -> Response:
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
    contexts = services.retriever.retrieve(req.question, top_k=req.top_k, doc_ids=req.doc_ids)
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
    if not contexts:
        return QueryResponse(answer="知识库中没有找到与该问题相关的内容。")
    if services.generator is None:
        return QueryResponse(answer="（未配置 LLM，以下为检索到的相关片段）", contexts=chunks)
    result = services.generator.generate(req.question, contexts)
    return QueryResponse(answer=result["answer"], sources=result["sources"], contexts=chunks)
