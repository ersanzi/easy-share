"""服务组件容器：集中组装依赖，避免路由模块级单例并便于测试注入。"""
from __future__ import annotations

import logging
from dataclasses import dataclass

from app.config import Settings, settings
from app.jobs.runner import JobRunner
from app.jobs.store import JobStore
from app.kb.bm25 import BM25Retriever
from app.kb.embedder import Embedder, build_embedder
from app.kb.query_log import QueryLog
from app.kb.reranker import Reranker, NoopReranker, build_reranker
from app.kb.store import VectorStore
from app.pipeline.service import DocumentPipeline
from app.ocr import OCRProvider, build_paddle_provider
from app.rag.generator import Generator, build_generator
from app.rag.retriever import Retriever
from app.storage.base import ObjectStorage
from app.storage.rustfs import RustFSStorage

logger = logging.getLogger(__name__)


@dataclass(slots=True)
class AppServices:
    config: Settings
    storage: ObjectStorage
    embedder: Embedder
    vector_store: VectorStore
    bm25: BM25Retriever
    query_log: QueryLog
    reranker: Reranker | NoopReranker
    retriever: Retriever
    generator: Generator | None
    job_store: JobStore
    pipeline: DocumentPipeline
    job_runner: JobRunner
    ocr: OCRProvider | None = None

    def start(self) -> None:
        self.job_runner.start()

    def close(self) -> None:
        self.job_runner.shutdown()
        self.job_store.close()


def build_vector_store(config: Settings) -> VectorStore:
    """根据配置选择向量库实现：Milvus（生产）或 JSON 文件（开发/测试）。"""
    if config.milvus_uri:
        from app.kb.milvus_store import MilvusVectorStore

        logger.info("向量库：Milvus（%s, collection=%s）", config.milvus_uri, config.milvus_collection)
        return MilvusVectorStore(
            uri=config.milvus_uri,
            dim=config.embedding_dim,
            collection_name=config.milvus_collection,
        )
    logger.info("向量库：JSON 文件（%s）", config.vector_store_path)
    return VectorStore(config.vector_store_path)


def build_services(config: Settings = settings) -> AppServices:
    storage = RustFSStorage(
        endpoint=config.rustfs_endpoint,
        access_key=config.rustfs_access_key,
        secret_key=config.rustfs_secret_key,
        bucket=config.rustfs_bucket,
    )
    embedder = build_embedder(config)
    ocr = build_paddle_provider(enabled=config.ocr_enabled, lang=config.ocr_lang)
    vector_store = build_vector_store(config)
    bm25 = BM25Retriever()
    query_log = QueryLog(config.query_log_path)
    reranker = build_reranker(config)
    retriever = Retriever(embedder, vector_store)
    generator = build_generator(config)
    job_store = JobStore(config.job_store_path)
    pipeline = DocumentPipeline(
        storage=storage,
        embedder=embedder,
        vector_store=vector_store,
        chunk_size=config.chunk_size,
        chunk_overlap=config.chunk_overlap,
        max_source_bytes=config.max_source_bytes,
        cleaning_rules_path=config.cleaning_rules_path,
        ocr_provider=ocr,
        ocr_min_text_chars=config.ocr_min_text_chars,
    )
    job_runner = JobRunner(job_store, pipeline.process, workers=config.job_workers)
    return AppServices(
        config=config,
        storage=storage,
        embedder=embedder,
        vector_store=vector_store,
        bm25=bm25,
        query_log=query_log,
        reranker=reranker,
        retriever=retriever,
        generator=generator,
        job_store=job_store,
        pipeline=pipeline,
        job_runner=job_runner,
        ocr=ocr,
    )
