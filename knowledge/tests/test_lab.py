from __future__ import annotations

import time

from fastapi.testclient import TestClient

from app.main import create_app
from tests.golden.builders import build_docx_policy
from tests.helpers import FakeStorage, make_services


def _wait_for_terminal(client: TestClient, job_id: str) -> dict:
    for _ in range(150):
        response = client.get(f"/jobs/{job_id}")
        assert response.status_code == 200
        payload = response.json()
        if payload["status"] in {"completed", "failed"}:
            return payload
        time.sleep(0.01)
    raise AssertionError("job did not finish in time")


def test_lab_page_marks_product_boundary(tmp_path) -> None:
    app = create_app(make_services(tmp_path))
    with TestClient(app) as client:
        response = client.get("/lab")
        assert response.status_code == 200
        assert response.headers["content-type"].startswith("text/html")
        assert "仅供本地测试" in response.text
        assert "不是 EasyShare 客户端功能" in response.text
        assert "不代表最终产品界面" in response.text
        assert client.get("/lab/assets/lab.css").status_code == 200
        assert client.get("/lab/assets/lab.js").status_code == 200


def test_lab_upload_txt_runs_pipeline_and_sanitizes_filename(tmp_path) -> None:
    storage = FakeStorage()
    app = create_app(make_services(tmp_path, storage))
    with TestClient(app) as client:
        response = client.post(
            "/lab/api/uploads",
            files={"file": ("../../guide.txt", b"EasyShare local lab document cleaning.", "text/plain")},
        )
        assert response.status_code == 202
        submitted = response.json()
        assert submitted["filename"] == "guide.txt"
        assert submitted["object_key"].endswith("/guide.txt")
        assert ".." not in submitted["object_key"]
        assert storage.objects[submitted["object_key"]] == b"EasyShare local lab document cleaning."
        assert storage.content_types[submitted["object_key"]] == "text/plain"

        completed = _wait_for_terminal(client, submitted["id"])
        assert completed["status"] == "completed"
        base = f"/documents/{completed['file_id']}/versions/{completed['version_id']}/artifacts"
        assert client.get(base).status_code == 200
        assert "document cleaning" in client.get(f"{base}/clean.md").text
        assert client.get(f"{base}/document.json").status_code == 200
        assert client.get(f"{base}/manifest.json").status_code == 200


def test_lab_upload_office_docx_and_recent_jobs(tmp_path) -> None:
    storage = FakeStorage()
    app = create_app(make_services(tmp_path, storage))
    with TestClient(app) as client:
        first = client.post(
            "/lab/api/uploads",
            files={"file": ("first.txt", b"first local lab document", "text/plain")},
        )
        assert first.status_code == 202
        _wait_for_terminal(client, first.json()["id"])

        second = client.post(
            "/lab/api/uploads",
            files={
                "file": (
                    "policy.docx",
                    build_docx_policy(),
                    "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
                )
            },
        )
        assert second.status_code == 202
        completed = _wait_for_terminal(client, second.json()["id"])
        assert completed["status"] == "completed"

        jobs = client.get("/lab/api/jobs?limit=1")
        assert jobs.status_code == 200
        assert len(jobs.json()) == 1
        assert jobs.json()[0]["id"] == second.json()["id"]


def test_lab_upload_validation_and_size_limit(tmp_path) -> None:
    services = make_services(tmp_path, FakeStorage())
    services.config.max_source_bytes = 4
    app = create_app(services)
    with TestClient(app) as client:
        unsupported = client.post(
            "/lab/api/uploads",
            files={"file": ("malware.exe", b"MZ", "application/octet-stream")},
        )
        assert unsupported.status_code == 415

        too_large = client.post(
            "/lab/api/uploads",
            files={"file": ("large.txt", b"12345", "text/plain")},
        )
        assert too_large.status_code == 413

        invalid_id = client.post(
            "/lab/api/uploads",
            data={"file_id": "../escape"},
            files={"file": ("safe.txt", b"ok", "text/plain")},
        )
        assert invalid_id.status_code == 422


def test_lab_can_be_disabled_and_rejects_non_loopback_clients(tmp_path) -> None:
    disabled_services = make_services(tmp_path / "disabled", FakeStorage())
    disabled_services.config.local_lab_enabled = False
    disabled_app = create_app(disabled_services)
    with TestClient(disabled_app) as client:
        assert client.get("/lab").status_code == 404
        assert client.get("/lab/api/jobs").status_code == 404

    remote_app = create_app(make_services(tmp_path / "remote", FakeStorage()))
    with TestClient(remote_app, client=("192.0.2.25", 50000)) as client:
        assert client.get("/lab").status_code == 403
        assert client.get("/lab/api/jobs").status_code == 403
