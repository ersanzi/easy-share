"""向量化：可替换网关。配置了 OpenAI 兼容 embedding 服务时用真实语义向量；
未配置时退回 HashEmbedder 仅用于跑通管线（无语义能力）。"""
import hashlib
import logging
import math
from abc import ABC, abstractmethod

from app.config import settings

logger = logging.getLogger(__name__)


class Embedder(ABC):
    dim: int

    @abstractmethod
    def embed(self, texts: list[str]) -> list[list[float]]:
        ...


class OpenAIEmbedder(Embedder):
    def __init__(self, base_url: str, api_key: str, model: str, dim: int) -> None:
        from openai import OpenAI

        self.dim = dim
        self.model = model
        self.client = OpenAI(base_url=base_url, api_key=api_key)

    def embed(self, texts: list[str]) -> list[list[float]]:
        resp = self.client.embeddings.create(model=self.model, input=texts)
        return [d.embedding for d in resp.data]


class HashEmbedder(Embedder):
    """占位嵌入：字符 n-gram 哈希生成确定性向量，仅用于在没有 embedding 服务时
    跑通整条管线，不具备语义检索能力。配置真实 embedding 后应自动替换。"""

    def __init__(self, dim: int) -> None:
        self.dim = dim

    def embed(self, texts: list[str]) -> list[list[float]]:
        return [self._one(t) for t in texts]

    def _one(self, text: str) -> list[float]:
        vec = [0.0] * self.dim
        if not text:
            return vec
        for i in range(max(1, len(text) - 2)):
            token = text[i : i + 3]
            h = int(hashlib.md5(token.encode("utf-8")).hexdigest(), 16)
            vec[h % self.dim] += 1.0
        norm = math.sqrt(sum(v * v for v in vec)) or 1.0
        return [v / norm for v in vec]


def build_embedder() -> Embedder:
    if settings.embedding_api_key and settings.embedding_base_url and settings.embedding_model:
        logger.info("使用 OpenAI 兼容 embedding: %s", settings.embedding_model)
        return OpenAIEmbedder(
            settings.embedding_base_url,
            settings.embedding_api_key,
            settings.embedding_model,
            settings.embedding_dim,
        )
    logger.warning("未配置 embedding 服务，退回 HashEmbedder（仅跑通管线，无语义能力）")
    return HashEmbedder(settings.embedding_dim)
