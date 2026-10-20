"""知识库文档清单：最新状态、最近成功版本与可见性。

清单必须同时回答两件事：现在能不能读（ready + read_version_id），以及最近一次
更新为什么失败（status/error_message）。只看“最新任务状态”会让一次失败的覆盖
上传把原本可读的文档误报成不可用。
"""
from __future__ import annotations

import time

from fastapi.testclient import TestClient

from app.main import create_app
from tests.helpers import FakeStorage, make_services


def _wait_terminal(client: TestClient, job_id: str) -> dict:
    for _ in range(200):
        response = client.get(f"/jobs/{job_id}")
        assert response.status_code == 200
        payload = response.json()
        if payload["status"] in {"completed", "failed"}:
            return payload
        time.sleep(0.01)
    raise AssertionError("job did not finish in time")


def _process(
    client: TestClient,
    file_id: str,
    *,
    version: str = "v1",
    token: str | None = None,
    object_key: str | None = None,
) -> dict:
    headers = {"Authorization": f"Bearer {token}"} if token else {}
    response = client.post(
        "/documents/process",
        json={
            "file_id": file_id,
            "version_id": version,
            "object_key": object_key or f"source/{file_id}.txt",
            "filename": f"{file_id}.txt",
        },
        headers=headers,
    )
    assert response.status_code == 202, response.text
    return _wait_terminal(client, response.json()["id"])


def _user_token(services, username: str, role: str = "member") -> str:
    services.users.create_user(username, "pw-test", role=role)
    token, _ = services.users.issue_token(username)
    return token


def _list(client: TestClient, token: str | None = None) -> list[dict]:
    headers = {"Authorization": f"Bearer {token}"} if token else {}
    response = client.get("/documents", headers=headers)
    assert response.status_code == 200, response.text
    return response.json()["documents"]


def test_list_shows_ready_document_and_filters_private(tmp_path) -> None:
    storage = FakeStorage({"source/file-a.txt": b"# Policy\n\nTravel limit is 400 per night."})
    services = make_services(tmp_path, storage)
    app = create_app(services)
    alice = _user_token(services, "alice")
    bob = _user_token(services, "bob")
    admin = _user_token(services, "root", role="admin")

    with TestClient(app) as client:
        completed = _process(client, "file-a", token=alice)
        assert completed["status"] == "completed"

        alice_docs = _list(client, alice)
        assert len(alice_docs) == 1
        document = alice_docs[0]
        assert document["file_id"] == "file-a"
        assert document["status"] == "completed"
        assert document["ready"] is True
        assert document["read_version_id"] == "v1"
        assert document["chunks"] > 0
        assert document["processed_at"]

        assert _list(client, bob) == []
        assert any(item["file_id"] == "file-a" for item in _list(client, admin))


def test_list_keeps_previous_version_when_update_fails(tmp_path) -> None:
    storage = FakeStorage({"source/file-b.txt": b"version one is searchable"})
    services = make_services(tmp_path, storage)
    app = create_app(services)
    alice = _user_token(services, "alice")

    with TestClient(app) as client:
        first = _process(client, "file-b", version="v1", token=alice)
        assert first["status"] == "completed"

        failed = _process(
            client,
            "file-b",
            version="v2",
            token=alice,
            object_key="missing/file-b.txt",
        )
        assert failed["status"] == "failed"
        assert failed["error_message"]

        documents = _list(client, alice)
        assert len(documents) == 1
        document = documents[0]
        assert document["version_id"] == "v2"
        assert document["status"] == "failed"
        assert document["ready"] is True
        assert document["read_version_id"] == "v1"
        assert document["chunks"] > 0
        assert "missing" in document["error_message"] or "NoSuchKey" in document["error_message"]


def test_list_shares_unowned_document_with_members(tmp_path) -> None:
    storage = FakeStorage({"source/file-c.txt": b"shared handbook for every employee"})
    services = make_services(tmp_path, storage)
    app = create_app(services)
    bob = _user_token(services, "bob")

    with TestClient(app) as client:
        completed = _process(client, "file-c")
        assert completed["status"] == "completed"
        documents = _list(client, bob)
        assert any(item["file_id"] == "file-c" and item["ready"] for item in documents)
