from __future__ import annotations

import time

from fastapi.testclient import TestClient

from app.main import create_app
from tests.helpers import FakeStorage, make_services


def _wait_for_terminal(client: TestClient, job_id: str) -> dict:
    for _ in range(100):
        response = client.get(f"/jobs/{job_id}")
        assert response.status_code == 200
        payload = response.json()
        if payload["status"] in {"completed", "failed"}:
            return payload
        time.sleep(0.01)
    raise AssertionError("job did not finish in time")


def test_process_query_idempotency_and_artifacts(tmp_path) -> None:
    storage = FakeStorage({"source/guide.txt": b"EasyShare supports document cleaning and indexing."})
    services = make_services(tmp_path, storage)
    app = create_app(services)

    with TestClient(app) as client:
        payload = {
            "file_id": "file-1",
            "version_id": "v1",
            "object_key": "source/guide.txt",
        }
        response = client.post("/documents/process", json=payload)
        assert response.status_code == 202
        job_id = response.json()["id"]
        completed = _wait_for_terminal(client, job_id)
        assert completed["status"] == "completed"

        duplicate = client.post("/documents/process", json=payload)
        assert duplicate.status_code == 202
        assert duplicate.json()["id"] == job_id

        manifest = client.get("/documents/file-1/versions/v1/artifacts")
        assert manifest.status_code == 200
        assert manifest.json()["file_id"] == "file-1"
        clean = client.get("/documents/file-1/versions/v1/artifacts/clean.md")
        assert clean.status_code == 200
        assert "document cleaning" in clean.text

        query = client.post("/query", json={"question": "What is supported?", "doc_ids": ["file-1"]})
        assert query.status_code == 200
        assert query.json()["contexts"]


class _StubGenerator:
    def generate(self, question: str, contexts: list[dict]) -> dict:
        refs = "".join(f"[{index + 1}]" for index in range(len(contexts)))
        return {
            "answer": f"根据资料回答：{question} {refs}",
            "sources": [{"doc_id": context.get("doc_id"), "score": context.get("score")} for context in contexts],
        }


def test_query_citations_are_traceable_to_artifacts(tmp_path) -> None:
    storage = FakeStorage({"source/policy.txt": b"EasyShare requires traceable citations."})
    services = make_services(tmp_path, storage)
    app = create_app(services)

    with TestClient(app) as client:
        submitted = client.post(
            "/documents/process",
            json={"file_id": "file-9", "version_id": "v3", "object_key": "source/policy.txt"},
        )
        _wait_for_terminal(client, submitted.json()["id"])

        degraded = client.post("/query", json={"question": "traceable citations?"})
        assert degraded.status_code == 200
        payload = degraded.json()
        assert payload["answer"].startswith("（未配置 LLM")
        context = payload["contexts"][0]
        assert context["file_id"] == "file-9"
        assert context["version_id"] == "v3"
        assert context["filename"] == "policy.txt"
        artifact = client.get(
            f"/documents/{context['file_id']}/versions/{context['version_id']}/artifacts/clean.md"
        )
        assert artifact.status_code == 200
        assert "traceable citations" in artifact.text

        services.generator = _StubGenerator()
        generated = client.post("/query", json={"question": "traceable citations?"})
        assert generated.status_code == 200
        assert generated.json()["answer"].startswith("根据资料回答：")
        assert generated.json()["sources"][0]["doc_id"] == "file-9"
        assert generated.json()["contexts"][0]["version_id"] == "v3"


def test_api_validation_not_found_and_retry_conflict(tmp_path) -> None:
    services = make_services(tmp_path, FakeStorage())
    app = create_app(services)
    with TestClient(app) as client:
        invalid = client.post(
            "/documents/process",
            json={"file_id": "../bad", "version_id": "v1", "object_key": "a.txt"},
        )
        assert invalid.status_code == 422
        assert client.get("/jobs/missing").status_code == 404
        assert client.get("/documents/file-1/versions/v1/artifacts").status_code == 404

        response = client.post(
            "/documents/process",
            json={"file_id": "file-1", "version_id": "v1", "object_key": "missing.txt"},
        )
        failed = _wait_for_terminal(client, response.json()["id"])
        assert failed["status"] == "failed"

        retry = client.post(f"/jobs/{failed['id']}/retry")
        assert retry.status_code == 202
        failed_again = _wait_for_terminal(client, failed["id"])
        assert failed_again["retry_count"] == 1

        services.storage.objects["ok.txt"] = b"ok"
        success = client.post(
            "/documents/process",
            json={"file_id": "file-2", "version_id": "v1", "object_key": "ok.txt"},
        )
        success_job = _wait_for_terminal(client, success.json()["id"])
        assert success_job["status"] == "completed"
        assert client.post(f"/jobs/{success_job['id']}/retry").status_code == 409
