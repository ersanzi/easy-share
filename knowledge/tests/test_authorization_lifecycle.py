"""授权状态变化的 HTTP 回归；仅替换远端控制面与存储。"""
import io
import json
import urllib.request

import pytest
from fastapi.testclient import TestClient

from app.auth.sso import ControlPlaneSSO
from app.main import create_app
from tests.helpers import FakeStorage, make_services
from tests.test_managed_acl_api import _control_plane, _wait


@pytest.fixture
def enterprise(tmp_path, monkeypatch):
    services = make_services(tmp_path, FakeStorage(), config_overrides={"auth_enabled": True})
    services.sso = ControlPlaneSSO("http://control.test", client_id="fixture", users=services.users)
    state = {"online": True, "admin": False, "permission": {"knowledge": True, "meeting": True, "adminScope": "all"}}

    def remote(request, **kwargs):
        if not state["online"]:
            raise OSError("fixture unavailable")
        if request.full_url.endswith("getInfo"):
            payload = {"code": 200, "data": {"user": {"userId": "71", "userName": "alice", "deptId": "10"},
                       "roles": ["superadmin"] if state["admin"] else [], "permissions": []}}
        else:
            permission = state["permission"]
            if isinstance(permission, Exception):
                raise permission
            payload = {"code": 200, "data": permission}
        return io.BytesIO(json.dumps(payload).encode())

    monkeypatch.setattr(urllib.request, "urlopen", remote)
    with TestClient(create_app(services)) as client:
        yield client, services, state, {"Authorization": "Bearer fixture-sso"}


@pytest.mark.parametrize("permission", [OSError("offline"), {}, None, [], {"knowledge": "false"}, {"knowledge": 1}])
def test_permission_failure_or_missing_boolean_never_grants(enterprise, permission):
    client, _, state, headers = enterprise
    state["permission"] = permission
    assert client.get("/documents", headers=headers).status_code in (401, 403)


@pytest.mark.parametrize("change", ["revoke", "offline"])
def test_warm_session_does_not_reuse_previous_grant(enterprise, change):
    client, _, state, headers = enterprise
    assert client.get("/documents", headers=headers).status_code == 200
    if change == "revoke":
        state["permission"]["knowledge"] = False
    else:
        state["online"] = False
    assert client.get("/documents", headers=headers).status_code in (401, 403)


def test_knowledge_job_routes_enforce_feature_before_read_or_retry(enterprise):
    client, _, state, headers = enterprise
    state["permission"]["knowledge"] = False
    assert client.get("/jobs/unknown", headers=headers).status_code == 403
    assert client.post("/jobs/unknown/retry", headers=headers).status_code == 403


def test_enterprise_mode_rejects_local_session_and_password_fallback(enterprise):
    client, services, state, _ = enterprise
    services.users.create_user("localadmin", "fixture-password", role="admin")
    token, _ = services.users.issue_token("localadmin")
    state["online"] = False
    assert client.get("/documents", headers={"Authorization": f"Bearer {token}"}).status_code == 401
    assert client.post("/auth/login", json={"username": "localadmin", "password": "fixture-password"}).status_code == 503
    for endpoint in ("bootstrap", "users"):
        assert client.post(f"/auth/{endpoint}", json={"username": "new", "password": "fixture-password"}).status_code == 403


def test_enterprise_mode_requires_identity_even_if_legacy_auth_switch_is_off(enterprise):
    client, services, _, _ = enterprise
    services.config.auth_enabled = False
    assert client.get("/documents").status_code == 401


def test_sso_admin_demotion_does_not_inherit_local_profile_role(enterprise):
    client, _, state, headers = enterprise
    state["admin"] = True
    assert client.get("/auth/me", headers=headers).json()["role"] == "admin"
    state["admin"] = False
    assert client.get("/auth/users", headers=headers).status_code == 403
    assert client.get("/auth/me", headers=headers).json()["role"] == "member"


@pytest.mark.parametrize("path", ["/debug/document/secret", "/lab"])
def test_enterprise_mode_has_no_loopback_diagnostic_read_bypass(enterprise, path):
    client, services, _, headers = enterprise
    services.storage.objects["source/secret.txt"] = b"private policy secret"
    job = client.post("/documents/process", json={"file_id": "secret", "version_id": "v1", "object_key": "source/secret.txt"}, headers=headers)
    _wait(client, job.json(), headers)
    assert client.get("/documents/secret/versions/v1/view", headers=headers).status_code == 200
    assert client.get(path, headers=headers).status_code == 404


def test_meeting_archive_requires_knowledge_feature_too(enterprise):
    client, _, state, headers = enterprise
    state["permission"]["knowledge"] = False
    assert client.post("/meetings/fixture/archive", headers=headers).status_code == 403


def test_stats_exposes_document_identifiers_only_to_current_admin(enterprise):
    client, _, state, headers = enterprise
    assert client.get("/stats").status_code == 401
    assert client.get("/stats", headers=headers).status_code == 403
    state["admin"] = True
    assert client.get("/stats", headers=headers).status_code == 200
    state["admin"] = False
    assert client.get("/stats", headers=headers).status_code == 403


@pytest.mark.parametrize("mode", ["search", "ask"])
def test_revocation_during_retrieval_removes_contexts_without_llm(tmp_path, monkeypatch, mode):
    services = make_services(tmp_path, FakeStorage({"shared/policy.txt": b"policy secret travel"}))
    state = _control_plane(monkeypatch, services)
    headers = {"Authorization": "Bearer fixture-sso"}
    with TestClient(create_app(services)) as client:
        _wait(client, client.post("/documents/process-drive", json={"file_id": "42", "version_id": "3"}, headers=headers).json(), headers)
        retrieve = services.orchestrator.retrieve

        def revoke_after_retrieve(*args, **kwargs):
            result = retrieve(*args, **kwargs)
            assert result.contexts
            state["role"] = None
            return result

        monkeypatch.setattr(services.orchestrator, "retrieve", revoke_after_retrieve)
        response = client.post("/query", json={"question": "policy", "mode": mode}, headers=headers)
        assert response.status_code == 200
        assert response.json()["contexts"] == []

