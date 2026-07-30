"""BM25 关键词检索器：与向量检索互补，支持混合检索策略。

轻量实现，无外部依赖：中文按字符分词，英文按空格/标点分词。
索引从向量库记录构建，按需重建（文档变更时调用 rebuild）。
"""
from __future__ import annotations

import math
import re
import threading
from collections import Counter


def _tokenize(text: str) -> list[str]:
    """中英文混合分词：中文逐字、英文按词、数字保留。"""
    tokens: list[str] = []
    # 英文词和数字
    tokens.extend(w.lower() for w in re.findall(r'[a-zA-Z_]\w+|\d+(?:\.\d+)?%?', text))
    # 中文字符逐字
    tokens.extend(re.findall(r'[\u4e00-\u9fff]', text))
    return tokens


class BM25Retriever:
    """BM25 关键词检索，接口与向量 Retriever 对齐。"""

    def __init__(self, k1: float = 1.5, b: float = 0.75) -> None:
        self.k1 = k1
        self.b = b
        self.lock = threading.RLock()
        # 索引数据
        self.records: list[dict] = []  # 原始记录（不含 embedding）
        self.doc_tokens: list[list[str]] = []  # 每条记录的 token 列表
        self.doc_freqs: list[Counter] = []  # 每条记录的词频
        self.doc_lens: list[int] = []  # 每条记录的 token 数
        self.avg_dl: float = 0.0
        self.idf: dict[str, float] = {}  # 逆文档频率
        self.n_docs: int = 0

    def rebuild(self, records: list[dict]) -> None:
        """从向量库记录重建 BM25 索引。"""
        with self.lock:
            self.records = [
                {k: v for k, v in r.items() if k != "embedding"} for r in records
            ]
            self.doc_tokens = []
            self.doc_freqs = []
            self.doc_lens = []

            df: Counter = Counter()  # 文档频率

            for record in self.records:
                tokens = _tokenize(record.get("text", ""))
                freq = Counter(tokens)
                self.doc_tokens.append(tokens)
                self.doc_freqs.append(freq)
                self.doc_lens.append(len(tokens))
                # 每个词只计一次文档频率
                for term in freq:
                    df[term] += 1

            self.n_docs = len(self.records)
            self.avg_dl = sum(self.doc_lens) / max(self.n_docs, 1)

            # IDF: log((N - df + 0.5) / (df + 0.5) + 1)
            self.idf = {
                term: math.log((self.n_docs - freq + 0.5) / (freq + 0.5) + 1.0)
                for term, freq in df.items()
            }

    def query(
        self,
        question: str,
        top_k: int = 5,
        doc_ids: list[str] | None = None,
    ) -> list[dict]:
        """BM25 检索，返回格式与向量检索一致（含 score）。"""
        with self.lock:
            if self.n_docs == 0:
                return []

            query_tokens = _tokenize(question)
            allowed = set(doc_ids) if doc_ids else None
            scores: list[float] = []

            for idx in range(self.n_docs):
                record = self.records[idx]
                if allowed and record.get("doc_id") not in allowed:
                    scores.append(-1.0)
                    continue

                freq = self.doc_freqs[idx]
                dl = self.doc_lens[idx]
                score = 0.0
                for term in query_tokens:
                    if term not in freq:
                        continue
                    tf = freq[term]
                    idf = self.idf.get(term, 0.0)
                    numerator = tf * (self.k1 + 1)
                    denominator = tf + self.k1 * (1 - self.b + self.b * dl / self.avg_dl)
                    score += idf * numerator / denominator
                scores.append(score)

            # 排序取 Top-K
            ranked = sorted(
                ((idx, s) for idx, s in enumerate(scores) if s > 0),
                key=lambda x: x[1],
                reverse=True,
            )[:top_k]

            results = []
            for idx, score in ranked:
                record = dict(self.records[idx])
                record["score"] = round(score, 4)
                results.append(record)
            return results
