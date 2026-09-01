"""检索：把问题向量化后在向量库中取 top-k 相关片段，支持按 doc_id 范围过滤。

Embedding 服务故障时自动降级 BM25 关键词检索（TDB-AM 对标 P0），
核心检索不因外部供应商不可用而中断；未配置 BM25 时保持原行为（异常上抛）。
"""
from __future__ import annotations

import logging

from app.kb.bm25 import BM25Retriever
from app.kb.embedder import Embedder
from app.kb.store import VectorStore

logger = logging.getLogger(__name__)


class Retriever:
    def __init__(self, embedder: Embedder, store: VectorStore, bm25: BM25Retriever | None = None) -> None:
        self.embedder = embedder
        self.store = store
        self.bm25 = bm25

    def retrieve(self, question: str, top_k: int = 5, doc_ids: list[str] | None = None) -> list[dict]:
        try:
            qvec = self.embedder.embed([question])[0]
        except Exception as exc:
            if self.bm25 is None:
                raise
            logger.warning("Embedding 调用失败，降级 BM25 检索: %s", exc)
            self._ensure_bm25_index()
            return self.bm25.query(question, top_k, doc_ids)
        return self.store.query(qvec, top_k=top_k, doc_ids=doc_ids)

    def _ensure_bm25_index(self) -> None:
        """BM25 索引懒构建：与向量库记录数不一致时重建，自动跟随文档增删。

        走 count()/snapshot_records() 双后端协议（JSON 与 Milvus 均实现），
        不能直接读 store.records——Milvus 后端没有该属性。
        """
        if self.bm25.n_docs != self.store.count():
            self.bm25.rebuild(self.store.snapshot_records())
