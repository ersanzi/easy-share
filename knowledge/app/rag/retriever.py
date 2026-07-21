"""检索：把问题向量化后在向量库中取 top-k 相关片段，支持按 doc_id 范围过滤。"""
from app.kb.embedder import Embedder
from app.kb.store import VectorStore


class Retriever:
    def __init__(self, embedder: Embedder, store: VectorStore) -> None:
        self.embedder = embedder
        self.store = store

    def retrieve(self, question: str, top_k: int = 5, doc_ids: list[str] | None = None) -> list[dict]:
        qvec = self.embedder.embed([question])[0]
        return self.store.query(qvec, top_k=top_k, doc_ids=doc_ids)
