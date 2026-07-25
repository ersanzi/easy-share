"""向量库：最薄实现——内存 + numpy 余弦相似度 + JSON 持久化 + 按 doc_id 过滤。
里程碑 1 可替换为 Chroma/Milvus，对外接口保持不变。"""
from __future__ import annotations

import json
import os
import threading

import numpy as np


class VectorStore:
    def __init__(self, path: str) -> None:
        self.path = path
        self.lock = threading.RLock()
        self.records: list[dict] = []
        self._load()

    def _load(self) -> None:
        if os.path.exists(self.path):
            with open(self.path, encoding="utf-8") as file:
                self.records = json.load(file)

    def _save(self) -> None:
        directory = os.path.dirname(self.path)
        if directory:
            os.makedirs(directory, exist_ok=True)
        temporary = f"{self.path}.tmp"
        with open(temporary, "w", encoding="utf-8") as file:
            json.dump(self.records, file, ensure_ascii=False)
        os.replace(temporary, self.path)

    def add(self, items: list[dict]) -> None:
        with self.lock:
            self.records.extend(items)
            self._save()

    def delete_doc(self, doc_id: str) -> None:
        with self.lock:
            self.records = [record for record in self.records if record.get("doc_id") != doc_id]
            self._save()

    def replace_doc(self, doc_id: str, items: list[dict]) -> None:
        """在同一个锁内替换文档索引，避免先删后加导致查询短暂看不到数据。"""
        with self.lock:
            retained = [record for record in self.records if record.get("doc_id") != doc_id]
            self.records = retained + items
            self._save()

    def query(self, embedding: list[float], top_k: int = 5, doc_ids: list[str] | None = None) -> list[dict]:
        with self.lock:
            candidates = list(self.records)
        if not candidates:
            return []
        if doc_ids:
            allowed = set(doc_ids)
            candidates = [record for record in candidates if record.get("doc_id") in allowed]
        if not candidates:
            return []

        matrix = np.array([record["embedding"] for record in candidates], dtype=np.float32)
        query = np.array(embedding, dtype=np.float32)
        if matrix.ndim != 2 or query.ndim != 1 or matrix.shape[1] != query.shape[0]:
            raise ValueError("查询向量维度与索引不一致，请使用相同 Embedding 模型重新建库")
        query_norm = np.linalg.norm(query) or 1.0
        matrix_norm = np.linalg.norm(matrix, axis=1, keepdims=True) + 1e-9
        scores = (matrix / matrix_norm) @ (query / query_norm)

        order = np.argsort(scores)[::-1][:top_k]
        results = []
        for index in order:
            record = {key: value for key, value in candidates[index].items() if key != "embedding"}
            record["score"] = float(scores[index])
            results.append(record)
        return results
