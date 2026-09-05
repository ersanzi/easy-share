"""文档清洗与索引编排：对象读取、结构化解析、派生产物和版本化索引。"""
from __future__ import annotations

import hashlib
import json
import threading
from datetime import UTC, datetime
from typing import Any, Callable

from app.jobs.store import ProcessingJob
from app.ocr import OCRProvider
from app.kb.chunker import chunk_document
from app.kb.contextual import DocContextBuilder
from app.kb.embedder import Embedder
from app.kb.store import VectorStore
from app.parsing.extractor import _parse_markdown, parse_document
from app.parsing.mineru import MinerUError
from app.parsing.mineru.adapter import document_from_mineru
from app.parsing.mineru.base import MinerUProvider
from app.parsing.models import ParsedDocument
from app.parsing.pdf_router import PdfInspectorRouter
from app.parsing.renderer import render_markdown
from app.parsing.rules import load_rules
from app.storage.base import ObjectStorage

PIPELINE_VERSION = "2026-09-05.1"
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
        cleaning_rules_path: str | None = None,
        ocr_provider: OCRProvider | None = None,
        ocr_min_text_chars: int = 20,
        mineru_provider: MinerUProvider | None = None,
        pdf_router: PdfInspectorRouter | None = None,
        context_builder: DocContextBuilder | None = None,
    ) -> None:
        self.storage = storage
        self.embedder = embedder
        self.vector_store = vector_store
        self.chunk_size = chunk_size
        self.chunk_overlap = chunk_overlap
        self.max_source_bytes = max_source_bytes
        self.cleaning_rules_path = cleaning_rules_path
        self.ocr_provider = ocr_provider
        self.ocr_min_text_chars = ocr_min_text_chars
        self.mineru_provider = mineru_provider
        self.pdf_router = pdf_router
        self.context_builder = context_builder
        self._document_locks_guard = threading.Lock()
        self._document_locks: dict[str, threading.Lock] = {}

    def process(self, job: ProcessingJob, report: ProgressReporter) -> dict:
        with self._document_lock(job.file_id):
            return self._process_locked(job, report)

    def _document_lock(self, file_id: str) -> threading.Lock:
        with self._document_locks_guard:
            return self._document_locks.setdefault(file_id, threading.Lock())

    def _process_locked(self, job: ProcessingJob, report: ProgressReporter) -> dict:
        # 入库时间：chunk metadata 与 manifest 使用同一时间戳，保证检索侧与产物侧一致
        processed_at = datetime.now(UTC).isoformat()
        report("downloading", 10)
        content = self.storage.read(job.object_key, max_bytes=self.max_source_bytes)
        source_sha256 = hashlib.sha256(content).hexdigest()

        report("parsing", 30)
        document, parsing_meta = self._parse_routed(job.filename, content)

        # 规则引擎清洗：解析后的独立阶段，命中统计写入 manifest 保证可追溯
        rules_engine = load_rules(self.cleaning_rules_path)
        rule_hits = rules_engine.apply(document)
        document.warnings.extend(rules_engine.load_warnings)
        cleaning_report = {
            "rules": [
                {"id": rule.id, "name": rule.name, "hits": rule_hits.get(rule.id, 0)}
                for rule in rules_engine.enabled_rules()
            ],
            "actions": getattr(rules_engine, "actions", []),
            "warnings": rules_engine.load_warnings,
        }

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
        # 文档级定位摘要（Contextual Chunking）：清洗之后构建，避免 PII 进索引；
        # builder 为 None（功能关闭）时与旧行为完全一致
        doc_summary = ""
        heuristic_mode = False
        if self.context_builder is not None:
            context = self.context_builder.build(document, job.filename)
            doc_summary = context.summary
            # 启发式摘要派生自标题大纲，只注入无标题上下文的段落（有路径即冗余）；
            # LLM 摘要含标题之外的语义词，全量注入
            heuristic_mode = context.provider == "heuristic"
        chunks = chunk_document(
            document,
            self.chunk_size,
            self.chunk_overlap,
            doc_summary=doc_summary,
            heuristic_mode=heuristic_mode,
        )
        if not chunks:
            raise ValueError("清洗结果无法生成有效文本块")

        report("embedding", 78)
        embeddings = self.embedder.embed([chunk.text for chunk in chunks])
        if len(embeddings) != len(chunks):
            raise ValueError("Embedding 返回数量与文本块数量不一致")

        items = [
            {
                "id": f"{job.file_id}:{job.version_id}:{index}",
                "doc_id": job.file_id,
                "file_id": job.file_id,
                "version_id": job.version_id,
                "text": chunk.text,
                "metadata": {
                    "filename": job.filename,
                    "object_key": job.object_key,
                    "owner": job.owner,
                    "pipeline_version": PIPELINE_VERSION,
                    "ingested_at": processed_at,
                    **chunk.metadata(),
                },
                "embedding": embedding,
            }
            for index, (chunk, embedding) in enumerate(zip(chunks, embeddings))
        ]

        report("indexing", 90)
        previous_items = self.vector_store.get_doc(job.file_id)
        self.vector_store.replace_doc(job.file_id, items)

        manifest = {
            "schema_version": 1,
            "pipeline_version": PIPELINE_VERSION,
            "status": "completed",
            "file_id": job.file_id,
            "version_id": job.version_id,
            "filename": job.filename,
            "object_key": job.object_key,
            "owner": job.owner,
            "source_sha256": source_sha256,
            "source_bytes": len(content),
            "media_type": document.media_type,
            "blocks": len(document.blocks),
            "characters": len(markdown),
            "chunks": len(chunks),
            "warnings": document.warnings,
            "ocr": document.metadata.get("ocr"),
            "parsing": parsing_meta,
            "cleaning": cleaning_report,
            "contextual": {
                "enabled": self.context_builder is not None,
                "provider": self.context_builder.provider if self.context_builder else None,
                "summary": doc_summary or None,
            },
            "artifacts": keys,
            "processed_at": processed_at,
        }
        report("finalizing", 97)
        try:
            self.storage.write(
                keys["manifest"],
                json.dumps(manifest, ensure_ascii=False, indent=2).encode("utf-8"),
                content_type="application/json; charset=utf-8",
            )
        except Exception:
            self.vector_store.replace_doc(job.file_id, previous_items)
            raise
        return manifest

    def _parse_routed(self, filename: str, content: bytes) -> tuple[ParsedDocument, dict[str, Any]]:
        """三层解析路由：pdf-inspector 快路由 → MinerU 深度解析 → 本地管线兜底。

        任何一层失败都向下降级并在 meta 留痕（进 manifest），保证文档处理
        永不因可选解析器不可用而失败。仅 PDF 走路由，其余格式直达本地解析。
        """
        meta: dict[str, Any] = {"provider": "local"}
        is_pdf = filename.lower().endswith(".pdf")

        route = None
        if is_pdf and self.pdf_router is not None:
            try:
                route = self.pdf_router.analyze(content)
                meta["router"] = {"provider": self.pdf_router.name, **route.to_dict()}
            except Exception as exc:  # 路由器故障不阻塞，直接走下层
                meta["router_error"] = f"{type(exc).__name__}: {exc}"

        # 第一层：文本型 PDF 本地快速提取（markdown 足够成文且无编码问题）
        if route is not None and route.kind == "text_based" and not route.has_encoding_issues:
            markdown = route.markdown or ""
            if len(markdown.strip()) >= self.ocr_min_text_chars:
                document = _parse_markdown(filename, markdown)
                document.media_type = "application/pdf"
                meta["provider"] = "pdf-inspector"
                return document, meta

        # 第二层：MinerU 深度解析（扫描/图片/混合/编码破损；路由器缺失时对全部 PDF 生效）
        mineru_ready = (
            is_pdf
            and self.mineru_provider is not None
            and self.mineru_provider.capability().available
        )
        if mineru_ready and (route is None or route.kind != "text_based" or route.has_encoding_issues):
            try:
                result = self.mineru_provider.parse_pdf(content, filename=filename)
                document = document_from_mineru(filename, result)
                meta.update(provider="mineru", backend=result.backend, duration_ms=result.duration_ms)
                return document, meta
            except (MinerUError, ValueError) as exc:
                meta["fallback_reason"] = f"mineru: {exc}"

        # 第三层：现有本地管线（pypdf 文本提取 + OCR 页级分流启发式）
        document = parse_document(
            filename,
            content,
            ocr_provider=self.ocr_provider,
            ocr_min_text_chars=self.ocr_min_text_chars,
        )
        return document, meta

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
