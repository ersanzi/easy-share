"""知识公开 API 的权限闭环；只替换控制面 HTTP 与对象存储边界。"""
import io
import json
import time
import hashlib
import urllib.request

from fastapi.testclient import TestClient

from app.auth.sso import ControlPlaneSSO
from app.main import create_app
from tests.helpers import FakeStorage, make_services


def test_managed_source_never_falls_back_to_anonymous_shared(tmp_path):
    services = make_services(tmp_path, FakeStorage({"shared/policy.txt": b"secret policy"}))
    with TestClient(create_app(services)) as client:
        response = client.post("/documents/process", json={
            "file_id": "lab-name", "version_id": "v1", "object_key": "shared/policy.txt",
        })
        assert response.status_code == 403


def _control_plane(monkeypatch, services, content=b"policy secret travel"):
    state = {"role": "download", "version": "3", "key": "shared/policy.txt", "calls": [], "fail": False}
    services.sso = ControlPlaneSSO("http://control-plane.test", client_id="knowledge-client", users=services.users)
    def urlopen(request, **kwargs):
        state["calls"].append((request.full_url, request.headers, json.loads(request.data) if request.data else None))
        if request.full_url.endswith("getInfo"):
            data = {"user": {"userId": "2", "userName": "reader", "deptId": "10"}, "roles": [], "permissions": []}
        elif request.full_url.endswith("/mine"):
            data = {"knowledge": True}
        elif state["fail"]:
            raise OSError("control plane unavailable")
        elif request.full_url.endswith("/bind"):
            data = {"authority": "ruoyi", "fileId": "42", "versionId": state["version"], "objectKey": state["key"],
                    "bucket": services.config.rustfs_bucket, "sourceSha256": hashlib.sha256(content).hexdigest(),
                    "sourceBytes": len(content), "filename": "policy.txt"}
        else:
            body = json.loads(request.data)
            data = [{"fileId": fid, "read": state["role"] is not None,
                     "download": state["role"] in ("download", "edit", "manage"),
                     "objectKey": state["key"]} for fid in body["fileIds"]]
        return io.BytesIO(json.dumps({"code": 200, "data": data}).encode())
    monkeypatch.setattr(urllib.request, "urlopen", urlopen)
    return state


def _wait(client, job, headers):
    for _ in range(200):
        response = client.get(f"/jobs/{job['id']}", headers=headers)
        assert response.status_code == 200, response.text
        result = response.json()
        if result["status"] in ("completed", "failed"):
            assert result["status"] == "completed", result
            return result
        time.sleep(.01)
    raise AssertionError("processing did not complete")


def test_drive_process_freezes_verified_source_before_worker_and_download(tmp_path, monkeypatch):
    content = b"policy secret travel"
    storage = FakeStorage({"shared/policy.txt": content})
    services = make_services(tmp_path, storage)
    state = _control_plane(monkeypatch, services, content)
    headers = {"Authorization": "Bearer trusted-sso"}
    with TestClient(create_app(services)) as client:
        response = client.post("/documents/process-drive", json={"file_id": "42", "version_id": "3"}, headers=headers)
        assert response.status_code == 202, response.text
        job = _wait(client, response.json(), headers)
        assert job["file_id"] == "drive-42"
        storage.objects["shared/policy.txt"] = b"replaced unrelated body"
        source = client.get(f"/documents/{job['file_id']}/versions/{job['version_id']}/source", headers=headers)
        assert source.status_code == 200, source.text
        assert source.content == content
        view = client.get(f"/documents/{job['file_id']}/versions/{job['version_id']}/view", headers=headers)
        assert view.json()["manifest"]["source_binding"]["file_id"] == "42"
        assert any(body == {"fileId": "42", "expectedVersionId": "3"} for _, _, body in state["calls"])


def test_read_role_searches_and_views_but_cannot_download_source(tmp_path, monkeypatch):
    services = make_services(tmp_path, FakeStorage({"shared/policy.txt": b"policy secret travel"}))
    state = _control_plane(monkeypatch, services)
    headers = {"Authorization": "Bearer trusted-sso"}
    with TestClient(create_app(services)) as client:
        job = _wait(client, client.post("/documents/process-drive", json={"file_id": "42", "version_id": "3"}, headers=headers).json(), headers)
        state["role"] = "read"
        base = f"/documents/{job['file_id']}/versions/{job['version_id']}"
        assert client.get(base + "/view", headers=headers).status_code == 200
        assert client.get(base + "/artifacts/clean.md", headers=headers).status_code == 200
        assert client.get(base + "/source", headers=headers).status_code == 403
        assert client.post("/query", json={"question": "policy", "mode": "search"}, headers=headers).json()["contexts"]
        assert client.get("/documents", headers=headers).json()["documents"][0]["can_download"] is False


