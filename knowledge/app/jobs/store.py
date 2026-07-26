"""SQLite 任务存储：提供幂等提交、状态迁移、失败重试与重启恢复。"""
from __future__ import annotations

import json
import sqlite3
import threading
import uuid
from dataclasses import asdict, dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

TERMINAL_STATES = {"completed", "failed"}


def _now() -> str:
    return datetime.now(UTC).isoformat()


@dataclass(slots=True)
class ProcessingJob:
    id: str
    file_id: str
    version_id: str
    object_key: str
    filename: str
    status: str
    stage: str
    progress: int
    retry_count: int
    error_code: str | None
    error_message: str | None
    result: dict[str, Any] | None
    created_at: str
    updated_at: str
    started_at: str | None
    finished_at: str | None

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)


class JobStore:
    def __init__(self, path: str) -> None:
        self.path = path
        self.lock = threading.RLock()
        if path != ":memory:":
            Path(path).parent.mkdir(parents=True, exist_ok=True)
        self.connection = sqlite3.connect(path, check_same_thread=False)
        self.connection.row_factory = sqlite3.Row
        self.connection.execute("PRAGMA journal_mode=WAL")
        self.connection.execute("PRAGMA busy_timeout=5000")
        self._migrate()

    def _migrate(self) -> None:
        with self.connection:
            self.connection.execute(
                """
                CREATE TABLE IF NOT EXISTS processing_jobs (
                    id TEXT PRIMARY KEY,
                    file_id TEXT NOT NULL,
                    version_id TEXT NOT NULL,
                    object_key TEXT NOT NULL,
                    filename TEXT NOT NULL,
                    status TEXT NOT NULL,
                    stage TEXT NOT NULL,
                    progress INTEGER NOT NULL DEFAULT 0,
                    retry_count INTEGER NOT NULL DEFAULT 0,
                    error_code TEXT,
                    error_message TEXT,
                    result_json TEXT,
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL,
                    started_at TEXT,
                    finished_at TEXT
                )
                """
            )
            self.connection.execute(
                "CREATE INDEX IF NOT EXISTS idx_processing_jobs_file_version ON processing_jobs(file_id, version_id, created_at DESC)"
            )
            self.connection.execute(
                "CREATE INDEX IF NOT EXISTS idx_processing_jobs_status ON processing_jobs(status, created_at)"
            )

    def close(self) -> None:
        with self.lock:
            self.connection.close()

    def create_or_get(
        self,
        *,
        file_id: str,
        version_id: str,
        object_key: str,
        filename: str,
        force: bool = False,
    ) -> tuple[ProcessingJob, bool]:
        with self.lock, self.connection:
            if not force:
                row = self.connection.execute(
                    """
                    SELECT * FROM processing_jobs
                    WHERE file_id = ? AND version_id = ?
                    ORDER BY created_at DESC LIMIT 1
                    """,
                    (file_id, version_id),
                ).fetchone()
                if row is not None:
                    return self._from_row(row), False

            job_id = str(uuid.uuid4())
            now = _now()
            self.connection.execute(
                """
                INSERT INTO processing_jobs (
                    id, file_id, version_id, object_key, filename, status, stage,
                    progress, retry_count, created_at, updated_at
                ) VALUES (?, ?, ?, ?, ?, 'queued', 'queued', 0, 0, ?, ?)
                """,
                (job_id, file_id, version_id, object_key, filename, now, now),
            )
            return self.get(job_id), True

    def get(self, job_id: str) -> ProcessingJob:
        with self.lock:
            row = self.connection.execute(
                "SELECT * FROM processing_jobs WHERE id = ?", (job_id,)
            ).fetchone()
        if row is None:
            raise KeyError(job_id)
        return self._from_row(row)

    def find_latest(self, *, file_id: str, version_id: str) -> ProcessingJob | None:
        """返回指定文件版本最近创建的任务，不存在时返回 None。"""
        with self.lock:
            row = self.connection.execute(
                """
                SELECT * FROM processing_jobs
                WHERE file_id = ? AND version_id = ?
                ORDER BY created_at DESC LIMIT 1
                """,
                (file_id, version_id),
            ).fetchone()
        return self._from_row(row) if row is not None else None

    def list_recent(self, *, limit: int = 20) -> list[ProcessingJob]:
        """按创建时间倒序返回最近任务，并限制查询数量。"""
        bounded_limit = max(1, min(limit, 100))
        with self.lock:
            rows = self.connection.execute(
                "SELECT * FROM processing_jobs ORDER BY created_at DESC LIMIT ?",
                (bounded_limit,),
            ).fetchall()
        return [self._from_row(row) for row in rows]

    def claim(self, job_id: str) -> ProcessingJob | None:
        now = _now()
        with self.lock, self.connection:
            cursor = self.connection.execute(
                """
                UPDATE processing_jobs
                SET status = 'processing', stage = 'starting', progress = 1,
                    started_at = ?, finished_at = NULL, updated_at = ?,
                    error_code = NULL, error_message = NULL
                WHERE id = ? AND status = 'queued'
                """,
                (now, now, job_id),
            )
            if cursor.rowcount != 1:
                return None
        return self.get(job_id)

    def update_progress(self, job_id: str, *, stage: str, progress: int) -> None:
        progress = max(1, min(progress, 99))
        with self.lock, self.connection:
            cursor = self.connection.execute(
                """
                UPDATE processing_jobs
                SET stage = ?, progress = ?, updated_at = ?
                WHERE id = ? AND status = 'processing'
                """,
                (stage, progress, _now(), job_id),
            )
            if cursor.rowcount != 1:
                raise ValueError("只有 processing 状态的任务可以更新进度")

    def complete(self, job_id: str, result: dict[str, Any]) -> None:
        now = _now()
        with self.lock, self.connection:
            cursor = self.connection.execute(
                """
                UPDATE processing_jobs
                SET status = 'completed', stage = 'completed', progress = 100,
                    result_json = ?, error_code = NULL, error_message = NULL,
                    updated_at = ?, finished_at = ?
                WHERE id = ? AND status = 'processing'
                """,
                (json.dumps(result, ensure_ascii=False), now, now, job_id),
            )
            if cursor.rowcount != 1:
                raise ValueError("只有 processing 状态的任务可以完成")

    def fail(self, job_id: str, *, error_code: str, error_message: str) -> None:
        now = _now()
        with self.lock, self.connection:
            cursor = self.connection.execute(
                """
                UPDATE processing_jobs
                SET status = 'failed', stage = 'failed', error_code = ?, error_message = ?,
                    updated_at = ?, finished_at = ?
                WHERE id = ? AND status = 'processing'
                """,
                (error_code, error_message[:4000], now, now, job_id),
            )
            if cursor.rowcount != 1:
                raise ValueError("只有 processing 状态的任务可以标记失败")

    def retry(self, job_id: str) -> ProcessingJob:
        with self.lock, self.connection:
            cursor = self.connection.execute(
                """
                UPDATE processing_jobs
                SET status = 'queued', stage = 'queued', progress = 0,
                    retry_count = retry_count + 1, error_code = NULL, error_message = NULL,
                    result_json = NULL, started_at = NULL, finished_at = NULL, updated_at = ?
                WHERE id = ? AND status = 'failed'
                """,
                (_now(), job_id),
            )
            if cursor.rowcount != 1:
                raise ValueError("只有 failed 状态的任务可以重试")
        return self.get(job_id)

    def recover_incomplete(self) -> list[ProcessingJob]:
        with self.lock, self.connection:
            self.connection.execute(
                """
                UPDATE processing_jobs
                SET status = 'queued', stage = 'recovered', progress = 0,
                    started_at = NULL, finished_at = NULL, updated_at = ?
                WHERE status = 'processing'
                """,
                (_now(),),
            )
            rows = self.connection.execute(
                "SELECT * FROM processing_jobs WHERE status = 'queued' ORDER BY created_at"
            ).fetchall()
        return [self._from_row(row) for row in rows]

    def counts(self) -> dict[str, int]:
        with self.lock:
            rows = self.connection.execute(
                "SELECT status, COUNT(*) AS count FROM processing_jobs GROUP BY status"
            ).fetchall()
        return {row["status"]: row["count"] for row in rows}

    @staticmethod
    def _from_row(row: sqlite3.Row) -> ProcessingJob:
        return ProcessingJob(
            id=row["id"],
            file_id=row["file_id"],
            version_id=row["version_id"],
            object_key=row["object_key"],
            filename=row["filename"],
            status=row["status"],
            stage=row["stage"],
            progress=row["progress"],
            retry_count=row["retry_count"],
            error_code=row["error_code"],
            error_message=row["error_message"],
            result=json.loads(row["result_json"]) if row["result_json"] else None,
            created_at=row["created_at"],
            updated_at=row["updated_at"],
            started_at=row["started_at"],
            finished_at=row["finished_at"],
        )
