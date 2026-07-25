from __future__ import annotations

import json
import os
from uuid import uuid4

import pytest
from botocore.config import Config
from botocore.exceptions import BotoCoreError, ClientError

from app.jobs.store import ProcessingJob
from app.kb.embedder import HashEmbedder
from app.kb.store import VectorStore
from app.pipeline.service import DocumentPipeline, artifact_keys
from app.storage.rustfs import RustFSStorage
from tests.golden.builders import build_docx_policy

pytestmark = pytest.mark.integration


def _required_environment(name: str) -> str:
    value = os.getenv(name, "").strip()
    if not value:
        pytest.fail(f"启用 RustFS 集成测试后必须设置 {name}")
    return value


def _processing_job(file_id: str, object_key: str) -> ProcessingJob:
    timestamp = "2026-07-25T00:00:00+00:00"
    return ProcessingJob(
        id=f"job-{file_id}",
        file_id=file_id,
        version_id="v1",
        object_key=object_key,
        filename="word-policy.docx",
        status="processing",
        stage="starting",
        progress=1,
        retry_count=0,
        error_code=None,
        error_message=None,
        result=None,
        created_at=timestamp,
        updated_at=timestamp,
        started_at=timestamp,
        finished_at=None,
    )


def test_real_rustfs_document_pipeline(tmp_path) -> None:
    if os.getenv("EASYSHARE_RUSTFS_INTEGRATION") != "1":
        pytest.skip("设置 EASYSHARE_RUSTFS_INTEGRATION=1 后才运行真实 RustFS 测试")

    endpoint = _required_environment("EASYSHARE_RUSTFS_ENDPOINT")
    access_key = _required_environment("EASYSHARE_RUSTFS_ACCESS_KEY")
    secret_key = _required_environment("EASYSHARE_RUSTFS_SECRET_KEY")
    bucket = _required_environment("EASYSHARE_RUSTFS_BUCKET")
    storage = RustFSStorage(
        endpoint=endpoint,
        access_key=access_key,
        secret_key=secret_key,
        bucket=bucket,
        client_config=Config(
            signature_version="s3v4",
            connect_timeout=3,
            read_timeout=10,
            retries={"max_attempts": 1, "mode": "standard"},
        ),
    )

    try:
        storage.client.head_bucket(Bucket=bucket)
    except (BotoCoreError, ClientError) as exc:
        pytest.fail(f"无法访问 RustFS bucket {bucket!r}: {type(exc).__name__}: {exc}")

    file_id = f"rustfs-it-{uuid4().hex}"
    object_key = f"integration/python/{file_id}/word-policy.docx"
    keys = artifact_keys(file_id, "v1")
    cleanup_keys = [object_key, *keys.values()]
    pipeline = DocumentPipeline(
        storage=storage,
        embedder=HashEmbedder(32),
        vector_store=VectorStore(str(tmp_path / "vectors.json")),
        chunk_size=120,
        chunk_overlap=20,
        max_source_bytes=4 * 1024 * 1024,
    )

    try:
        storage.write(
            object_key,
            build_docx_policy(),
            content_type="application/vnd.openxmlformats-officedocument.wordprocessingml.document",
        )
        reports: list[tuple[str, int]] = []
        manifest = pipeline.process(
            _processing_job(file_id, object_key),
            lambda stage, progress: reports.append((stage, progress)),
        )

        assert set(storage.list_keys(f"derived/{file_id}/v1/")) == set(keys.values())
        clean_markdown = storage.read(keys["clean_markdown"]).decode("utf-8")
        document = json.loads(storage.read(keys["document"]).decode("utf-8"))
        stored_manifest = json.loads(storage.read(keys["manifest"]).decode("utf-8"))

        assert "# EasyShare 文档处理规范" in clean_markdown
        assert document["file_id"] == file_id
        assert any(block.get("text") == "验收标准" for block in document["blocks"])
        assert stored_manifest == manifest
        assert stored_manifest["status"] == "completed"
        assert stored_manifest["artifacts"] == keys
        assert reports[0] == ("downloading", 10)
        assert reports[-1] == ("finalizing", 97)
        assert {record["version_id"] for record in pipeline.vector_store.records} == {"v1"}

        expected_types = {
            keys["clean_markdown"]: "text/markdown",
            keys["document"]: "application/json",
            keys["manifest"]: "application/json",
        }
        for key, expected_type in expected_types.items():
            head = storage.client.head_object(Bucket=bucket, Key=key)
            assert head["ContentType"].startswith(expected_type)
    finally:
        storage.client.delete_objects(
            Bucket=bucket,
            Delete={"Objects": [{"Key": key} for key in cleanup_keys], "Quiet": True},
        )