def test_revocation_immediately_removes_query_list_view_and_artifacts(tmp_path, monkeypatch):
    services = make_services(tmp_path, FakeStorage({"shared/policy.txt": b"policy secret travel"}))
    state = _control_plane(monkeypatch, services)
    headers = {"Authorization": "Bearer trusted-sso"}
    with TestClient(create_app(services)) as client:
        job = _wait(client, client.post("/documents/process-drive", json={"file_id": "42", "version_id": "3"}, headers=headers).json(), headers)
        assert client.post("/query", json={"question": "policy"}, headers=headers).json()["contexts"]
        state["role"] = None
        assert client.post("/query", json={"question": "policy"}, headers=headers).json()["contexts"] == []
        assert client.get("/documents", headers=headers).json()["documents"] == []
        for suffix in ("view", "source", "artifacts", "artifacts/document.json"):
            assert client.get(f"/documents/{job['file_id']}/versions/{job['version_id']}/{suffix}", headers=headers).status_code == 403
        assert client.get(f"/jobs/{job['id']}", headers=headers).status_code == 403


def test_legacy_managed_manifest_migrates_only_with_matching_server_digest(tmp_path, monkeypatch):
    content = b"policy secret travel"
    storage = FakeStorage({"source/legacy.txt": content, "shared/policy.txt": content})
    services = make_services(tmp_path, storage)
    state = _control_plane(monkeypatch, services)
    headers = {"Authorization": "Bearer trusted-sso"}
    with TestClient(create_app(services)) as client:
        job = _wait(client, client.post("/documents/process", json={"file_id": "legacy", "version_id": "v1", "object_key": "source/legacy.txt"}).json(), {})
        # 模拟升级前已有受管来源、但无控制面绑定的产物和索引。
        key = "derived/legacy/v1/manifest.json"
        manifest = json.loads(storage.objects[key]); manifest["object_key"] = "shared/policy.txt"
        storage.objects[key] = json.dumps(manifest).encode()
        records = services.vector_store.get_doc("legacy")
        for record in records: record["metadata"]["object_key"] = "shared/policy.txt"
        services.vector_store.replace_doc("legacy", records)
        base = "/documents/legacy/versions/v1"
        view = client.get(base + "/view", headers=headers)
        assert view.status_code == 200, view.text
        assert view.json()["manifest"]["source_binding"]["file_id"] == "42"
        state["key"] = "shared/renamed.txt"
        storage.objects["shared/policy.txt"] = b"unrelated new contents"
        assert client.get(base + "/source", headers=headers).content == content
        state["role"] = None
        assert client.post("/query", json={"question": "policy"}, headers=headers).json()["contexts"] == []


def test_local_same_name_cannot_impersonate_sso_legacy_owner(tmp_path, monkeypatch):
    services = make_services(tmp_path, FakeStorage({"source/policy.txt": b"policy secret travel"}))
    _control_plane(monkeypatch, services)
    services.users.create_user("reader", "password")
    local, _ = services.users.issue_token("reader")
    trusted = {"Authorization": "Bearer trusted-sso"}
    with TestClient(create_app(services)) as client:
        job = _wait(client, client.post("/documents/process", json={"file_id": "sso-private", "version_id": "v1", "object_key": "source/policy.txt"}, headers=trusted).json(), trusted)
        local_headers = {"Authorization": f"Bearer {local}"}
        assert client.get(f"/documents/{job['file_id']}/versions/v1/view", headers=local_headers).status_code == 403
        assert client.post("/query", json={"question": "policy"}, headers=local_headers).json()["contexts"] == []


def test_text_or_untrusted_process_cannot_replace_bound_drive_document(tmp_path, monkeypatch):
    services = make_services(tmp_path, FakeStorage({"shared/policy.txt": b"policy secret travel", "source/mine.txt": b"attacker text"}))
    _control_plane(monkeypatch, services)
    headers = {"Authorization": "Bearer trusted-sso"}
    with TestClient(create_app(services)) as client:
        job = _wait(client, client.post("/documents/process-drive", json={"file_id": "42", "version_id": "3"}, headers=headers).json(), headers)
        response = client.post("/ingest", json={"source": "text", "content": "attacker text", "doc_id": job["file_id"]}, headers=headers)
        assert response.status_code == 403
        response = client.post("/documents/process", json={"file_id": job["file_id"], "version_id": "v999", "object_key": "source/mine.txt", "force": True}, headers=headers)
        assert response.status_code == 403
        assert "attacker" not in client.post("/query", json={"question": "policy"}, headers=headers).text


def test_public_ingestion_cannot_launder_managed_or_derived_source_keys(tmp_path):
    secrets = {"shared/private.txt": b"secret", "users/1/private.txt": b"secret", "derived/drive-42/v3/source": b"secret",
               "./shared/private.txt": b"secret", "source/../shared/private.txt": b"secret"}
    services = make_services(tmp_path, FakeStorage(secrets))
    with TestClient(create_app(services)) as client:
        for key in secrets:
            ingest = client.post("/ingest", json={"source": "rustfs", "key": key})
            assert ingest.status_code == 403, (key, ingest.text)
            process = client.post("/documents/process", json={"file_id": "mine", "version_id": "v1", "object_key": key})
            assert process.status_code == 403, (key, process.text)
