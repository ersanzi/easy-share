from __future__ import annotations

import json

import pytest

from app.jobs.store import ProcessingJob
from app.pipeline.service import artifact_keys
from tests.helpers import FakeStorage, make_services


def _job(version_id: str = "v1") -> ProcessingJob:
    return ProcessingJob(
        id=f"job-{version_id}",
        file_id="file-1",
        version_id=version_id,
        object_key=f"source/{version_id}/note.txt",
        filename="note.txt",
        status="processing",
        stage="starting",
        progress=1,
        retry_count=0,
        error_code=None,
        error_message=None,
        result=None,
        created_at="2026-07-25T00:00:00+00:00",
        updated_at="2026-07-25T00:00:00+00:00",
        started_at="2026-07-25T00:00:00+00:00",
        finished_at=None,
    )


def test_pipeline_writes_artifacts_and_replaces_current_version(tmp_path) -> None:
    storage = FakeStorage(
        {
            "source/v1/note.txt": b"first version knowledge",
            "source/v2/note.txt": b"second version knowledge",
        }
    )
    services = make_services(tmp_path, storage)
    reports: list[tuple[str, int]] = []
    try:
        manifest_v1 = services.pipeline.process(_job("v1"), lambda stage, progress: reports.append((stage, progress)))
        keys_v1 = artifact_keys("file-1", "v1")
        assert set(keys_v1.values()).issubset(storage.objects)
        assert manifest_v1["version_id"] == "v1"
        assert json.loads(storage.objects[keys_v1["document"]])["file_id"] == "file-1"
        assert services.vector_store.records[0]["version_id"] == "v1"

        services.pipeline.process(_job("v2"), lambda *_: None)
        assert services.vector_store.records
        assert {item["version_id"] for item in services.vector_store.records} == {"v2"}
        assert reports[0] == ("downloading", 10)
        assert reports[-1] == ("finalizing", 97)
    finally:
        services.job_store.close()


def test_pipeline_does_not_succeed_when_artifact_write_fails(tmp_path) -> None:
    storage = FakeStorage({"source/v1/note.txt": b"content"}, fail_on_write="manifest.json")
    services = make_services(tmp_path, storage)
    try:
        with pytest.raises(OSError, match="simulated"):
            services.pipeline.process(_job(), lambda *_: None)
    finally:
        services.job_store.close()
