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


# 区块状态（对标钉钉：转写/关键词/总结/导图各自一个状态字段，互不牵连）
BLOCKS = ("transcript", "keywords", "summary", "mindmap")


def _blank_blocks() -> dict:
    return {name: "pending" for name in BLOCKS}


def _ensure_schema(meta: dict) -> dict:
    """老记录（本轮之前的落盘文件）补齐区块状态/关键词/待办三字段，按已有产物推断状态。"""
    if meta.get("keywords") is None:
        meta["keywords"] = []
    if meta.get("todos") is None:
        meta["todos"] = [
            {
                "id": f"ai{index}",
                "task": str(todo.get("task") or ""),
                "owner_hint": str(todo.get("owner_hint") or ""),
                "due_hint": str(todo.get("due_hint") or ""),
                "done": False,
                "source": "ai",
                "created_at": meta.get("created_at") or _now(),
            }
            for index, todo in enumerate((meta.get("summary") or {}).get("todos") or [])
            if isinstance(todo, dict) and todo.get("task")
        ]
    blocks = meta.get("blocks")
    if not blocks:
        failed = meta.get("status") == "failed"
        summary = meta.get("summary") or {}
        blocks = {
            "transcript": "ready" if meta.get("segments") else ("failed" if failed else "pending"),
            "keywords": "skipped",
            "summary": "ready" if summary else ("failed" if failed else "pending"),
            "mindmap": "ready" if meta.get("mindmap") else ("failed" if failed else "pending"),
        }
    meta["blocks"] = {**_blank_blocks(), **blocks}
    return meta


class MeetingStore:
    def __init__(self, data_path: str | Path) -> None:
        self._directory = Path(data_path)
        self._directory.mkdir(parents=True, exist_ok=True)
        self._lock = threading.Lock()

    def _meta_path(self, meeting_id: str) -> Path:
        return self._directory / f"{meeting_id}.json"

    def _audio_path(self, meeting_id: str) -> Path:
        return self._directory / f"{meeting_id}.wav"

    def create(
        self, title: str, owner: str | None, template_id: str | None = None, status: str = "processing"
    ) -> dict:
        meeting_id = uuid.uuid4().hex
        meta = {
            "id": meeting_id,
            "title": title or f"{datetime.now().strftime('%Y-%m-%d %H:%M')} 的会议",
            "created_at": _now(),
            "owner": owner,
            "template_id": template_id or None,
            "status": status,
            "error": None,
            "duration_seconds": None,
            "speaker_count": 0,
            "speaker_names": {},
            "segments": [],
            "summary": None,
            "keywords": [],
            "todos": [],
            "blocks": _blank_blocks(),
        }
        self._write_meta(meta)
        return meta

    def audio_path(self, meeting_id: str) -> Path:
        return self._audio_path(meeting_id)

    def get(self, meeting_id: str) -> dict:
        path = self._meta_path(meeting_id)
        if not path.is_file():
            raise KeyError(meeting_id)
        return _ensure_schema(json.loads(path.read_text(encoding="utf-8")))

    def list(self, limit: int = 50) -> list[dict]:
        """列表视图：不含分段全文（列表页只需要元数据与摘要）。"""
        meetings: list[dict] = []
        for path in self._directory.glob("*.json"):
            try:
                meetings.append(_ensure_schema(json.loads(path.read_text(encoding="utf-8"))))
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

    # --- 区块状态（转写/关键词/总结/导图各自独立，某块失败只重跑那块） ---

    def set_block(self, meeting_id: str, block: str, state: str, error: str | None = None) -> dict:
        with self._lock:
            meta = self.get(meeting_id)
            blocks = dict(meta.get("blocks") or _blank_blocks())
            blocks[block] = state
            meta["blocks"] = blocks
            if error is not None:
                meta.setdefault("block_errors", {})[block] = error
            self._write_meta(meta)
            return meta

    # --- 待办（一等实体：可增删改勾选，AI 首抽只是初始数据） ---

    def add_todo(self, meeting_id: str, task: str, owner_hint: str = "", due_hint: str = "", source: str = "manual") -> dict:
        with self._lock:
            meta = self.get(meeting_id)
            todo = {
                "id": uuid.uuid4().hex[:10],
                "task": task,
                "owner_hint": owner_hint,
                "due_hint": due_hint,
                "done": False,
                "source": source,
                "created_at": _now(),
            }
            meta["todos"] = list(meta.get("todos") or []) + [todo]
            self._write_meta(meta)
            return todo

    def replace_ai_todos(self, meeting_id: str, todos: list[dict]) -> list[dict]:
        """转写/重生成产出的 AI 待办整体刷新，用户手工新增（source=manual）的条目保留。"""
        with self._lock:
            meta = self.get(meeting_id)
            kept = [todo for todo in (meta.get("todos") or []) if todo.get("source") != "ai"]
            stamped = []
            for item in todos:
                stamped.append({
                    "id": uuid.uuid4().hex[:10],
                    "task": str(item.get("task") or ""),
                    "owner_hint": str(item.get("owner_hint") or ""),
                    "due_hint": str(item.get("due_hint") or ""),
                    "done": False,
                    "source": "ai",
                    "created_at": _now(),
                })
            meta["todos"] = kept + stamped
            self._write_meta(meta)
            return stamped

    def update_todo(self, meeting_id: str, todo_id: str, **fields) -> dict:
        with self._lock:
            meta = self.get(meeting_id)
            target = None
            for todo in meta.get("todos") or []:
                if todo.get("id") == todo_id:
                    target = todo
                    break
            if target is None:
                raise KeyError(todo_id)
            for key, value in fields.items():
                if value is not None and key in ("task", "owner_hint", "due_hint", "done"):
                    target[key] = value
            self._write_meta(meta)
            return target

    def remove_todo(self, meeting_id: str, todo_id: str) -> list[dict]:
        with self._lock:
            meta = self.get(meeting_id)
            remaining = [todo for todo in (meta.get("todos") or []) if todo.get("id") != todo_id]
            if len(remaining) == len(meta.get("todos") or []):
                raise KeyError(todo_id)
            meta["todos"] = remaining
            self._write_meta(meta)
            return remaining

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
