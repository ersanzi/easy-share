"""生产查询编排（/query 回灌）：策略选择、降级链、查询日志与权限口径。"""
from __future__ import annotations

import time

import pytest
from fastapi.testclient import TestClient

from app.main import create_app
from app.rag.orchestrator import QueryOrchestrator
from tests.helpers import FakeStorage, make_services


def _wait_for_terminal(client: TestClient, job_id: str) -> dict:
    for _ in range(100):
        response = client.get(f"/jobs/{job_id}")
        assert response.status_code == 200
        payload = response.json()
        if payload["status"] in {"completed", "failed"}:
            return payload
        time.sleep(0.01)
    raise AssertionError("job did not finish in time")


def _ingest(client: TestClient, file_id: str, key: str, content: bytes) -> None:
    response = client.post(
        "/documents/process",
        json={"file_id": file_id, "version_id": "v1", "object_key": key},
    )
    completed = _wait_for_terminal(client, response.json()["id"])
    assert completed["status"] == "completed", completed.get("error_message")


def test_query_default_hybrid_strategy_and_log(tmp_path) -> None:
    storage = FakeStorage({"docs/guide.txt": b"EasyShare hybrid retrieval combines vector and keyword search."})
    services = make_services(tmp_path, storage)
    app = create_app(services)

    with TestClient(app) as client:
        _ingest(client, "file-1", "docs/guide.txt", b"")
        response = client.post("/query", json={"question": "hybrid retrieval?"})
        assert response.status_code == 200
        payload = response.json()
        assert payload["strategy"] == "hybrid"
        assert payload["degraded"] is None
        assert payload["contexts"]
        # 生产查询必须落日志——公司部署观察期的使用率/盲区数据源
        assert services.query_log.stats()["total_queries"] >= 1


def test_query_vector_strategy_preserves_legacy_behavior(tmp_path) -> None:
    storage = FakeStorage({"docs/guide.txt": b"EasyShare vector only retrieval path."})
    services = make_services(tmp_path, storage, config_overrides={"query_strategy": "vector"})
    app = create_app(services)

    with TestClient(app) as client:
        _ingest(client, "file-1", "docs/guide.txt", b"")
        response = client.post("/query", json={"question": "vector retrieval?"})
        assert response.status_code == 200
        payload = response.json()
        assert payload["strategy"] == "vector"
        assert payload["contexts"]


def test_query_multi_hop_degrades_without_llm(tmp_path) -> None:
    storage = FakeStorage({"docs/guide.txt": b"EasyShare multi hop fallback behavior."})
    # 测试装配不建 multi_hop（未配置 LLM），配置 multi_hop 应降级 hybrid_rerank 而不是报错
    services = make_services(tmp_path, storage, config_overrides={"query_strategy": "multi_hop"})
    app = create_app(services)

    with TestClient(app) as client:
        _ingest(client, "file-1", "docs/guide.txt", b"")
        response = client.post("/query", json={"question": "multi hop?"})
        assert response.status_code == 200
        payload = response.json()
        assert payload["strategy"] == "hybrid_rerank"
        assert payload["degraded"] and "降级" in payload["degraded"]
        assert payload["contexts"]


def test_hybrid_respects_doc_scope_on_both_retrievers(tmp_path) -> None:
    """混合检索的向量与 BM25 两条召回路径都必须遵守 doc_ids 过滤口径。"""
    storage = FakeStorage(
        {
            "docs/alpha.txt": b"Alpha machine spindle maintenance schedule weekly.",
            "docs/beta.txt": b"Beta machine spindle calibration procedure monthly.",
        }
    )
    services = make_services(tmp_path, storage)
    app = create_app(services)

    with TestClient(app) as client:
        _ingest(client, "file-alpha", "docs/alpha.txt", b"")
        _ingest(client, "file-beta", "docs/beta.txt", b"")
        response = client.post("/query", json={"question": "spindle", "doc_ids": ["file-alpha"]})
        assert response.status_code == 200
        contexts = response.json()["contexts"]
        assert contexts
        assert all(c["doc_id"] == "file-alpha" for c in contexts)


def test_orchestrator_rejects_unknown_strategy(tmp_path) -> None:
    services = make_services(tmp_path, FakeStorage())
    with pytest.raises(ValueError, match="query_strategy"):
        QueryOrchestrator(
            retriever=services.retriever,
            store=services.vector_store,
            bm25=services.bm25,
            reranker=services.reranker,
            strategy="bogus",
        )
