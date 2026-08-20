from __future__ import annotations

import time

from app.watcher.service import DirectoryWatcher
from tests.helpers import FakeStorage, make_services


def _make_watcher(tmp_path, stable_seconds: int = 0):
    watch_dir = tmp_path / "share"
    watch_dir.mkdir()
    storage = FakeStorage()
    services = make_services(
        tmp_path,
        storage,
        config_overrides={"watch_dirs": str(watch_dir)},
    )
    watcher = DirectoryWatcher(services, interval_seconds=3600, stable_seconds=stable_seconds)
    return watcher, services, storage, watch_dir


def _wait_job_done(services, timeout: float = 10.0) -> int:
    """轮询等待所有任务到终态，返回完成任务数。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        jobs = services.job_store.list_recent(limit=100)
        if jobs and all(job.status in {"completed", "failed"} for job in jobs):
            return sum(1 for job in jobs if job.status == "completed")
        time.sleep(0.05)
    return -1


def _old_mtime(path) -> None:
    """把 mtime 拨回过去，绕过稳定性窗口。"""
    import os

    past = time.time() - 3600
    os.utime(path, (past, past))


def test_new_file_ingested_once(tmp_path) -> None:
    watcher, services, storage, watch_dir = _make_watcher(tmp_path)
    try:
        doc = watch_dir / "报销制度.md"
        doc.write_text("# 报销制度\n单笔上限五千元，需部门审批。", encoding="utf-8")
        _old_mtime(doc)

        assert watcher.scan_once() == ["报销制度.md"]
        assert _wait_job_done(services) == 1
        assert any(key.startswith("watched/") for key in storage.objects)
        assert services.vector_store.records, "应生成向量记录"

        # 再扫一轮：内容未变，不产生新任务（幂等）
        assert watcher.scan_once() == []
        assert _wait_job_done(services) == 1
    finally:
        services.job_store.close()


def test_modified_file_creates_new_version(tmp_path) -> None:
    watcher, services, storage, watch_dir = _make_watcher(tmp_path)
    try:
        doc = watch_dir / "制度.txt"
        doc.write_text("第一版内容：报销上限三千元。", encoding="utf-8")
        _old_mtime(doc)
        watcher.scan_once()
        assert _wait_job_done(services) == 1

        doc.write_text("第二版内容：报销上限五千元，提高到部门审批制。", encoding="utf-8")
        _old_mtime(doc)
        assert watcher.scan_once() == ["制度.txt"]
        assert _wait_job_done(services) == 2  # 同名文件新版本 = 新任务
    finally:
        services.job_store.close()


def test_unsupported_extensions_ignored(tmp_path) -> None:
    watcher, services, _, watch_dir = _make_watcher(tmp_path)
    try:
        (watch_dir / "病毒.exe").write_bytes(b"MZ...")
        (watch_dir / "压缩包.zip").write_bytes(b"PK...")
        _old_mtime(watch_dir / "病毒.exe")
        _old_mtime(watch_dir / "压缩包.zip")

        assert watcher.scan_once() == []
        assert services.job_store.list_recent(limit=10) == []
    finally:
        services.job_store.close()


def test_fresh_file_waits_for_stability(tmp_path) -> None:
    watcher, services, _, watch_dir = _make_watcher(tmp_path, stable_seconds=60)
    try:
        doc = watch_dir / "正在复制.txt"
        doc.write_text("内容仍在写入中……", encoding="utf-8")  # mtime 是现在

        assert watcher.scan_once() == []  # mtime 太新，视为复制中，跳过
        _old_mtime(doc)
        assert watcher.scan_once() == ["正在复制.txt"]  # 稳定后入库
    finally:
        services.job_store.close()


def test_failed_ingest_retries_next_scan(tmp_path) -> None:
    """存储写入失败时不记录指纹，下一轮自动重试（冒烟抓到的真实 bug）。"""
    watcher, services, storage, watch_dir = _make_watcher(tmp_path)
    try:
        doc = watch_dir / "重试文档.txt"
        doc.write_text("存储故障后应重试入库的内容。", encoding="utf-8")
        _old_mtime(doc)

        storage.fail_on_write = "重试文档.txt"  # 第一轮：写入失败
        assert watcher.scan_once() == []
        assert services.job_store.list_recent(limit=10) == []

        storage.fail_on_write = None  # 第二轮：恢复后必须能重试（而非被指纹跳过）
        assert watcher.scan_once() == ["重试文档.txt"]
        assert _wait_job_done(services) == 1
    finally:
        services.job_store.close()


def test_missing_directory_is_skipped_not_fatal(tmp_path) -> None:
    storage = FakeStorage()
    services = make_services(
        tmp_path,
        storage,
        config_overrides={"watch_dirs": f"{tmp_path / '不存在的目录'}"},
    )
    watcher = DirectoryWatcher(services)
    try:
        assert watcher.scan_once() == []  # 目录缺失只告警不抛错
    finally:
        services.job_store.close()
