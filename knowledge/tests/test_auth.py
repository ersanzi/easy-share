from __future__ import annotations

from fastapi.testclient import TestClient

from app.main import create_app
from tests.helpers import FakeStorage, make_services


def _auth_app(tmp_path):
    """启用认证的测试服务：先入库一份文档供 /query 使用。"""
    storage = FakeStorage({"source/policy.txt": b"EasyShare knowledge auth policy content."})
    services = make_services(
        tmp_path,
        storage,
        config_overrides={"auth_enabled": True},
    )
    return create_app(services), services


def _seed_and_login(client: TestClient) -> dict:
    bootstrap = client.post("/auth/bootstrap", json={"username": "boss", "password": "secret-pass-1"})
    assert bootstrap.status_code == 200
    login = client.post("/auth/login", json={"username": "boss", "password": "secret-pass-1"})
    assert login.status_code == 200
    payload = login.json()
    assert payload["role"] == "admin"
    return payload


def test_bootstrap_only_once(tmp_path) -> None:
    app, _ = _auth_app(tmp_path)
    with TestClient(app) as client:
        assert client.post("/auth/bootstrap", json={"username": "boss", "password": "secret-pass-1"}).status_code == 200
        again = client.post("/auth/bootstrap", json={"username": "other", "password": "x"})
        assert again.status_code == 409


def test_login_and_protected_routes(tmp_path) -> None:
    app, services = _auth_app(tmp_path)
    with TestClient(app) as client:
        # 先以管理员身份处理一份文档（也顺带验证写路径需要令牌）
        anonymous_process = client.post(
            "/documents/process",
            json={"file_id": "file-1", "version_id": "v1", "object_key": "source/policy.txt"},
        )
        assert anonymous_process.status_code == 401

        login = _seed_and_login(client)
        headers = {"Authorization": f"Bearer {login['token']}"}

        assert client.get("/auth/me", headers=headers).json()["username"] == "boss"
        assert client.get("/health").status_code == 200  # 白名单不受保护

        submitted = client.post(
            "/documents/process",
            json={"file_id": "file-1", "version_id": "v1", "object_key": "source/policy.txt"},
            headers=headers,
        )
        assert submitted.status_code == 202
        for _ in range(100):
            job = client.get(f"/jobs/{submitted.json()['id']}", headers=headers).json()
            if job["status"] in {"completed", "failed"}:
                break
        assert job["status"] == "completed"

        query = client.post(
            "/query", json={"question": "auth policy", "doc_ids": ["file-1"]}, headers=headers
        )
        assert query.status_code == 200
        assert query.json()["contexts"]


def test_wrong_password_rejected(tmp_path) -> None:
    app, _ = _auth_app(tmp_path)
    with TestClient(app) as client:
        _seed_and_login(client)
        bad = client.post("/auth/login", json={"username": "boss", "password": "wrong"})
        assert bad.status_code == 401


def test_admin_creates_member_and_member_cannot_manage(tmp_path) -> None:
    app, _ = _auth_app(tmp_path)
    with TestClient(app) as client:
        login = _seed_and_login(client)
        admin_headers = {"Authorization": f"Bearer {login['token']}"}

        created = client.post(
            "/auth/users",
            json={"username": "zhang", "password": "member-pass-1", "role": "member"},
            headers=admin_headers,
        )
        assert created.status_code == 201

        member_login = client.post("/auth/login", json={"username": "zhang", "password": "member-pass-1"})
        member_headers = {"Authorization": f"Bearer {member_login.json()['token']}"}

        assert client.get("/auth/users", headers=member_headers).status_code == 403
        assert client.post(
            "/auth/users", json={"username": "li", "password": "x"}, headers=member_headers
        ).status_code == 403


def test_disabled_auth_keeps_legacy_behavior(tmp_path) -> None:
    # 默认关闭：不带令牌访问 /query 不再 401（既有全部测试依赖此行为）
    storage = FakeStorage({"source/policy.txt": b"EasyShare legacy open behavior document."})
    services = make_services(tmp_path, storage)
    app = create_app(services)
    with TestClient(app) as client:
        assert client.get("/health").status_code == 200
        assert client.post("/query", json={"question": "legacy"}).status_code == 200
