"""--apply 生产索引重建与任务库文档枚举的验证。

apply 是覆盖生产索引的破坏性写路径，在冲刺期轻测试约定下例外覆盖三个
安全语义：任一文档失败必须整体中止且索引不动、替换必须留备份、任务库
枚举必须选最新 completed 版本。
"""

import json
import shutil
from pathlib import Path

import pytest

from app.config import Settings
from app.jobs.store import JobStore
from app.kb.rebuild import apply_rebuild, collect_rebuild_targets
from app.kb.store import VectorStore
from tests.retrieval.corpus import (
    EVAL_CHUNK_OVERLAP,
    EVAL_CHUNK_SIZE,
    EVAL_HASH_DIM,
    build_eval_setup,
    index_corpus,
)


def _config(tmp_path: Path) -> Settings:
    """隔离 .env 的最小配置：路径全指 tmp，切块参数与评测语料一致。"""
    return Settings(
        _env_file=None,
        vector_store_path=str(tmp_path / "vector_store.json"),
        job_store_path=str(tmp_path / "jobs.db"),
        chunk_size=EVAL_CHUNK_SIZE,
        chunk_overlap=EVAL_CHUNK_OVERLAP,
    )


def _complete_job(store: JobStore, file_id: str, version_id: str) -> None:
    job, _ = store.create_or_get(
        file_id=file_id,
        version_id=version_id,
        object_key=f"docs/{file_id}",
        filename=f"{file_id}.pdf",
    )
    assert store.claim(job.id) is not None
    store.complete(job.id, {})


def _chunks_by_doc(store: VectorStore) -> dict[str, int]:
    counts: dict[str, int] = {}
    for record in store.snapshot_records():
        counts[record["doc_id"]] = counts.get(record["doc_id"], 0) + 1
    return counts


def test_apply_rebuild_restores_lost_doc_and_keeps_backup(tmp_path):
    setup = build_eval_setup(tmp_path / "work")
    file_ids = index_corpus(setup)
    assert len(file_ids) >= 2
    config = _config(tmp_path)

    # 生产索引 = 管线直建结果，人为丢掉一个文档（模拟索引损坏/丢块）
    shutil.copyfile(setup.vector_store.path, config.vector_store_path)
    store_file = Path(config.vector_store_path)
    records = json.loads(store_file.read_text(encoding="utf-8"))
    lost_doc = file_ids[0]
    records = [record for record in records if record["doc_id"] != lost_doc]
    store_file.write_text(json.dumps(records), encoding="utf-8")

    # 任务库登记全部文档为 completed（生产的权威文档清单）
    job_store = JobStore(config.job_store_path)
    for file_id in file_ids:
        _complete_job(job_store, file_id, "v1")
    job_store.close()

    report = apply_rebuild(config, storage=setup.storage, embedder=setup.embedder)

    assert report["applied"] is True
    assert report["documents"] == len(file_ids)
    assert report["backup"] is not None and Path(report["backup"]).exists()

    # 丢掉的文档恢复，各文档块数与管线直建逐一持平
    rebuilt = VectorStore(config.vector_store_path)
    assert set(_chunks_by_doc(rebuilt)) == set(file_ids)
    assert _chunks_by_doc(rebuilt) == _chunks_by_doc(setup.vector_store)

    # 备份保存的是替换前的损坏版
    backup_docs = {
        record["doc_id"] for record in json.loads(Path(report["backup"]).read_text(encoding="utf-8"))
    }
    assert lost_doc not in backup_docs


def test_apply_aborts_and_keeps_index_when_derived_missing(tmp_path):
    setup = build_eval_setup(tmp_path / "work")
    index_corpus(setup)
    config = _config(tmp_path)

    # 生产索引里有一个 derived 已丢失的幽灵文档（任务库为空，走索引反查兜底）
    ghost = {
        "doc_id": "ghost",
        "file_id": "ghost",
        "version_id": "v1",
        "text": "x",
        "metadata": {},
        "embedding": [0.0] * EVAL_HASH_DIM,
    }
    store = VectorStore(config.vector_store_path)
    store.add([ghost])
    store_file = Path(config.vector_store_path)
    before = store_file.read_text(encoding="utf-8")

    with pytest.raises(RuntimeError):
        apply_rebuild(config, storage=setup.storage, embedder=setup.embedder)

    # 整体中止：生产索引原样保留，不留下半成品临时文件
    assert store_file.read_text(encoding="utf-8") == before
    assert not store_file.with_name(store_file.name + ".rebuild-tmp").exists()


def test_latest_completed_docs_prefers_newest_version(tmp_path):
    store = JobStore(str(tmp_path / "jobs.db"))
    _complete_job(store, "f1", "v1")
    _complete_job(store, "f1", "v2")
    _complete_job(store, "f2", "v1")
    # f2 的 v2 任务失败过：不参与清单（failed ≠ derived 可用）
    job, _ = store.create_or_get(
        file_id="f2", version_id="v2", object_key="docs/f2v2", filename="f2v2.pdf"
    )
    assert store.claim(job.id) is not None
    store.fail(job.id, error_code="parse", error_message="boom")

    assert store.latest_completed_docs() == [("f1", "v2"), ("f2", "v1")]
    # 枚举口径：任务库为权威，索引反查只补任务库里没有的文档
    index = VectorStore(str(tmp_path / "vectors.json"))
    assert collect_rebuild_targets(index, store) == [("f1", "v2"), ("f2", "v1")]
