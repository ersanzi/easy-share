"""混合检索融合：RRF（Reciprocal Rank Fusion）。

从 debug 路由提升为公共能力，供检索调试与 Agent 多跳检索共用。
"""
from __future__ import annotations


def hybrid_fusion(
    vector_results: list[dict],
    bm25_results: list[dict],
    top_k: int,
    alpha: float = 0.6,
) -> list[dict]:
    """RRF 混合排序，alpha 为向量权重。"""
    k = 60  # RRF 常数
    scores: dict[str, float] = {}
    records_map: dict[str, dict] = {}

    for rank, record in enumerate(vector_results, start=1):
        rid = record.get("id", "")
        scores[rid] = scores.get(rid, 0.0) + alpha / (k + rank)
        records_map[rid] = record

    for rank, record in enumerate(bm25_results, start=1):
        rid = record.get("id", "")
        scores[rid] = scores.get(rid, 0.0) + (1 - alpha) / (k + rank)
        if rid not in records_map:
            records_map[rid] = record

    ranked = sorted(scores.items(), key=lambda x: x[1], reverse=True)[:top_k]
    results = []
    for rid, score in ranked:
        record = dict(records_map[rid])
        record["score"] = round(score, 6)
        results.append(record)
    return results
