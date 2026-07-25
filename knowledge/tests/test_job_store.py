from __future__ import annotations

import pytest

from app.jobs.store import JobStore


def _create(store: JobStore, *, force: bool = False):
    return store.create_or_get(
        file_id="file-1",
        version_id="v1",
        object_key="source/demo.txt",
        filename="demo.txt",
        force=force,
    )


def test_idempotency_and_force(tmp_path) -> None:
    store = JobStore(str(tmp_path / "jobs.db"))
    try:
        first, first_created = _create(store)
        second, second_created = _create(store)
        forced, forced_created = _create(store, force=True)
        assert first_created is True
        assert second_created is False
        assert second.id == first.id
        assert forced_created is True
        assert forced.id != first.id
    finally:
        store.close()


def test_state_transitions_retry_and_recovery(tmp_path) -> None:
    path = tmp_path / "jobs.db"
    store = JobStore(str(path))
    job, _ = _create(store)
    claimed = store.claim(job.id)
    assert claimed is not None and claimed.status == "processing"
    store.update_progress(job.id, stage="parsing", progress=30)
    assert store.get(job.id).progress == 30
    store.fail(job.id, error_code="ParseError", error_message="bad file")
    failed = store.get(job.id)
    assert failed.status == "failed"
    retried = store.retry(job.id)
    assert retried.status == "queued"
    assert retried.retry_count == 1
    claimed = store.claim(job.id)
    assert claimed is not None
    store.close()

    reopened = JobStore(str(path))
    try:
        recovered = reopened.recover_incomplete()
        assert [item.id for item in recovered] == [job.id]
        assert reopened.get(job.id).stage == "recovered"
        claimed = reopened.claim(job.id)
        assert claimed is not None
        reopened.complete(job.id, {"ok": True})
        completed = reopened.get(job.id)
        assert completed.status == "completed"
        assert completed.progress == 100
        with pytest.raises(ValueError, match="failed"):
            reopened.retry(job.id)
    finally:
        reopened.close()


def test_state_updates_reject_non_processing_job(tmp_path) -> None:
    store = JobStore(str(tmp_path / "jobs.db"))
    try:
        job, _ = _create(store)
        with pytest.raises(ValueError, match="processing"):
            store.update_progress(job.id, stage="parsing", progress=30)
        with pytest.raises(ValueError, match="processing"):
            store.complete(job.id, {})
        with pytest.raises(ValueError, match="processing"):
            store.fail(job.id, error_code="Error", error_message="failed")
    finally:
        store.close()
