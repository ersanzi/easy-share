"""检索质量评测：对标注问题集计算 hit@1 / recall@k / MRR / 片段命中率。

这是整条 RAG 链路的"尺子"：切块、embedding、向量库或检索策略的任何改动，
都应先在评测集上对比指标再合入。评测集见 tests/retrieval/cases.json。
"""
from __future__ import annotations

from dataclasses import asdict, dataclass, field

from app.rag.retriever import Retriever


@dataclass
class RetrievalCase:
    """一条标注：问题 → 应命中的文档（可选：应命中的片段、权限范围）。"""

    id: str
    question: str
    expected_file_id: str
    expected_snippet: str | None = None
    doc_ids: list[str] | None = field(default=None)

    @classmethod
    def from_dict(cls, raw: dict) -> "RetrievalCase":
        return cls(
            id=raw["id"],
            question=raw["question"],
            expected_file_id=raw["expected_file_id"],
            expected_snippet=raw.get("expected_snippet"),
            doc_ids=raw.get("doc_ids"),
        )


@dataclass
class CaseResult:
    case_id: str
    question: str
    expected_file_id: str
    retrieved_file_ids: list[str]
    hit_rank: int | None
    snippet_hit: bool | None


def evaluate_retrieval(retriever: Retriever, cases: list[RetrievalCase], top_k: int = 5) -> dict:
    """跑完整评测集并返回聚合报告。

    - hit_rank：期望文档在 top-k 结果中首次出现的名次（1-based），未命中为 None。
    - snippet_hit：期望片段是否出现在期望文档被召回的某个块里；未标注片段时为 None。
    - misses：所有未命中文档或未命中片段的用例明细，便于定位回归。
    """
    if not cases:
        raise ValueError("评测集为空")

    results: list[CaseResult] = []
    for case in cases:
        records = retriever.retrieve(case.question, top_k=top_k, doc_ids=case.doc_ids)
        file_ids = [record.get("file_id") or record.get("doc_id") for record in records]
        hit_rank = next(
            (index + 1 for index, file_id in enumerate(file_ids) if file_id == case.expected_file_id),
            None,
        )
        snippet_hit = None
        if case.expected_snippet is not None:
            snippet_hit = any(
                case.expected_snippet in (record.get("text") or "")
                for record, file_id in zip(records, file_ids)
                if file_id == case.expected_file_id
            )
        results.append(
            CaseResult(
                case_id=case.id,
                question=case.question,
                expected_file_id=case.expected_file_id,
                retrieved_file_ids=file_ids,
                hit_rank=hit_rank,
                snippet_hit=snippet_hit,
            )
        )

    total = len(results)
    hits = [result for result in results if result.hit_rank is not None]
    snippet_cases = [result for result in results if result.snippet_hit is not None]
    return {
        "top_k": top_k,
        "cases": total,
        "hit_at_1": sum(1 for result in results if result.hit_rank == 1) / total,
        "recall_at_k": len(hits) / total,
        "mrr": sum(1 / result.hit_rank for result in hits) / total,
        "snippet_hit_rate": (
            sum(1 for result in snippet_cases if result.snippet_hit) / len(snippet_cases)
            if snippet_cases
            else None
        ),
        "misses": [
            asdict(result)
            for result in results
            if result.hit_rank is None or result.snippet_hit is False
        ],
        "results": [asdict(result) for result in results],
    }
