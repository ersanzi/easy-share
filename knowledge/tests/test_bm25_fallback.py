from __future__ import annotations

from app.kb.bm25 import BM25Retriever
from app.kb.store import VectorStore
from app.rag.retriever import Retriever


class FlakyEmbedder:
    """可控故障的 Embedder：fail=True 时抛错模拟供应商宕机。"""

    def __init__(self, dim: int = 8) -> None:
        self.dim = dim
        self.fail = True

    def embed(self, texts: list[str]) -> list[list[float]]:
        if self.fail:
            raise RuntimeError("embedding api down（模拟）")
        return [[0.0] * self.dim for _ in texts]


def _record(rid: str, text: str, doc_id: str) -> dict:
    return {
        "id": rid,
        "doc_id": doc_id,
        "file_id": doc_id,
        "version_id": "v1",
        "text": text,
        "metadata": {"filename": f"{rid}.md"},
        "embedding": [0.0] * 8,
    }


def _store_with(*records: dict) -> VectorStore:
    store = VectorStore(":memory:")
    store.records.extend(records)
    return store


def test_embedding_failure_falls_back_to_bm25() -> None:
    store = _store_with(_record("c1", "报销制度规定单笔上限五千元", "d1"))
    retriever = Retriever(FlakyEmbedder(), store, bm25=BM25Retriever())

    results = retriever.retrieve("报销上限是多少", top_k=5)

    assert results and results[0]["id"] == "c1"
    assert results[0]["score"] > 0  # BM25 打分，非向量余弦


def test_without_bm25_failure_propagates() -> None:
    store = _store_with(_record("c1", "内容", "d1"))
    retriever = Retriever(FlakyEmbedder(), store, bm25=None)

    try:
        retriever.retrieve("问题")
        raise AssertionError("应当上抛 Embedding 故障")
    except RuntimeError as exc:
        assert "embedding api down" in str(exc)


def test_recovers_to_vector_path_after_provider_restored() -> None:
    embedder = FlakyEmbedder()
    store = _store_with(_record("c1", "报销制度内容", "d1"))
    retriever = Retriever(embedder, store, bm25=BM25Retriever())

    assert retriever.retrieve("报销")  # 故障期走 BM25 命中
    embedder.fail = False
    results = retriever.retrieve("报销")  # 恢复后走向量路径（全零向量命中）
    assert isinstance(results, list)


def test_bm25_index_follows_new_documents() -> None:
    store = _store_with(_record("c1", "报销制度", "d1"))
    retriever = Retriever(FlakyEmbedder(), store, bm25=BM25Retriever())

    retriever.retrieve("报销")  # 首次降级构建索引（1 条）
    store.records.append(_record("c2", "差旅补贴标准说明", "d2"))
    results = retriever.retrieve("差旅补贴", top_k=5)  # 记录数变化触发重建

    assert {r["id"] for r in results} == {"c2"}


def test_services_wires_bm25_fallback(tmp_path) -> None:
    from tests.helpers import make_services

    services = make_services(tmp_path)
    try:
        assert services.retriever.bm25 is not None
    finally:
        services.job_store.close()
