"""文档清洗与索引编排：对象读取、结构化解析、派生产物和版本化索引。"""
from __future__ import annotations

import hashlib
import json
from datetime import UTC, datetime
from typing import Callable

from app.jobs.store import ProcessingJob
from app.kb.chunker import chunk_text
from app.kb.embedder import Embedder
from app.kb.store import VectorStore
from app.parsing.extractor import parse_document
from app.parsing.renderer import render_markdown
from app.storage.base import ObjectStorage

PIPELINE_VERSION = "2026-07-25.1"
ProgressReporter = Callable[[str, int], None]


def artifact_keys(file_id: str, version_id: str) -> dict[str, str]:
    prefix = f"derived/{file_id}/{version_id}"
    return {
        "clean_markdown": f"{prefix}/clean.md",
        "document": f"{prefix}/document.json",
        "manifest": f"{prefix}/manifest.json",
    }


class DocumentPipeline:
    def __init__(
        self,
        *,
        storage: ObjectStorage,
        embedder: Embedder,
        vector_store: VectorStore,
        chunk_size: int,
        chunk_overlap: int,
        max_source_bytes: int,
    ) -> None:
        self.storage = storage
        self.embedder = embedder
        self.vector_store = vector_store
        self.chunk_size = chunk_size
        self.chunk_overlap = chunk_overlap
        self.max_source_bytes = max_source_bytes

    def process(self, job: ProcessingJob, report: ProgressReporter) -> dict:
        report("downloading", 10)
        content = self.storage.read(job.object_key, max_bytes=self.max_source_bytes)
        source_sha256 = hashlib.sha256(content).hexdigest()

        report("parsing", 30)
        document = parse_document(job.filename, content)
        markdown = render_markdown(document)
        if not markdown.strip():
            raise ValueError("清洗结果为空")

        keys = artifact_keys(job.file_id, job.version_id)
        document_payload = document.to_dict()
        document_payload.update(
            {
                "file_id": job.file_id,
                "version_id": job.version_id,
                "object_key": job.object_key,
                "source_sha256": source_sha256,
                "pipeline_version": PIPELINE_VERSION,
            }
        )

        report("saving_artifacts", 55)
        self.storage.write(
            keys["clean_markdown"],
            markdown.encode("utf-8"),
            content_type="text/markdown; charset=utf-8",
        )
        self.storage.write(
            keys["document"],
            json.dumps(document_payload, ensure_ascii=False, indent=2).encode("utf-8"),
            content_type="application/json; charset=utf-8",
        )

        report("chunking", 68)
        chunks = chunk_text(markdown, self.chunk_size, self.chunk_overlap)
        if not chunks:
            raise ValueError("清洗结果无法生成有效文本块")

        report("embedding", 78)
        embeddings = self.embedder.embed(chunks)
        if len(embeddings) != len(chunks):
            raise ValueError("Embedding 返回数量与文本块数量不一致")

        items = [
            {
                "id": f"{job.file_id}:{job.version_id}:{index}",
                "doc_id": job.file_id,
                "file_id": job.file_id,
                "version_id": job.version_id,
                "text": chunk,
                "metadata": {
                    "filename": job.filename,
                    "object_key": job.object_key,
                    "pipeline_version": PIPELINE_VERSION,
                },
                "embedding": embedding,
            }
            for index, (chunk, embedding) in enumerate(zip(chunks, embeddings))
        ]

        report("indexing", 90)
        self.vector_store.replace_doc(job.file_id, items)

        manifest = {
            "schema_version": 1,
            "pipeline_version": PIPELINE_VERSION,
            "status": "completed",
            "file_id": job.file_id,
            "version_id": job.version_id,
            "filename": job.filename,
            "object_key": job.object_key,
            "source_sha256": source_sha256,
            "source_bytes": len(content),
            "media_type": document.media_type,
            "blocks": len(document.blocks),
            "characters": len(markdown),
            "chunks": len(chunks),
            "warnings": document.warnings,
            "artifacts": keys,
            "processed_at": datetime.now(UTC).isoformat(),
        }
        report("finalizing", 97)
        self.storage.write(
            keys["manifest"],
            json.dumps(manifest, ensure_ascii=False, indent=2).encode("utf-8"),
            content_type="application/json; charset=utf-8",
        )
        return manifest

    def read_artifact(self, file_id: str, version_id: str, name: str) -> tuple[bytes, str]:
        keys = artifact_keys(file_id, version_id)
        mapping = {
            "clean.md": (keys["clean_markdown"], "text/markdown; charset=utf-8"),
            "document.json": (keys["document"], "application/json; charset=utf-8"),
            "manifest.json": (keys["manifest"], "application/json; charset=utf-8"),
        }
        if name not in mapping:
            raise KeyError(name)
        key, content_type = mapping[name]
        return self.storage.read(key, max_bytes=self.max_source_bytes), content_type

    def read_manifest(self, file_id: str, version_id: str) -> dict:
        content, _ = self.read_artifact(file_id, version_id, "manifest.json")
        return json.loads(content.decode("utf-8"))
