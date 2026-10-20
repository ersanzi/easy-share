"""会议记录存储：data/meetings/{id}.wav + {id}.json，无数据库——量级为
企业每周若干场会议，JSON 足够；归档/检索由知识库管线承接。
"""
from __future__ import annotations

import json
import logging
import threading
import time
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
        # **RLock 而非 Lock**：写路径（update/set_block/rename_speaker…）都是
        # 「持锁 → 调 self.get() → 写」，而 get() 自己也要持锁。
        # 用普通 Lock 会自死锁；RLock 可重入，同一线程重复获取没问题。
        self._lock = threading.RLock()

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
        # **读也要持锁**——这是 Windows 上的正确性要求，不是性能取舍：
        # `_write_meta` 用「写临时文件 + 替换」原子更新，而 Windows 在
        # 目标文件正被打开读时**不允许替换**。读不持锁 = 与写并发时
        # 必然偶发 `PermissionError`（实测见 concurrent_readers 用例），
        # 而那个异常会穿过 `_process` 被线程池吞掉，记录永久停在 processing。
        with self._lock:
            path = self._meta_path(meeting_id)
            if not path.is_file():
                raise KeyError(meeting_id)
            return _ensure_schema(json.loads(path.read_text(encoding="utf-8")))

    def list(self, limit: int = 50) -> list[dict]:
        """列表视图：不含分段全文（列表页只需要元数据与摘要）。"""
        meetings: list[dict] = []
        with self._lock:
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

    def merge_speakers(self, meeting_id: str, from_speaker: str, into_speaker: str) -> dict:
        """把 from_speaker 的所有轮次并进 into_speaker，并从发言人表里删掉来源。

        合并后相邻同人且紧挨着的轮次顺手拼起来（否则转写里会出现连续两条同一个人）。
        """
        with self._lock:
            meta = self.get(meeting_id)
            speakers = {str(segment.get("speaker")) for segment in meta.get("segments") or []}
            speakers |= {str(key) for key in (meta.get("speaker_names") or {})}
            for target in (from_speaker, into_speaker):
                if target not in speakers:
                    raise KeyError(target)
            segments = [
                {**segment, "speaker": into_speaker} if str(segment.get("speaker")) == from_speaker else dict(segment)
                for segment in meta.get("segments") or []
            ]
            merged: list[dict] = []
            for segment in segments:
                previous = merged[-1] if merged else None
                if (
                    previous
                    and previous["speaker"] == segment["speaker"]
                    and float(segment["start"]) - float(previous["end"]) <= 1.0
                ):
                    previous["text"] = str(previous["text"]) + str(segment["text"])
                    previous["end"] = segment["end"]
                else:
                    merged.append(segment)
            names = {key: value for key, value in (meta.get("speaker_names") or {}).items() if key != from_speaker}
            meta["segments"] = merged
            meta["speaker_names"] = names
            meta["speaker_count"] = len({str(segment["speaker"]) for segment in merged})
            self._write_meta(meta)
            return meta

    def update_segment_text(self, meeting_id: str, index: int, text: str) -> dict:
        """改一句转写文字（对标钉钉逐字稿可编辑）。

        只动这一句：说话人、时间轴原样保留——用户改的是「字」，不是「谁在什么时候说的」。
        """
        with self._lock:
            meta = self.get(meeting_id)
            segments = list(meta.get("segments") or [])
            if index < 0 or index >= len(segments):
                raise KeyError(index)
            segments[index] = {**segments[index], "text": text}
            meta["segments"] = segments
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
        """原子写入：临时文件 + 替换，并在 Windows 上**重试**。

        ⚠️ 这里的重试不是"防御性编程"，是 Windows 的硬约束：
        **一个文件正被别的线程/进程打开读时，不允许被替换**。
        `Path.replace` 在那时会抛 `PermissionError: [Errno 13]`。
        在 POSIX 上 rename 可以覆盖被打开的文件，所以这个竞态**只在 Windows 出现**——
        本仓主线是 Windows 桌面端，恰好是踩中的那一侧。

        触发场景很常见：前端轮询列表（`list()` 逐文件 `read_text`）
        与后台管线写同一条记录（`update()`/`set_block()`）并发。

        实测：4 个读线程 + 300 次写，**必然**撞上（见
        `test_write_meta_survives_concurrent_readers`）。

        重试几轮即可——读是毫秒级的，退避几十毫秒足够错开。
        上限 50 次 × (5~25ms) ≈ 最长 1.2 秒，之后仍失败就如实抛出
        （不无限重试：那会把一个可诊断的错误变成静默挂起）。
        """
        path = self._meta_path(meta["id"])
        temporary = path.with_suffix(".json.tmp")
        payload = json.dumps(meta, ensure_ascii=False, indent=2)
        last: OSError | None = None
        for attempt in range(50):
            try:
                temporary.write_text(payload, encoding="utf-8")
                temporary.replace(path)
                return
            except OSError as exc:  # Windows: 目标文件正被读 → PermissionError
                last = exc
                # 退避递增（5ms 起、封顶 25ms）：读写都是短操作，
                # 几十毫秒足够让读线程释放句柄。
                time.sleep(min(0.005 * (attempt + 1), 0.025))
        raise OSError(f"写入会议记录失败（重试 50 次仍被占用）: {path}") from last

    @staticmethod
    def _summary_view(meta: dict) -> dict:
        view = {key: meta[key] for key in meta if key != "segments"}
        view["segment_count"] = len(meta.get("segments") or [])
        return view
