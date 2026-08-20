from __future__ import annotations

import time
from datetime import datetime

from fastapi.testclient import TestClient

from app.jobs.store import ProcessingJob
from app.main import create_app
from app.rag.generator import SYSTEM_PROMPT, build_messages, build_reference_block
from tests.helpers import FakeStorage, make_services


def _job() -> ProcessingJob:
    return ProcessingJob(
        id="job-1",
        file_id="file-1",
        version_id="v1",
        object_key="source/v1/policy.txt",
        filename="policy.txt",
        status="processing",
        stage="starting",
        progress=1,
        retry_count=0,
        error_code=None,
        error_message=None,
        result=None,
        created_at="2026-08-20T00:00:00+00:00",
        updated_at="2026-08-20T00:00:00+00:00",
        started_at="2026-08-20T00:00:00+00:00",
        finished_at=None,
    )


# ---------- 生成器：时效注入 ----------


def test_reference_block_annotates_document_time() -> None:
    contexts = [
        {"doc_id": "old-doc", "text": "旧版制度正文", "metadata": {"ingested_at": "2024-01-02T03:04:05+00:00"}},
        {"doc_id": "new-doc", "text": "新版制度正文", "metadata": {}},
    ]
    block = build_reference_block(contexts)

    assert "[1] (来源: old-doc, 文档时间: 2024-01-02T03:04:05+00:00) 旧版制度正文" in block
    # 无入库时间的旧数据不标注，保持向后兼容
    assert "[2] (来源: new-doc) 新版制度正文" in block


def test_system_prompt_carries_freshness_directive() -> None:
    assert "较新" in SYSTEM_PROMPT
    assert "时效" in SYSTEM_PROMPT


def test_messages_include_dates_and_directive() -> None:
    contexts = [{"doc_id": "d", "text": "正文", "metadata": {"ingested_at": "2026-08-01T00:00:00+00:00"}}]
    messages = build_messages("报销标准是什么", contexts)

    assert any("文档时间: 2026-08-01" in message["content"] for message in messages)
    assert any("时效" in message["content"] for message in messages)


# ---------- 管线：ingested_at 落入 chunk metadata ----------


def test_pipeline_stamps_ingested_at_on_chunks(tmp_path) -> None:
    storage = FakeStorage({"source/v1/policy.txt": "第一条 报销标准。\n\n第二条 审批流程。".encode()})
    services = make_services(tmp_path, storage)
    try:
        manifest = services.pipeline.process(_job(), lambda *_: None)

        assert services.vector_store.records, "应产生向量记录"
        for record in services.vector_store.records:
            ingested_at = record["metadata"]["ingested_at"]
            datetime.fromisoformat(ingested_at)  # ISO 格式可解析
        # chunk metadata 与 manifest 使用同一时间戳
        assert services.vector_store.records[0]["metadata"]["ingested_at"] == manifest["processed_at"]
    finally:
        services.job_store.close()


# ---------- API：/query 透出入库时间 ----------


def test_query_contexts_expose_ingested_at(tmp_path) -> None:
    storage = FakeStorage({"source/policy.txt": b"EasyShare freshness policy content for retrieval."})
    services = make_services(tmp_path, storage)
    app = create_app(services)

    with TestClient(app) as client:
        submitted = client.post(
            "/documents/process",
            json={"file_id": "file-1", "version_id": "v1", "object_key": "source/policy.txt"},
        )
        for _ in range(100):
            payload = client.get(f"/jobs/{submitted.json()['id']}").json()
            if payload["status"] in {"completed", "failed"}:
                break
            time.sleep(0.01)
        assert payload["status"] == "completed"

        response = client.post("/query", json={"question": "freshness policy", "doc_ids": ["file-1"]})
        assert response.status_code == 200
        context = response.json()["contexts"][0]
        assert context["ingested_at"]
        datetime.fromisoformat(context["ingested_at"])
