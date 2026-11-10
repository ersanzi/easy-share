"""控制面 SSO 信任链路验证：introspection 头与解析、JIT 建档、中间件放行与缓存。

真实 RuoYi 的请求头与响应形状由 Go 客户端（internal/account/client.go）在
生产验证过；此处一是用本地 HTTP 桩验证知识服务侧发的头与解析口径，二是用
桩 introspection 验证中间件语义——本地令牌优先、未命中回查、缓存只回查一次、
无效令牌仍 401、部门跟随控制面而 role 不被覆盖。
"""

import json
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

from fastapi.testclient import TestClient

from app.auth.sso import ControlPlaneSSO
from app.main import create_app
from tests.helpers import FakeStorage, make_services


def _sso_app(tmp_path):
    storage = FakeStorage({"source/policy.txt": b"EasyShare knowledge sso policy content."})
    services = make_services(tmp_path, storage, config_overrides={"auth_enabled": True})
    services.sso = ControlPlaneSSO(
        "http://control-plane.test", client_id="test-client", users=services.users
    )
    return create_app(services), services


def _stub_introspect(services, payload, calls=None):
    """把 introspection 换成桩（返回固定身份并可选计数）。"""

    def fake(token):
        if calls is not None:
            calls.append(token)
        return payload

    services.sso._introspect = fake


def test_sso_token_grants_access_and_jit_provisions(tmp_path) -> None:
    app, services = _sso_app(tmp_path)
    _stub_introspect(services, {"username": "wang", "is_admin": False, "dept": "103"})
    with TestClient(app) as client:
        headers = {"Authorization": "Bearer ctrl-token-1"}
        me = client.get("/auth/me", headers=headers)
        assert me.status_code == 200
        assert me.json() == {"username": "wang", "role": "member", "dept": "103"}

        # JIT 建档落库（owner/部门过滤与用户列表依赖本地行）
        assert services.users.get_user("wang") == {
            "username": "wang", "role": "member", "dept": "103",
        }

        # 保护前缀放行（auth_enabled 下匿名仍 401）
        anonymous = client.post(
            "/documents/process",
            json={"file_id": "f1", "version_id": "v1", "object_key": "source/policy.txt"},
        )
        assert anonymous.status_code == 401
        submitted = client.post(
            "/documents/process",
            json={"file_id": "f1", "version_id": "v1", "object_key": "source/policy.txt"},
            headers=headers,
        )
        assert submitted.status_code == 202


def test_sso_caches_introspection(tmp_path) -> None:
    app, services = _sso_app(tmp_path)
    calls: list[str] = []
    _stub_introspect(services, {"username": "wang", "is_admin": False, "dept": "103"}, calls)
    with TestClient(app) as client:
        headers = {"Authorization": "Bearer ctrl-token-1"}
        assert client.get("/auth/me", headers=headers).status_code == 200
        assert client.get("/auth/me", headers=headers).status_code == 200
    assert calls == ["ctrl-token-1"]  # 第二次命中缓存，不再回查控制面


def test_sso_invalid_token_stays_401(tmp_path) -> None:
    app, services = _sso_app(tmp_path)
    _stub_introspect(services, None)
    with TestClient(app) as client:
        response = client.get(
            "/auth/me", headers={"Authorization": "Bearer garbage-token"}
        )
        assert response.status_code == 401


def test_local_login_still_works_alongside_sso(tmp_path) -> None:
    app, services = _sso_app(tmp_path)
    _stub_introspect(services, {"username": "wang", "is_admin": False, "dept": "103"})
    with TestClient(app) as client:
        assert client.post("/auth/bootstrap", json={"username": "boss", "password": "secret-pass-1"}).status_code == 200
        login = client.post("/auth/login", json={"username": "boss", "password": "secret-pass-1"})
        assert login.status_code == 200
        me = client.get("/auth/me", headers={"Authorization": f"Bearer {login.json()['token']}"})
        assert me.json()["username"] == "boss"  # 本地令牌优先，不走 SSO 回查


def test_sso_dept_syncs_but_local_role_preserved(tmp_path) -> None:
    app, services = _sso_app(tmp_path)
    _stub_introspect(services, {"username": "wang", "is_admin": False, "dept": "103"})
    with TestClient(app) as client:
        assert client.get("/auth/me", headers={"Authorization": "Bearer t1"}).status_code == 200
        # 控制面说 wang 升了部门且是管理员：dept 跟随，role 保留本地值（管理页语义）
        services.sso._cache.clear()
        _stub_introspect(services, {"username": "wang", "is_admin": True, "dept": "105"})
        me = client.get("/auth/me", headers={"Authorization": "Bearer t2"})
        assert me.json() == {"username": "wang", "role": "member", "dept": "105"}


