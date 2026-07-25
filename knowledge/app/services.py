"""服务组件容器：集中组装依赖，避免路由模块级单例并便于测试注入。"""
from __future__ import annotations

from dataclasses import dataclass

from app.config import Settings, settings
from app.jobs.runner import JobRunner
from app.jobs.store import JobStore
from app.kb.embedder import Embedder, build_embedder
from app.kb.store import VectorStore
from app.pipeline.service import DocumentPipeline
from app.rag.generator import Generator, build_generator
from app.rag.retriever import Retriever
from app.storage.base import ObjectStorage
from app.storage.rustfs import RustFSStorage


@dataclass(slots=True)
class AppServices:
    config: Settings
    storage: ObjectStorage
    embedder: Embedder
    vector_store: VectorStore
    retriever: Retriever
    generator: Generator | None
    job_store: JobStore
    pipeline: DocumentPipeline
    job_runner: JobRunner

    def start(self) -> None:
        self.job_runner.start()

    def close(self) -> None:
        self.job_runner.shutdown()
        self.job_store.close()


def build_services(config: Settings = settings) -> AppServices:
    storage = RustFSStorage(
        endpoint=config.rustfs_endpoint,
        access_key=config.rustfs_access_key,
        secret_key=config.rustfs_secret_key,
        bucket=config.rustfs_bucket,
    )
    embedder = build_embedder(config)
    vector_store = VectorStore(config.vector_store_path)
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
    )
    job_runner = JobRunner(job_store, pipeline.process, workers=config.job_workers)
    return AppServices(
        config=config,
        storage=storage,
        embedder=embedder,
        vector_store=vector_store,
        retriever=retriever,
        generator=generator,
        job_store=job_store,
        pipeline=pipeline,
        job_runner=job_runner,
    )