@pytest.mark.parametrize("permission", [OSError("permission offline"), {}, {"knowledge": True, "adminScope": "none"}])
def test_sso_stale_admin_claim_requires_live_management_scope(enterprise, permission):
    client, _, state, headers = enterprise
    state["admin"] = True
    state["permission"] = permission
    assert client.get("/auth/users", headers=headers).status_code == 403
    assert client.get("/auth/me", headers=headers).json()["role"] == "member"


def test_enterprise_password_login_returns_only_verified_control_plane_token(tmp_path, monkeypatch):
    services = make_services(tmp_path, FakeStorage(), config_overrides={"auth_enabled": True})
    services.sso = ControlPlaneSSO("http://control.test", client_id="fixture", users=services.users)
    seen = []

    def remote(request, **kwargs):
        seen.append(request.full_url)
        if request.full_url.endswith("/auth/login"):
            credentials = json.loads(request.data)
            assert credentials == {"username": "alice", "password": "fixture-password", "clientId": "fixture", "grantType": "password"}
            payload = {"code": 200, "data": {"access_token": "verified-enterprise-token", "expire_in": 3600}}
        elif request.full_url.endswith("getInfo"):
            assert request.get_header("Authorization") == "Bearer verified-enterprise-token"
            payload = {"code": 200, "data": {"user": {"userId": "71", "userName": "alice"}, "roles": [], "permissions": []}}
        else:
            payload = {"code": 200, "data": {"userId": "71", "knowledge": True, "adminScope": "none"}}
        return io.BytesIO(json.dumps(payload).encode())

    monkeypatch.setattr(urllib.request, "urlopen", remote)
    with TestClient(create_app(services)) as client:
        response = client.post("/auth/login", json={"username": "alice", "password": "fixture-password"})
        assert response.status_code == 200, response.text
        assert response.json()["token"] == "verified-enterprise-token"
        assert services.users.resolve_token(response.json()["token"]) is None
        assert services.users.verify("alice", "fixture-password") is None
        assert client.get("/documents", headers={"Authorization": "Bearer verified-enterprise-token"}).status_code == 200
    assert seen[0] == "http://control.test/auth/login"


@pytest.mark.parametrize("outcome, expected", [(200, 200), (401, 200), (500, 503), ("offline", 503)])
def test_enterprise_logout_revokes_presented_token_and_reports_failure(tmp_path, monkeypatch, outcome, expected):
    services = make_services(tmp_path, FakeStorage(), config_overrides={"auth_enabled": True})
    services.sso = ControlPlaneSSO("http://control.test", client_id="fixture", users=services.users)
    seen = []
    def remote(request, **kwargs):
        seen.append(request.full_url)
        assert request.full_url == "http://control.test/auth/logout"
        assert request.get_method() == "POST"
        assert request.get_header("Authorization") == "Bearer only-this-token"
        assert request.get_header("Clientid") == "fixture"
        if outcome == "offline":
            raise OSError("fixture unavailable")
        return io.BytesIO(json.dumps({"code": outcome}).encode())
    monkeypatch.setattr(urllib.request, "urlopen", remote)
    with TestClient(create_app(services)) as client:
        result = client.post("/auth/logout", headers={"Authorization": "Bearer only-this-token"})
        assert result.status_code == expected, result.text
        assert seen == ["http://control.test/auth/logout"]
        assert client.post("/auth/logout").status_code == 401


def test_standalone_logout_revokes_only_presented_local_token(tmp_path):
    services = make_services(tmp_path, FakeStorage(), config_overrides={"auth_enabled": True})
    services.users.create_user("alice", "fixture-password")
    token, _ = services.users.issue_token("alice")
    other, _ = services.users.issue_token("alice")
    with TestClient(create_app(services)) as client:
        assert client.post("/auth/logout", headers={"Authorization": f"Bearer {token}"}).status_code == 200
        assert services.users.resolve_token(token) is None
        assert services.users.resolve_token(other) is not None
        assert client.get("/documents", headers={"Authorization": f"Bearer {token}"}).status_code == 401


@pytest.mark.parametrize("login_result, expected", [(OSError("offline"), 503), ({"code":401}, 401),
    ({"code":200,"data":{}}, 502), ({"code":200,"data":{"access_token":"invalid"}}, 401)])
def test_enterprise_login_failure_never_issues_or_accepts_local_identity(tmp_path, monkeypatch, login_result, expected):
    services = make_services(tmp_path, FakeStorage(), config_overrides={"auth_enabled": True})
    services.users.create_user("alice", "fixture-password", role="admin")
    services.sso = ControlPlaneSSO("http://control.test", client_id="fixture", users=services.users)
    def remote(request, **kwargs):
        if request.full_url.endswith("/auth/login"):
            if isinstance(login_result, Exception): raise login_result
            return io.BytesIO(json.dumps(login_result).encode())
        return io.BytesIO(json.dumps({"code":401}).encode())
    monkeypatch.setattr(urllib.request,"urlopen",remote)
    with TestClient(create_app(services)) as client:
        result = client.post("/auth/login",json={"username":"alice","password":"fixture-password"})
        assert result.status_code == expected, result.text
        assert "token" not in result.json()
