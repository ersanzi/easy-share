"""知识质量驾驶舱（/debug）API 回归：单文档透视的清洗 Diff 数据。"""
from __future__ import annotations

import json
import time

from fastapi.testclient import TestClient

from app.main import create_app
from tests.helpers import FakeStorage, make_services


def _process_source(client: TestClient, storage: FakeStorage, file_id: str, text: str) -> None:
    storage.objects[f"source/{file_id}.txt"] = text.encode("utf-8")
    submitted = client.post(
        "/documents/process",
        json={"file_id": file_id, "version_id": "v1", "object_key": f"source/{file_id}.txt"},
    )
    for _ in range(150):
        payload = client.get(f"/jobs/{submitted.json()['id']}").json()
        if payload["status"] in {"completed", "failed"}:
            assert payload["status"] == "completed"
            return
        time.sleep(0.01)
    raise AssertionError("job did not finish in time")


def test_cockpit_document_returns_cleaning_actions(tmp_path) -> None:
    """/debug/document 返回清洗动作明细（before/after + 规则名）。"""
    storage = FakeStorage()
    services = make_services(tmp_path, storage)
    rules_path = tmp_path / "cleaning_rules.json"
    rules_path.write_text(
        json.dumps({"rules": [{"id": "phone-mask", "enabled": True}]}), encoding="utf-8"
    )

    app = create_app(services)
    with TestClient(app) as client:
        _process_source(client, storage, "doc-a", "联系人电话 13812345678，请保密。")

        response = client.get("/debug/document/doc-a")
        assert response.status_code == 200
        payload = response.json()

        actions = payload["cleaning_actions"]
        assert len(actions) == 1
        action = actions[0]
        assert action["rule_id"] == "phone-mask"
        assert action["rule_name"] == "手机号脱敏"
        assert action["kind"] == "text"
        assert "13812345678" in action["before"]
        assert "138****5678" in action["after"]
        assert action["block_id"]


def test_cockpit_document_degrades_gracefully_without_actions(tmp_path) -> None:
    """旧文档（manifest 无 actions 字段）返回空列表而非报错。"""
    storage = FakeStorage()
    services = make_services(tmp_path, storage)
    app = create_app(services)

    with TestClient(app) as client:
        # 未入库文档 → 404
        assert client.get("/debug/document/unknown").status_code == 404
