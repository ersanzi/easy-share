"""向量库：最薄实现——内存 + numpy 余弦相似度 + JSON 持久化 + 按 doc_id 过滤。
里程碑 1 可替换为 Chroma/Milvus，对外接口保持不变。"""
from __future__ import annotations

import copy
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

    def get_doc(self, doc_id: str) -> list[dict]:
        """Return a document index snapshot for cross-storage rollback."""
        with self.lock:
            return copy.deepcopy(
                [record for record in self.records if record.get("doc_id") == doc_id]
            )

    def count(self) -> int:
        """索引记录总数（双后端协议，供 BM25 等派生索引懒重建判断一致性）。"""
        with self.lock:
            return len(self.records)

    def snapshot_records(self) -> list[dict]:
        """索引记录快照（含 embedding），供 BM25 等派生索引全量重建。"""
        with self.lock:
            return list(self.records)

    def doc_visible_depts(self) -> dict[str, list[str]]:
        """聚合 doc_id → 可见部门列表（文档级可见性的检索过滤数据源）。

        visible_depts 为空/缺失 = 全体可见（聚合结果为空列表）。CSV 存储与
        控制面 es_file.visible_depts 同一表示。
        """
        with self.lock:
            snapshot = list(self.records)
        result: dict[str, list[str]] = {}
        for record in snapshot:
            doc_id = record.get("doc_id")
            if not doc_id:
                continue
            raw = (record.get("metadata") or {}).get("visible_depts")
            if not raw:
                continue
            result[doc_id] = [part.strip() for part in str(raw).split(",") if part.strip()]
        return result

    def doc_owners(self) -> dict[str, str | None]:
        """聚合 doc_id → owner 映射（权限感知检索的数据源）。

        owner 为 None/缺失表示共享文档；replace_doc 整体替换保证同一文档
        的 owner 一致，聚合时取任一条即可。
        """
        with self.lock:
            snapshot = list(self.records)
        owners: dict[str, str | None] = {}
        for record in snapshot:
            doc_id = record.get("doc_id")
            if doc_id:
                owners[doc_id] = (record.get("metadata") or {}).get("owner")
        return owners

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
