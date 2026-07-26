"""评测语料装载与索引：黄金 Office 样本 + 企业文档文本，经真实 DocumentPipeline 入库。

供 tests/retrieval/test_retrieval_eval.py（HashEmbedder 确定性基线）与
scripts/eval_retrieval.py（可切换真实 embedding）共用。
"""
from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path

from app.eval.retrieval import RetrievalCase
from app.jobs.store import ProcessingJob
from app.kb.embedder import Embedder, HashEmbedder
from app.kb.store import VectorStore
from app.pipeline.service import DocumentPipeline
from app.rag.retriever import Retriever
from tests.golden.builders import build_case
from tests.helpers import FakeStorage

RETRIEVAL_DIR = Path(__file__).resolve().parent
CORPUS_PATH = RETRIEVAL_DIR / "corpus.json"
CASES_PATH = RETRIEVAL_DIR / "cases.json"

# 评测固定切块参数：语料为短篇企业文档，300/60 可以让每篇产生多个块，
# 检验的是块级排序而不是"整篇即一块"的平凡命中。改动需同步更新迭代记录。
# 哈希维度取 4096：300 字符块约产生 300 个 trigram，桶数远大于 trigram 数
# 才能保持词面区分度（256 维时全部落桶过密，recall@5 仅 0.77）。
EVAL_CHUNK_SIZE = 300
EVAL_CHUNK_OVERLAP = 60
EVAL_HASH_DIM = 4096


@dataclass
class EvalSetup:
    storage: FakeStorage
    embedder: Embedder
    vector_store: VectorStore
    pipeline: DocumentPipeline
    retriever: Retriever


def load_corpus() -> list[dict]:
    return json.loads(CORPUS_PATH.read_text(encoding="utf-8"))["documents"]


def load_cases() -> list[RetrievalCase]:
    raw_cases = json.loads(CASES_PATH.read_text(encoding="utf-8"))
    return [RetrievalCase.from_dict(raw) for raw in raw_cases]


def document_bytes(document: dict) -> bytes:
    if "builder" in document:
        return build_case(document["builder"])
    return (RETRIEVAL_DIR / "corpus" / document["path"]).read_bytes()


def build_eval_setup(
    work_dir: Path,
    embedder: Embedder | None = None,
    chunk_size: int = EVAL_CHUNK_SIZE,
    chunk_overlap: int = EVAL_CHUNK_OVERLAP,
) -> EvalSetup:
    resolved_embedder = embedder or HashEmbedder(EVAL_HASH_DIM)
    storage = FakeStorage()
    vector_store = VectorStore(str(Path(work_dir) / "vectors.json"))
    pipeline = DocumentPipeline(
        storage=storage,
        embedder=resolved_embedder,
        vector_store=vector_store,
        chunk_size=chunk_size,
        chunk_overlap=chunk_overlap,
        max_source_bytes=10 * 1024 * 1024,
    )
    return EvalSetup(
        storage=storage,
        embedder=resolved_embedder,
        vector_store=vector_store,
        pipeline=pipeline,
        retriever=Retriever(resolved_embedder, vector_store),
    )


def index_corpus(setup: EvalSetup) -> list[str]:
    """把整套评测语料走一遍解析→切块→向量化→索引，返回入库的 file_id 列表。"""
    file_ids: list[str] = []
    for document in load_corpus():
        object_key = f"eval/{document['file_id']}/{document['filename']}"
        setup.storage.objects[object_key] = document_bytes(document)
        job = ProcessingJob(
            id=f"eval-{document['file_id']}",
            file_id=document["file_id"],
            version_id="v1",
            object_key=object_key,
            filename=document["filename"],
            status="processing",
            stage="starting",
            progress=1,
            retry_count=0,
            error_code=None,
            error_message=None,
            result=None,
            created_at="2026-07-26T00:00:00+00:00",
            updated_at="2026-07-26T00:00:00+00:00",
            started_at="2026-07-26T00:00:00+00:00",
            finished_at=None,
        )
        setup.pipeline.process(job, lambda *_: None)
        file_ids.append(document["file_id"])
    return file_ids
