"""重排序器：混合检索后用 Cross-Encoder 对候选结果精排。

支持 OpenAI 兼容的 rerank API（百炼 gte-rerank / bge-reranker 等）。
未配置时退回原始排序（no-op），不影响管线运行。
"""
from __future__ import annotations

import logging
from typing import Any

from app.config import Settings

logger = logging.getLogger(__name__)


class Reranker:
    """调用 rerank API 对候选 chunk 重排序。"""

    def __init__(self, base_url: str, api_key: str, model: str) -> None:
        from openai import OpenAI

        self.model = model
        self.client = OpenAI(base_url=base_url, api_key=api_key)
        logger.info("Reranker 已配置: %s (%s)", model, base_url)

    def rerank(self, query: str, documents: list[str], top_k: int | None = None) -> list[dict]:
        """重排序，返回 [{index, score}] 按 score 降序。

        使用 OpenAI 兼容的 /embeddings 或专用 /rerank 端点。
        百炼 DashScope 支持 POST /v1/rerank（非标准 OpenAI 路径）。
        """
        # 尝试标准 rerank 接口（DashScope / Jina / bge 均支持）
        try:
            response = self.client.post(
                "/rerank",
                body={
                    "model": self.model,
                    "query": query,
                    "documents": documents,
                    "top_n": top_k or len(documents),
                },
                cast_to=object,
            )
            results = response.get("results", [])  # type: ignore[union-attr]
            return [
                {"index": r["index"], "score": r["relevance_score"]}
                for r in sorted(results, key=lambda x: x["relevance_score"], reverse=True)
            ]
        except Exception as exc:
            logger.warning("Rerank API 调用失败，退回原始排序: %s", exc)
            return [{"index": i, "score": 1.0 - i * 0.01} for i in range(len(documents))]


class NoopReranker:
    """未配置 rerank 时的空操作占位。"""

    def rerank(self, query: str, documents: list[str], top_k: int | None = None) -> list[dict]:
        limit = top_k or len(documents)
        return [{"index": i, "score": 1.0 - i * 0.01} for i in range(min(limit, len(documents)))]


def build_reranker(config: Settings) -> Reranker | NoopReranker:
    """根据配置构建 reranker，未配置时返回 NoopReranker。"""
    if config.rerank_base_url and config.rerank_api_key and config.rerank_model:
        return Reranker(config.rerank_base_url, config.rerank_api_key, config.rerank_model)
    logger.info("Reranker 未配置，使用原始排序")
    return NoopReranker()
