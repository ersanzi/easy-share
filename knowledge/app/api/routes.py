"""API 路由：/health /ingest /query。骨架阶段在此组装各层组件。"""
import logging
import uuid

from fastapi import APIRouter, HTTPException

from app.api.schemas import (
    IngestRequest,
    IngestResponse,
    QueryRequest,
    QueryResponse,
    RetrievedChunk,
)
from app.config import settings
from app.kb.chunker import chunk_text
from app.kb.embedder import build_embedder
from app.kb.store import VectorStore
from app.parsing.extractor import extract_text
from app.rag.generator import build_generator
from app.rag.retriever import Retriever
from app.storage.rustfs import RustFSStorage

logger = logging.getLogger(__name__)
router = APIRouter()

# 组件单例
embedder = build_embedder()
store = VectorStore(settings.vector_store_path)
retriever = Retriever(embedder, store)
storage = RustFSStorage()
generator = build_generator()


@router.get("/health")
def health() -> dict:
    return {
        "status": "ok",
        "embedder": type(embedder).__name__,
        "llm": "configured" if generator else "absent",
        "records": len(store.records),
    }


@router.post("/ingest", response_model=IngestResponse)
def ingest(req: IngestRequest) -> IngestResponse:
    doc_id = req.doc_id or str(uuid.uuid4())

    if req.source == "text":
        if not req.content:
            raise HTTPException(400, "source=text 需要 content")
        filename = req.filename or f"{doc_id}.txt"
        text = req.content
    else:  # rustfs
        if not req.key:
            raise HTTPException(400, "source=rustfs 需要 key")
        filename = req.filename or req.key.split("/")[-1]
        content = storage.read(req.key)
        text = extract_text(filename, content)

    chunks = chunk_text(text, settings.chunk_size, settings.chunk_overlap)
    if not chunks:
        raise HTTPException(422, "文档解析后无可用文本")

    embeddings = embedder.embed(chunks)
    items = [
        {
            "id": f"{doc_id}-{i}",
            "doc_id": doc_id,
            "text": chunk,
            "metadata": {"filename": filename},
            "embedding": emb,
        }
        for i, (chunk, emb) in enumerate(zip(chunks, embeddings))
    ]
    store.delete_doc(doc_id)  # 重复入库前去重
    store.add(items)
    logger.info("入库完成: doc_id=%s filename=%s chunks=%d", doc_id, filename, len(chunks))
    return IngestResponse(doc_id=doc_id, filename=filename, chunks=len(chunks), chars=len(text))


@router.post("/query", response_model=QueryResponse)
def query(req: QueryRequest) -> QueryResponse:
    contexts = retriever.retrieve(req.question, top_k=req.top_k, doc_ids=req.doc_ids)
    chunks = [
        RetrievedChunk(
            doc_id=c.get("doc_id"),
            filename=(c.get("metadata") or {}).get("filename"),
            score=c.get("score"),
            text=c["text"],
        )
        for c in contexts
    ]
    if not contexts:
        return QueryResponse(answer="知识库中没有找到与该问题相关的内容。", contexts=[])
    if generator is None:
        return QueryResponse(answer="（未配置 LLM，以下为检索到的相关片段）", contexts=chunks)
    result = generator.generate(req.question, contexts)
    return QueryResponse(answer=result["answer"], sources=result["sources"], contexts=chunks)
