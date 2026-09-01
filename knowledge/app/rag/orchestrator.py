"""生产查询编排：按配置策略调度检索，统一降级链与查询日志。

策略（config.query_strategy，默认 hybrid；部署级配置不暴露给终端用户——技术参数自动推断）：
- vector         单路向量（旧行为；embedding 故障时 Retriever 内部自动降级 BM25）
- hybrid         向量 + BM25 RRF 融合
- hybrid_rerank  融合候选池 ×3 后 Cross-Encoder 精排（未配置 rerank 时等价 hybrid）
- multi_hop      分轮混合检索 + LLM 充分性裁判（需 LLM，未配置自动降级 hybrid_rerank）

降级只降不炸：任一外部依赖（embedding / rerank / LLM）不可用都退到最接近的可用策略；
outcome.strategy 回告实际执行策略，响应与查询日志均按实际策略记录，便于驾驶舱归因。

此前这些能力只在 /debug 驾驶舱可用（1.9 检索加固批次），本层把它们回灌生产 /query。
"""
from __future__ import annotations

import logging
from dataclasses import dataclass, field
from typing import Any

from app.kb.bm25 import BM25Retriever
from app.kb.fusion import hybrid_fusion

logger = logging.getLogger(__name__)

_VALID_STRATEGIES = {"vector", "hybrid", "hybrid_rerank", "multi_hop"}


@dataclass(slots=True)
class RetrievalOutcome:
    contexts: list[dict]                                   # 最终上下文（按分数降序）
    strategy: str                                          # 实际执行的策略
    requested: str                                         # 配置请求的策略
    degraded: str | None = None                            # 降级说明（配置策略不可用时）
    hops: list[dict[str, Any]] = field(default_factory=list)  # multi_hop 跳记录


class QueryOrchestrator:
    """按策略调度检索组件，权限裁剪后的 doc_ids 全程透传（向量与 BM25 两侧同口径）。"""

    def __init__(
        self,
        *,
        retriever,
        store,
        bm25: BM25Retriever,
        reranker,
        multi_hop=None,
        query_log=None,
        strategy: str = "hybrid",
    ) -> None:
        if strategy not in _VALID_STRATEGIES:
            raise ValueError(f"未知 query_strategy: {strategy}（可选 {sorted(_VALID_STRATEGIES)}）")
        self.retriever = retriever
        self.store = store
        self.bm25 = bm25
        self.reranker = reranker
        self.multi_hop = multi_hop
        self.query_log = query_log
        self.strategy = strategy

    def retrieve(self, question: str, top_k: int = 5, doc_ids: list[str] | None = None) -> RetrievalOutcome:
        if self.strategy == "vector":
            contexts = self.retriever.retrieve(question, top_k=top_k, doc_ids=doc_ids)
            outcome = RetrievalOutcome(contexts=contexts, strategy="vector", requested=self.strategy)
        elif self.strategy == "multi_hop" and self.multi_hop is not None:
            self._ensure_bm25()
            result = self.multi_hop.retrieve(question, doc_ids)
            outcome = RetrievalOutcome(
                contexts=result.contexts,
                strategy="multi_hop",
                requested=self.strategy,
                hops=[hop.to_dict() for hop in result.hops],
            )
        else:
            strategy = self.strategy
            degraded = None
            if self.strategy == "multi_hop":
                strategy = "hybrid_rerank"
                degraded = "multi_hop 不可用（未配置 LLM），已降级 hybrid_rerank"
            contexts = self._hybrid(question, top_k, doc_ids, rerank=strategy == "hybrid_rerank")
            outcome = RetrievalOutcome(contexts=contexts, strategy=strategy, requested=self.strategy, degraded=degraded)
        self._log(question, outcome, top_k)
        return outcome

    def _hybrid(self, question: str, top_k: int, doc_ids: list[str] | None, *, rerank: bool) -> list[dict]:
        """向量 + BM25 双路召回 → RRF 融合 → 可选精排，截回 top_k。"""
        vector_results = self.retriever.retrieve(question, top_k=top_k * 2, doc_ids=doc_ids)
        self._ensure_bm25()
        bm25_results = self.bm25.query(question, top_k * 2, doc_ids)
        pool = hybrid_fusion(vector_results, bm25_results, top_k * 3)
        if not rerank:
            return pool[:top_k]
        return self._rerank(question, pool, top_k)

    def _rerank(self, question: str, pool: list[dict], top_k: int) -> list[dict]:
        """候选池精排；Reranker 内部失败已退回原序，这里只做越界防御。"""
        if not pool:
            return []
        scores = self.reranker.rerank(question, [record.get("text", "") for record in pool], top_k)
        results: list[dict] = []
        for item in scores:
            index = item.get("index", -1)
            if 0 <= index < len(pool):
                record = dict(pool[index])
                record["score"] = item.get("score", record.get("score"))
                results.append(record)
        return results

    def _ensure_bm25(self) -> None:
        """BM25 索引懒构建：与向量库记录数不一致时重建，自动跟随文档增删。"""
        if self.bm25.n_docs != self.store.count():
            self.bm25.rebuild(self.store.snapshot_records())

    def _log(self, question: str, outcome: RetrievalOutcome, top_k: int) -> None:
        """生产查询日志：公司部署观察期的使用率/盲区数据源（此前只有驾驶舱查询会记录）。"""
        if self.query_log is None:
            return
        try:
            self.query_log.log_retrieval(
                question=question,
                strategy=outcome.strategy,
                top_k=top_k,
                result_count=len(outcome.contexts),
                top_score=outcome.contexts[0].get("score", 0.0) if outcome.contexts else 0.0,
                file_ids_hit=[r.get("file_id", "") for r in outcome.contexts if r.get("file_id")],
            )
        except Exception:  # 日志失败不影响检索主流程
            logger.warning("生产查询日志写入失败", exc_info=True)
