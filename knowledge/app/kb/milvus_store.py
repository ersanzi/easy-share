"""Milvus 向量库：与 JSON VectorStore 同接口，生产级 ANN 检索。

启用条件：config.milvus_uri 非空且 pymilvus 已安装。
未启用时自动退回 JSON 文件存储，不影响开发与测试。
"""
from __future__ import annotations

import json
import logging
import threading
from typing import Any

logger = logging.getLogger(__name__)

# 延迟导入 pymilvus，未安装时不阻塞启动
try:
    from pymilvus import (
        Collection,
        CollectionSchema,
        DataType,
        FieldSchema,
        MilvusClient,
        connections,
        utility,
    )

    PYMILVUS_AVAILABLE = True
except ImportError:
    PYMILVUS_AVAILABLE = False

_COLLECTION_NAME = "easyshare_chunks"
_MAX_TEXT_LEN = 8192  # VARCHAR 上限（UTF-8 字节），切块默认 800 字符足够
_INDEX_PARAMS = {
    "index_type": "IVF_FLAT",
    "metric_type": "COSINE",
    "params": {"nlist": 1024},
}
_SEARCH_PARAMS = {"metric_type": "COSINE", "params": {"nprobe": 16}}


class MilvusVectorStore:
    """Milvus Standalone 向量库，接口与 JSON VectorStore 保持一致。"""

    def __init__(self, uri: str, dim: int = 1024, collection_name: str = _COLLECTION_NAME) -> None:
        if not PYMILVUS_AVAILABLE:
            raise RuntimeError(
                "pymilvus 未安装，请执行 pip install -r requirements-milvus.txt"
            )
        self.uri = uri
        self.dim = dim
        self.collection_name = collection_name
        self.lock = threading.RLock()
        self._client: MilvusClient | None = None
        self._ensure_collection()

    def _get_client(self) -> MilvusClient:
        if self._client is None:
            self._client = MilvusClient(uri=self.uri)
        return self._client

    def _ensure_collection(self) -> None:
        """幂等创建 collection + 索引。"""
        client = self._get_client()
        if client.has_collection(self.collection_name):
            return

        schema = client.create_schema(auto_id=False, enable_dynamic_field=False)
        schema.add_field("id", DataType.VARCHAR, is_primary=True, max_length=256)
        schema.add_field("doc_id", DataType.VARCHAR, max_length=128)
        schema.add_field("file_id", DataType.VARCHAR, max_length=128)
        schema.add_field("version_id", DataType.VARCHAR, max_length=64)
        schema.add_field("text", DataType.VARCHAR, max_length=_MAX_TEXT_LEN)
        schema.add_field("metadata", DataType.JSON)
        schema.add_field("embedding", DataType.FLOAT_VECTOR, dim=self.dim)

        index_params = client.prepare_index_params()
        index_params.add_index(
            field_name="embedding",
            index_type=_INDEX_PARAMS["index_type"],
            metric_type=_INDEX_PARAMS["metric_type"],
            params=_INDEX_PARAMS["params"],
        )
        # doc_id 标量索引加速过滤
        index_params.add_index(field_name="doc_id", index_type="Trie")

        client.create_collection(
            collection_name=self.collection_name,
            schema=schema,
            index_params=index_params,
        )
        logger.info("Milvus collection '%s' 已创建（dim=%d）", self.collection_name, self.dim)

    @staticmethod
    def _to_row(item: dict) -> dict:
        """将 JSON 记录转为 Milvus 行格式。"""
        metadata = item.get("metadata", {})
        return {
            "id": item["id"],
            "doc_id": item.get("doc_id", ""),
            "file_id": item.get("file_id", ""),
            "version_id": item.get("version_id", ""),
            "text": item.get("text", "")[:_MAX_TEXT_LEN],
            "metadata": json.dumps(metadata, ensure_ascii=False) if metadata else "{}",
            "embedding": item["embedding"],
        }

    @staticmethod
    def _from_row(row: dict) -> dict:
        """将 Milvus 行格式转回 JSON 记录。"""
        metadata_raw = row.get("metadata", "{}")
        metadata = json.loads(metadata_raw) if isinstance(metadata_raw, str) else metadata_raw
        return {
            "id": row["id"],
            "doc_id": row.get("doc_id", ""),
            "file_id": row.get("file_id", ""),
            "version_id": row.get("version_id", ""),
            "text": row.get("text", ""),
            "metadata": metadata,
        }

    def add(self, items: list[dict]) -> None:
        if not items:
            return
        with self.lock:
            client = self._get_client()
            rows = [self._to_row(item) for item in items]
            client.insert(collection_name=self.collection_name, data=rows)

    def delete_doc(self, doc_id: str) -> None:
        with self.lock:
            client = self._get_client()
            client.delete(
                collection_name=self.collection_name,
                filter=f'doc_id == "{doc_id}"',
            )

    def replace_doc(self, doc_id: str, items: list[dict]) -> None:
        """在同一锁内替换文档索引，避免先删后加导致查询短暂看不到数据。"""
        with self.lock:
            client = self._get_client()
            client.delete(
                collection_name=self.collection_name,
                filter=f'doc_id == "{doc_id}"',
            )
            if items:
                rows = [self._to_row(item) for item in items]
                client.insert(collection_name=self.collection_name, data=rows)

    def get_doc(self, doc_id: str) -> list[dict]:
        with self.lock:
            client = self._get_client()
            rows = client.query(
                collection_name=self.collection_name,
                filter=f'doc_id == "{doc_id}"',
                output_fields=["id", "doc_id", "file_id", "version_id", "text", "metadata"],
                limit=16384,
            )
            return [self._from_row(row) for row in rows]

    def count(self) -> int:
        """索引记录总数（双后端协议）：走 collection 统计，不拉数据。"""
        client = self._get_client()
        stats = client.get_collection_stats(self.collection_name) or {}
        row_count = stats.get("row_count", 0) if isinstance(stats, dict) else 0
        # 兼容旧版本把 row_count 包成列表的返回形态
        if isinstance(row_count, (list, tuple)):
            row_count = row_count[0] if row_count else 0
        return int(row_count)

    def snapshot_records(self) -> list[dict]:
        """全量拉取索引记录（不含 embedding），供 BM25 等派生索引全量重建，按 offset 分页。"""
        records: list[dict] = []
        page_size = 16384
        with self.lock:
            client = self._get_client()
            offset = 0
            while True:
                rows = client.query(
                    collection_name=self.collection_name,
                    filter="",
                    output_fields=["id", "doc_id", "file_id", "version_id", "text", "metadata"],
                    limit=page_size,
                    offset=offset,
                )
                records.extend(self._from_row(row) for row in rows)
                if len(rows) < page_size:
                    break
                offset += page_size
        return records

    def doc_visible_depts(self) -> dict[str, list[str]]:
        """聚合 doc_id → 可见部门列表（与 JSON 库同语义，CSV 解析）。"""
        rows_map: dict[str, str] = {}
        page_size = 16384
        with self.lock:
            client = self._get_client()
            offset = 0
            while True:
                rows = client.query(
                    collection_name=self.collection_name,
                    filter='doc_id != ""',
                    output_fields=["doc_id", "metadata"],
                    limit=page_size,
                    offset=offset,
                )
                if not rows:
                    break
                for row in rows:
                    doc_id = row.get("doc_id", "")
                    metadata = row.get("metadata", "{}")
                    try:
                        parsed = json.loads(metadata) if isinstance(metadata, str) else (metadata or {})
                    except (json.JSONDecodeError, TypeError):
                        parsed = {}
                    raw = parsed.get("visible_depts")
                    if raw:
                        rows_map[doc_id] = str(raw)
                offset += page_size
        result: dict[str, list[str]] = {}
        for doc_id, raw in rows_map.items():
            depts = [part.strip() for part in raw.split(",") if part.strip()]
            if depts:
                result[doc_id] = depts
        return result

    def doc_owners(self) -> dict[str, str | None]:
        """聚合 doc_id → owner 映射（权限感知检索的数据源），按 offset 分页拉全量。"""
        owners: dict[str, str | None] = {}
        page_size = 16384
        with self.lock:
            client = self._get_client()
            offset = 0
            while True:
                rows = client.query(
                    collection_name=self.collection_name,
                    filter='doc_id != ""',
                    output_fields=["doc_id", "metadata"],
                    limit=page_size,
                    offset=offset,
                )
                for row in rows:
                    metadata_raw = row.get("metadata", "{}")
                    metadata = json.loads(metadata_raw) if isinstance(metadata_raw, str) else metadata_raw
                    owners[row["doc_id"]] = (metadata or {}).get("owner")
                if len(rows) < page_size:
                    break
                offset += page_size
        return owners

    def query(
        self,
        embedding: list[float],
        top_k: int = 5,
        doc_ids: list[str] | None = None,
    ) -> list[dict]:
        with self.lock:
            client = self._get_client()

            filter_expr = ""
            if doc_ids:
                ids_str = ", ".join(f'"{d}"' for d in doc_ids)
                filter_expr = f"doc_id in [{ids_str}]"

            results = client.search(
                collection_name=self.collection_name,
                data=[embedding],
                limit=top_k,
                filter=filter_expr or None,
                output_fields=["id", "doc_id", "file_id", "version_id", "text", "metadata"],
                search_params=_SEARCH_PARAMS,
            )

            records = []
            for hit in results[0]:
                row = hit.get("entity", {})
                record = self._from_row(row)
                record["score"] = float(hit.get("distance", 0.0))
                records.append(record)
            return records
