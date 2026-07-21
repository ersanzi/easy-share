"""向量库：最薄实现——内存 + numpy 余弦相似度 + JSON 持久化 + 按 doc_id 过滤。
里程碑 1 可替换为 Chroma/Milvus，对外接口（add/delete_doc/query）保持不变。"""
import json
import os
import threading

import numpy as np


class VectorStore:
    def __init__(self, path: str) -> None:
        self.path = path
        self.lock = threading.Lock()
        self.records: list[dict] = []
        self._load()

    def _load(self) -> None:
        if os.path.exists(self.path):
            with open(self.path, encoding="utf-8") as f:
                self.records = json.load(f)

    def _save(self) -> None:
        directory = os.path.dirname(self.path)
        if directory:
            os.makedirs(directory, exist_ok=True)
        with open(self.path, "w", encoding="utf-8") as f:
            json.dump(self.records, f, ensure_ascii=False)

    def add(self, items: list[dict]) -> None:
        with self.lock:
            self.records.extend(items)
            self._save()

    def delete_doc(self, doc_id: str) -> None:
        with self.lock:
            self.records = [r for r in self.records if r.get("doc_id") != doc_id]
            self._save()

    def query(self, embedding: list[float], top_k: int = 5, doc_ids: list[str] | None = None) -> list[dict]:
        if not self.records:
            return []
        candidates = self.records
        if doc_ids:
            allowed = set(doc_ids)
            candidates = [r for r in candidates if r.get("doc_id") in allowed]
        if not candidates:
            return []

        mat = np.array([r["embedding"] for r in candidates], dtype=np.float32)
        q = np.array(embedding, dtype=np.float32)
        q_norm = np.linalg.norm(q) or 1.0
        m_norm = np.linalg.norm(mat, axis=1, keepdims=True) + 1e-9
        scores = (mat / m_norm) @ (q / q_norm)

        order = np.argsort(scores)[::-1][:top_k]
        results = []
        for i in order:
            record = {k: v for k, v in candidates[i].items() if k != "embedding"}
            record["score"] = float(scores[i])
            results.append(record)
        return results
