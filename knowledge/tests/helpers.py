from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path

from app.config import Settings
from app.jobs.runner import JobRunner
from app.jobs.store import JobStore
from app.kb.embedder import HashEmbedder
from app.kb.store import VectorStore
from app.pipeline.service import DocumentPipeline
from app.rag.retriever import Retriever
from app.services import AppServices


@dataclass
class FakeStorage:
    objects: dict[str, bytes] = field(default_factory=dict)
    content_types: dict[str, str] = field(default_factory=dict)
    fail_on_write: str | None = None

    def read(self, key: str, *, max_bytes: int | None = None) -> bytes:
        if key not in self.objects:
            raise FileNotFoundError(key)
        content = self.objects[key]
        if max_bytes is not None and len(content) > max_bytes:
            raise ValueError("object exceeds test size limit")
        return content

    def write(self, key: str, content: bytes, *, content_type: str) -> None:
        if self.fail_on_write and key.endswith(self.fail_on_write):
            raise OSError("simulated artifact write failure")
        self.objects[key] = content
        self.content_types[key] = content_type


def make_services(tmp_path: Path, storage: FakeStorage | None = None) -> AppServices:
    config = Settings(
        _env_file=None,
        vector_store_path=str(tmp_path / "vectors.json"),
        job_store_path=str(tmp_path / "jobs.db"),
        embedding_dim=32,
        chunk_size=80,
        chunk_overlap=10,
        max_source_bytes=1024 * 1024,
        job_workers=1,
    )
    resolved_storage = storage or FakeStorage()
    embedder = HashEmbedder(config.embedding_dim)
    vector_store = VectorStore(config.vector_store_path)
    retriever = Retriever(embedder, vector_store)
    job_store = JobStore(config.job_store_path)
    pipeline = DocumentPipeline(
        storage=resolved_storage,
        embedder=embedder,
        vector_store=vector_store,
        chunk_size=config.chunk_size,
        chunk_overlap=config.chunk_overlap,
        max_source_bytes=config.max_source_bytes,
    )
    runner = JobRunner(job_store, pipeline.process, workers=config.job_workers)
    return AppServices(
        config=config,
        storage=resolved_storage,
        embedder=embedder,
        vector_store=vector_store,
        retriever=retriever,
        generator=None,
        job_store=job_store,
        pipeline=pipeline,
        job_runner=runner,
    )
