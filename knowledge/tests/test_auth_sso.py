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
    """最小 RuoYi /system/user/getInfo 桩：记录请求头，按 mode 应答。"""

    mode = "ok"  # ok / unauthorized / bad_code

    def do_GET(self) -> None:  # noqa: N802——http.server 命名约定
        _RuoYiStub.received = {
            "path": self.path,
            "auth": self.headers.get("Authorization"),
            "clientid": self.headers.get("clientid"),
        }
        if _RuoYiStub.mode == "unauthorized":
            self.send_response(401)
            self.end_headers()
            return
        code = 200 if _RuoYiStub.mode == "ok" else 500
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
    try:
        users = UserStore(str(tmp_path / "auth.db"))
        sso = ControlPlaneSSO(
            f"http://127.0.0.1:{server.server_port}", client_id="cid-123", users=users
        )
        info = sso._introspect("real-token")
        assert _RuoYiStub.received == {
            "path": "/system/user/getInfo",
            "auth": "Bearer real-token",
            "clientid": "cid-123",
        }
        assert info == {"username": "wang", "is_admin": True, "dept": "103"}

        _RuoYiStub.mode = "unauthorized"
        assert sso._introspect("real-token") is None
        _RuoYiStub.mode = "bad_code"
        assert sso._introspect("real-token") is None
    finally:
        server.shutdown()
        server.server_close()
