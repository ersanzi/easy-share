"""阅读页数据面：文档视图、原件下载与产物级可见性。

产物端点此前只验登录、不验文档归属；阅读页把“按 file_id 直取原文”变成
常规能力后，这条路径必须与 /query 共用同一套 owner/部门判据。
"""
from __future__ import annotations

import time

from fastapi.testclient import TestClient

from app.main import create_app
from app.rag.permissions import can_view_document
from tests.helpers import FakeStorage, make_services


def _wait_for_terminal(client: TestClient, job_id: str) -> dict:
    for _ in range(200):
        response = client.get(f"/jobs/{job_id}")
        assert response.status_code == 200
        payload = response.json()
        if payload["status"] in {"completed", "failed"}:
            assert payload["status"] == "completed", payload
            return payload
        time.sleep(0.01)
    raise AssertionError("job did not finish in time")


def _process(client: TestClient, file_id: str, content: bytes, token: str | None = None) -> None:
    headers = {"Authorization": f"Bearer {token}"} if token else {}
    response = client.post(
        "/documents/process",
        json={
            "file_id": file_id,
            "version_id": "v1",
            "object_key": f"source/{file_id}.txt",
            "filename": f"{file_id}.txt",
        },
        headers=headers,
    )
    assert response.status_code == 202, response.text
    _wait_for_terminal(client, response.json()["id"])


def _user_token(services, username: str, role: str = "member") -> str:
    services.users.create_user(username, "pw-test", role=role)
    token, _ = services.users.issue_token(username)
    return token


def test_view_and_source_roundtrip(tmp_path) -> None:
    storage = FakeStorage({"source/file-view.txt": b"# Policy\n\nTravel limit is 400 per night."})
    services = make_services(tmp_path, storage)
    app = create_app(services)

    with TestClient(app) as client:
        _process(client, "file-view", b"")

        view = client.get("/documents/file-view/versions/v1/view")
        assert view.status_code == 200, view.text
        payload = view.json()
        assert payload["manifest"]["filename"] == "file-view.txt"
        assert payload["manifest"]["object_key"] == "source/file-view.txt"
        blocks = payload["document"]["blocks"]
        assert blocks and any("Policy" in block.get("text", "") for block in blocks)
        assert all("id" in block for block in blocks)

        source = client.get("/documents/file-view/versions/v1/source")
        assert source.status_code == 200, source.text
        assert source.content == storage.objects["source/file-view.txt"]
        assert "attachment;" in source.headers["content-disposition"]
        assert "file-view.txt" in source.headers["content-disposition"]

        # 旧产物端点仍可用，且走同一套可见性判据
        clean = client.get("/documents/file-view/versions/v1/artifacts/clean.md")
        assert clean.status_code == 200
        assert "Travel limit" in clean.text


def test_private_document_view_is_owner_only(tmp_path) -> None:
    storage = FakeStorage({"source/file-private.txt": b"alice private contract pricing details"})
    services = make_services(tmp_path, storage)
    app = create_app(services)
    alice = _user_token(services, "alice")
    bob = _user_token(services, "bob")

    with TestClient(app) as client:
        _process(client, "file-private", b"", token=alice)

        assert client.get("/documents/file-private/versions/v1/view", headers={"Authorization": f"Bearer {alice}"}).status_code == 200
        assert client.get("/documents/file-private/versions/v1/source", headers={"Authorization": f"Bearer {alice}"}).status_code == 200

        for path in ("view", "source", "artifacts/clean.md", "artifacts"):
            denied = client.get(f"/documents/file-private/versions/v1/{path}", headers={"Authorization": f"Bearer {bob}"})
            assert denied.status_code == 403, (path, denied.status_code, denied.text)


def test_shared_document_view_is_visible_to_members(tmp_path) -> None:
    storage = FakeStorage({"source/file-shared.txt": b"shared handbook for every employee"})
    services = make_services(tmp_path, storage)
    app = create_app(services)
    bob = _user_token(services, "bob")

    with TestClient(app) as client:
        _process(client, "file-shared", b"")
        response = client.get("/documents/file-shared/versions/v1/view", headers={"Authorization": f"Bearer {bob}"})
        assert response.status_code == 200, response.text


def test_can_view_document_matches_visibility_rules() -> None:
    assert can_view_document({"owner": None}, None) is True
    assert can_view_document({"owner": "alice"}, {"username": "alice", "role": "member"}) is True
    assert can_view_document({"owner": "alice"}, {"username": "bob", "role": "member"}) is False
    assert can_view_document({"owner": "alice"}, {"username": "root", "role": "admin"}) is True
    assert can_view_document(
        {"owner": None, "visible_depts": ["10"]},
        {"username": "bob", "role": "member", "dept": "10"},
    ) is True
    assert can_view_document(
        {"owner": None, "visible_depts": ["10"]},
        {"username": "bob", "role": "member", "dept": "20"},
    ) is False
    # 入库任务把部门存成 CSV；按子串判断会让部门 "1" 误命中 "10"
    assert can_view_document(
        {"owner": None, "visible_depts": "10,20"},
        {"username": "bob", "role": "member", "dept": "20"},
    ) is True
    assert can_view_document(
        {"owner": None, "visible_depts": "10,20"},
        {"username": "bob", "role": "member", "dept": "1"},
    ) is False
