"""进程内任务执行器；本地骨架支持重启恢复，未来由 Java + 队列替换。"""
from __future__ import annotations

import logging
import threading
from concurrent.futures import ThreadPoolExecutor
from typing import Callable

from app.jobs.store import JobStore, ProcessingJob

logger = logging.getLogger(__name__)
ProgressReporter = Callable[[str, int], None]
Processor = Callable[[ProcessingJob, ProgressReporter], dict]


class JobRunner:
    def __init__(self, store: JobStore, processor: Processor, *, workers: int = 2) -> None:
        self.store = store
        self.processor = processor
        self.executor = ThreadPoolExecutor(max_workers=max(1, workers), thread_name_prefix="knowledge-job")
        self.lock = threading.Lock()
        self.active: set[str] = set()
        self.closed = False

    def start(self) -> None:
        for job in self.store.recover_incomplete():
            self.submit(job.id)

    def submit(self, job_id: str) -> None:
        with self.lock:
            if self.closed or job_id in self.active:
                return
            self.active.add(job_id)
        self.executor.submit(self._run, job_id)

    def retry(self, job_id: str) -> ProcessingJob:
        job = self.store.retry(job_id)
        self.submit(job_id)
        return job

    def shutdown(self) -> None:
        with self.lock:
            self.closed = True
        self.executor.shutdown(wait=True, cancel_futures=False)

    def _run(self, job_id: str) -> None:
        try:
            job = self.store.claim(job_id)
            if job is None:
                return

            def report(stage: str, progress: int) -> None:
                self.store.update_progress(job_id, stage=stage, progress=progress)

            result = self.processor(job, report)
            self.store.complete(job_id, result)
        except Exception as exc:  # 后台边界必须把异常收敛为可查询任务错误
            logger.exception("文档处理失败: job_id=%s", job_id)
            try:
                self.store.fail(
                    job_id,
                    error_code=type(exc).__name__,
                    error_message=str(exc) or repr(exc),
                )
            except Exception:
                logger.exception("记录任务失败状态失败: job_id=%s", job_id)
        finally:
            with self.lock:
                self.active.discard(job_id)
