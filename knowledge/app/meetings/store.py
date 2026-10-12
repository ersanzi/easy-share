"""会议记录存储：data/meetings/{id}.wav + {id}.json，无数据库——量级为
企业每周若干场会议，JSON 足够；归档/检索由知识库管线承接。
"""
from __future__ import annotations

import json
import logging
import threading
import uuid
from datetime import datetime
from pathlib import Path

logger = logging.getLogger(__name__)


def _now() -> str:
    return datetime.now().isoformat(timespec="seconds")


class MeetingStore:
    def __init__(self, data_path: str | Path) -> None:
        self._directory = Path(data_path)
        self._directory.mkdir(parents=True, exist_ok=True)
        self._lock = threading.Lock()

    def _meta_path(self, meeting_id: str) -> Path:
        return self._directory / f"{meeting_id}.json"

    def _audio_path(self, meeting_id: str) -> Path:
        return self._directory / f"{meeting_id}.wav"

    def create(self, title: str, owner: str | None, template_id: str | None = None) -> dict:
        meeting_id = uuid.uuid4().hex
        meta = {
            "id": meeting_id,
            "title": title or f"{datetime.now().strftime('%Y-%m-%d %H:%M')} 的会议",
            "created_at": _now(),
            "owner": owner,
            "template_id": template_id or None,
            "status": "processing",
            "error": None,
            "duration_seconds": None,
            "speaker_count": 0,
            "speaker_names": {},
            "segments": [],
            "summary": None,
        }
        self._write_meta(meta)
        return meta

    def audio_path(self, meeting_id: str) -> Path:
        return self._audio_path(meeting_id)

    def get(self, meeting_id: str) -> dict:
        path = self._meta_path(meeting_id)
        if not path.is_file():
            raise KeyError(meeting_id)
        return json.loads(path.read_text(encoding="utf-8"))

    def list(self, limit: int = 50) -> list[dict]:
        """列表视图：不含分段全文（列表页只需要元数据与摘要）。"""
        meetings: list[dict] = []
        for path in self._directory.glob("*.json"):
            try:
                meetings.append(json.loads(path.read_text(encoding="utf-8")))
            except (json.JSONDecodeError, OSError):
                logger.warning("跳过损坏的会议记录: %s", path.name)
        meetings.sort(key=lambda meta: meta.get("created_at") or "", reverse=True)
        return [self._summary_view(meta) for meta in meetings[: max(1, limit)]]

    def update(self, meeting_id: str, **fields) -> dict:
        with self._lock:
            meta = self.get(meeting_id)
            meta.update(fields)
            self._write_meta(meta)
            return meta

    def rename_speaker(self, meeting_id: str, speaker: str, name: str) -> dict:
        with self._lock:
            meta = self.get(meeting_id)
            names = dict(meta.get("speaker_names") or {})
            names[speaker] = name
            meta["speaker_names"] = names
            self._write_meta(meta)
            return meta

    def delete(self, meeting_id: str) -> None:
        self.get(meeting_id)  # 不存在则抛 KeyError
        for path in (self._meta_path(meeting_id), self._audio_path(meeting_id)):
            path.unlink(missing_ok=True)

    def _write_meta(self, meta: dict) -> None:
        path = self._meta_path(meta["id"])
        temporary = path.with_suffix(".json.tmp")
        temporary.write_text(json.dumps(meta, ensure_ascii=False, indent=2), encoding="utf-8")
        temporary.replace(path)

    @staticmethod
    def _summary_view(meta: dict) -> dict:
        view = {key: meta[key] for key in meta if key != "segments"}
        view["segment_count"] = len(meta.get("segments") or [])
        return view