class _RuoYiStub(BaseHTTPRequestHandler):
    """最小 RuoYi 桩：getInfo 与功能权限两个端点，记录请求头，按 mode 应答。"""

    mode = "ok"  # ok / unauthorized / bad_code / no_permission_api
    received: list[dict] = []

    def do_GET(self) -> None:  # noqa: N802——http.server 命名约定
        _RuoYiStub.received.append({
            "path": self.path,
            "auth": self.headers.get("Authorization"),
            "clientid": self.headers.get("clientid"),
        })
        if _RuoYiStub.mode == "unauthorized":
            self.send_response(401)
            self.end_headers()
            return
        if self.path == "/easyshare/permission/mine":
            if _RuoYiStub.mode == "no_permission_api":
                self.send_response(404)
                self.end_headers()
                return
            body = json.dumps({
                "code": 200,
                "msg": "ok",
                "data": {
                    "userId": 1, "knowledge": False, "meeting": True,
                    "drive": True, "shared": True, "plugin": True,
                },
            }).encode("utf-8")
        else:
            code = 200 if _RuoYiStub.mode != "bad_code" else 500
            body = json.dumps({
                "code": code,
                "msg": "ok" if code == 200 else "err",
                "data": {
                    "user": {"userId": "1", "userName": "wang", "nickName": "小王", "deptId": "103"},
                    "roles": ["superadmin"],
                    "permissions": [],
                },
            }).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args) -> None:  # 静默测试输出
        pass


def test_introspect_sends_headers_and_parses_ruoyi_wrapper(tmp_path) -> None:
    from app.auth.store import UserStore

    server = HTTPServer(("127.0.0.1", 0), _RuoYiStub)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    _RuoYiStub.mode = "ok"
    _RuoYiStub.received = []
    try:
        users = UserStore(str(tmp_path / "auth.db"))
        sso = ControlPlaneSSO(
            f"http://127.0.0.1:{server.server_port}", client_id="cid-123", users=users
        )
        info = sso._introspect("real-token")
        # 身份与功能权限各回查一次，头一致
        assert [item["path"] for item in _RuoYiStub.received] == [
            "/system/user/getInfo", "/easyshare/permission/mine",
        ]
        assert all(item["auth"] == "Bearer real-token" for item in _RuoYiStub.received)
        assert all(item["clientid"] == "cid-123" for item in _RuoYiStub.received)
        assert info == {
            "username": "wang",
            "is_admin": True,
            "dept": "103",
            "features": {
                "knowledge": False, "meeting": True,
                "drive": True, "shared": True, "plugin": True,
            },
        }

        _RuoYiStub.mode = "unauthorized"
        assert sso._introspect("real-token") is None
        _RuoYiStub.mode = "bad_code"
        assert sso._introspect("real-token") is None
    finally:
        _RuoYiStub.mode = "ok"
        server.shutdown()
        server.server_close()


def test_permission_endpoint_missing_falls_open(tmp_path) -> None:
    """控制面还没升级到带权限端点：身份有效即放行，不把所有人挡在门外。"""
    from app.auth.store import UserStore

    server = HTTPServer(("127.0.0.1", 0), _RuoYiStub)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    _RuoYiStub.mode = "no_permission_api"
    try:
        users = UserStore(str(tmp_path / "auth.db"))
        sso = ControlPlaneSSO(
            f"http://127.0.0.1:{server.server_port}", client_id="cid-123", users=users
        )
        info = sso._introspect("real-token")
        assert info is not None
        assert info["features"] == {
            "knowledge": True, "meeting": True, "drive": True,
            "shared": True, "plugin": True,
        }
    finally:
        _RuoYiStub.mode = "ok"
        server.shutdown()
        server.server_close()


def test_disabled_feature_blocks_knowledge_only(tmp_path) -> None:
    app, services = _sso_app(tmp_path)
    _stub_introspect(services, {
        "username": "wang", "is_admin": False, "dept": "103",
        "features": {"knowledge": False, "meeting": True, "drive": True,
                     "shared": True, "plugin": True},
    })
    with TestClient(app) as client:
        headers = {"Authorization": "Bearer ctrl-token-off"}
        blocked = client.post(
            "/documents/process",
            json={"file_id": "f1", "version_id": "v1", "object_key": "source/policy.txt"},
            headers=headers,
        )
        assert blocked.status_code == 403
        assert "管理员" in blocked.json()["detail"]

        # 同一令牌的其它功能不受牵连：会议入口与 /auth/me 照常（测试环境会议存储不可用，只要求不是 403）
        assert client.get("/auth/me", headers=headers).status_code == 200
        assert client.get("/meetings", headers=headers).status_code != 403


def test_meeting_feature_disabled_blocks_meetings(tmp_path) -> None:
    app, services = _sso_app(tmp_path)
    _stub_introspect(services, {
        "username": "wang", "is_admin": False, "dept": "103",
        "features": {"knowledge": True, "meeting": False, "drive": True,
                     "shared": True, "plugin": True},
    })
    with TestClient(app) as client:
        headers = {"Authorization": "Bearer ctrl-token-meeting-off"}
        assert client.get("/meetings", headers=headers).status_code == 403
        assert client.get("/query", headers=headers).status_code != 403


def test_local_token_not_governed_by_matrix(tmp_path) -> None:
    """本地账号（不经控制面）没有 features，功能矩阵不管它——行为与引入前一致。"""
    app, services = _sso_app(tmp_path)
    with TestClient(app) as client:
        client.post("/auth/bootstrap", json={"username": "boss", "password": "secret-pass-1"})
        login = client.post("/auth/login", json={"username": "boss", "password": "secret-pass-1"})
        token = login.json()["token"]
        response = client.post(
            "/documents/process",
            json={"file_id": "f1", "version_id": "v1", "object_key": "source/policy.txt"},
            headers={"Authorization": f"Bearer {token}"},
        )
        assert response.status_code == 202
