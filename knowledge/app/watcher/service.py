"""目录监听自动入库实现：轮询扫描 + 稳定性判断 + 内容哈希版本去重。"""
from __future__ import annotations

import hashlib
import logging
import mimetypes
import re
import threading
import time
from pathlib import Path

from app.parsing.extractor import SUPPORTED_EXTENSIONS

logger = logging.getLogger(__name__)


def _safe_filename(raw: str) -> str:
    """与 lab 上传同规则的文件名净化（Windows 非法字符、控制字符、长度）。"""
    candidate = re.sub(r'[<>:"/\\|?*]+', "_", raw)
    candidate = "".join("_" if ord(char) < 32 else char for char in candidate).strip(" .")
    if not candidate:
        candidate = "watched-file"
    if len(candidate) > 180:
        suffix = Path(candidate).suffix[:20]
        candidate = f"{Path(candidate).stem[: max(1, 180 - len(suffix))]}{suffix}"
    return candidate


def _stable_file_id(path: str) -> str:
    """按路径生成稳定 file_id（SAFE_ID 兼容：ASCII 前缀 + 十六进制）。"""
    return f"watch-{hashlib.sha1(path.lower().encode('utf-8', errors='replace')).hexdigest()[:16]}"


class DirectoryWatcher:
    """轮询监听目录；文件大小/mtime 变化且稳定后按内容哈希入库。

    与 lab 上传走完全相同的链路：storage.write → job_store.create_or_get →
    runner.submit，不另建解析或清洗逻辑。
    """

    def __init__(self, services, *, interval_seconds: int = 30, stable_seconds: int = 5) -> None:
        self.services = services
        self.interval_seconds = max(5, interval_seconds)
        self.stable_seconds = max(0, stable_seconds)
        self._stop = threading.Event()
        self._thread: threading.Thread | None = None
        # path -> (size, mtime, 已入库版本)；重启后靠内容哈希版本去重兜底
        self._seen: dict[str, tuple[int, float, str]] = {}

    @property
    def directories(self) -> list[Path]:
        raw = getattr(self.services.config, "watch_dirs", "") or ""
        return [Path(part.strip()) for part in raw.split(";") if part.strip()]

    def start(self) -> None:
        if self._thread is not None:
            return
        dirs = "、".join(str(d) for d in self.directories)
        logger.info("目录监听已启动（每 %ss 扫描）：%s", self.interval_seconds, dirs)
        self._thread = threading.Thread(target=self._loop, name="dir-watcher", daemon=True)
        self._thread.start()

    def stop(self) -> None:
        self._stop.set()
        if self._thread is not None:
            self._thread.join(timeout=10)
            self._thread = None

    def _loop(self) -> None:
        while not self._stop.wait(self.interval_seconds):
            try:
                self.scan_once()
            except Exception:  # 单轮失败不终止监听
                logger.warning("目录扫描失败", exc_info=True)

    def scan_once(self) -> list[str]:
        """手动/定时扫描一轮，返回本轮新入库的文件名列表。"""
        ingested: list[str] = []
        for directory in self.directories:
            if not directory.is_dir():
                logger.warning("监听目录不存在，跳过：%s", directory)
                continue
            for file_path in sorted(directory.rglob("*")):
                if not file_path.is_file():
                    continue
                if file_path.suffix.lower() not in SUPPORTED_EXTENSIONS:
                    continue
                try:
                    stat = file_path.stat()
                except OSError:
                    continue
                key = str(file_path)
                previous = self._seen.get(key)
                if previous and previous[0] == stat.st_size and previous[1] == stat.st_mtime:
                    continue  # 指纹未变，跳过
                # 稳定性：mtime 太新视为仍在写入（复制中），下轮再看
                if time.time() - stat.st_mtime < self.stable_seconds:
                    continue
                try:
                    content = file_path.read_bytes()
                except OSError:
                    continue
                if not content or len(content) > self.services.config.max_source_bytes:
                    continue
                version = hashlib.sha256(content).hexdigest()[:12]
                if previous and previous[2] == version:
                    self._seen[key] = (stat.st_size, stat.st_mtime, version)
                    continue  # 仅时间戳变化，内容未变
                if self._ingest(file_path, content, version):
                    ingested.append(file_path.name)
                    # 仅成功入库才记录指纹；失败不记录，下一轮自动重试
                    self._seen[key] = (stat.st_size, stat.st_mtime, version)
        return ingested

    def _ingest(self, file_path: Path, content: bytes, version: str) -> bool:
        filename = _safe_filename(file_path.name)
        file_id = _stable_file_id(str(file_path))
        object_key = f"watched/{file_id}/{version}/{filename}"
        content_type = mimetypes.guess_type(filename)[0] or "application/octet-stream"
        try:
            self.services.storage.write(object_key, content, content_type=content_type)
        except Exception:
            logger.warning("监听文件写入存储失败：%s", file_path, exc_info=True)
            return False
        job, created = self.services.job_store.create_or_get(
            file_id=file_id,
            version_id=version,
            object_key=object_key,
            filename=filename,
        )
        if created or job.status == "queued":
            self.services.job_runner.submit(job.id)
            logger.info("监听入库：%s（%s/%s）", filename, file_id, version)
        return True
