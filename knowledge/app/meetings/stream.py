"""实时转写会话：分片 PCM16 → paraformer-zh-streaming → 增量字幕。

刻意只做「预览」：会话只活在内存里（进程重启即丢、闲置 TTL 回收），
停止录音后仍走离线整段重跑（paraformer-zh + VAD + 标点 + cam++ 说话人），
终态转写与说话人以离线结果为准——与钉钉听记「先上屏、后端出正式纪要」一致。
"""
from __future__ import annotations

import logging
import threading
import time
from collections import OrderedDict
from dataclasses import dataclass, field

import numpy as np

logger = logging.getLogger(__name__)

TARGET_RATE = 16000
# 流式模型的 chunk_size 是 [左上下文, 当前, 右上下文]（单位 60ms 帧）；
# 每次喂 chunk_stride = 当前帧数 × 960 样本，即 10×960 = 9600 样本 = 600ms
CHUNK_SIZE = [0, 10, 5]
CHUNK_SAMPLES = CHUNK_SIZE[1] * 960
CHUNK_BYTES = CHUNK_SAMPLES * 2  # s16le

_IDLE_TTL_SECONDS = 30 * 60
_MAX_SESSIONS = 32
# 断行触发：模型回包里出现句末标点，或累计到该字数
_SENTENCE_ENDS = ("。", "？", "！", "，，")
_MAX_LINE_CHARS = 40


@dataclass
class LiveSession:
    meeting_id: str
    cache: dict = field(default_factory=dict)
    pending: bytes = b""
    current: str = ""
    lines: list[dict] = field(default_factory=list)
    samples: int = 0
    updated_at: float = field(default_factory=time.time)

    def elapsed_seconds(self) -> int:
        return int(self.samples / TARGET_RATE)

    def view(self) -> dict:
        return {
            "lines": list(self.lines),
            "current": self.current,
            "elapsed_seconds": self.elapsed_seconds(),
        }


class StreamingTranscriber:
    """单模型多会话：FunASR 流式模型常驻约 1GB，进程内复用，会话状态各自独立。"""

    def __init__(self, device: str = "cpu") -> None:
        self._device = device
        self._model = None
        self._sessions: OrderedDict[str, LiveSession] = OrderedDict()
        self._lock = threading.Lock()
        # 模型加载与会话表分开上锁：prewarm 与首片 feed 并发时不能双份加载，
        # 也不能因为 feed 持着会话锁就把自己堵死
        self._model_lock = threading.Lock()

    def _ensure_model(self):
        with self._model_lock:
            if self._model is None:
                from funasr import AutoModel

                logger.info("拉起流式转写模型 paraformer-zh-streaming（首次会下载权重）")
                self._model = AutoModel(model="paraformer-zh-streaming", device=self._device, disable_update=True)
        return self._model

    def prewarm(self) -> bool:
        """提前扛掉流式模型冷加载（本机实测首次约 70s，否则首个分片会卡住上屏）。

        返回 True 表示本次真的加载了模型。
        """
        before = self._model
        self._ensure_model()
        return before is None and self._model is not None

    def start(self, meeting_id: str) -> dict:
        with self._lock:
            self._sessions[meeting_id] = LiveSession(meeting_id=meeting_id)
            self._evict_locked()
        return {"meeting_id": meeting_id, "lines": [], "current": "", "elapsed_seconds": 0}

    def feed(self, meeting_id: str, pcm: bytes) -> dict:
        """喂进一段 s16le 单声道 PCM（长度任意），攒够 600ms 就推一次模型。"""
        with self._lock:
            session = self._sessions.get(meeting_id)
            if session is None:
                session = LiveSession(meeting_id=meeting_id)
                self._sessions[meeting_id] = session
                self._evict_locked()
            session.pending += pcm
            session.updated_at = time.time()
            chunk_bytes = CHUNK_SAMPLES * 2
            model = None
            while len(session.pending) >= chunk_bytes:
                piece = session.pending[:chunk_bytes]
                session.pending = session.pending[chunk_bytes:]
                session.samples += CHUNK_SAMPLES
                if model is None:
                    model = self._ensure_model()
                text = self._recognize(model, session, piece)
                self._absorb(session, text)
            return session.view()

    def stop(self, meeting_id: str) -> dict:
        with self._lock:
            session = self._sessions.pop(meeting_id, None)
        if session is None:
            return {"lines": [], "current": "", "elapsed_seconds": 0}
        model = self._ensure_model() if session.pending else None
        if model is not None:
            # 尾包：剩余不足一片的音频用 is_final 冲掉，避免丢最后一个字
            session.samples += len(session.pending) // 2
            self._absorb(session, self._recognize(model, session, bytes(session.pending), is_final=True))
        self._flush(session)
        return session.view()

    def snapshot(self, meeting_id: str) -> dict:
        with self._lock:
            session = self._sessions.get(meeting_id)
            return session.view() if session else {"lines": [], "current": "", "elapsed_seconds": 0}

    def active_lines(self, meeting_id: str) -> list[str]:
        """会中要点用：已上屏的行拼成纯文本。"""
        view = self.snapshot(meeting_id)
        lines = [str(item.get("text") or "") for item in view["lines"]]
        if view["current"]:
            lines.append(view["current"])
        return [line for line in lines if line.strip()]

    # --- 内部 ---

    def _recognize(self, model, session: LiveSession, piece: bytes, is_final: bool = False) -> str:
        audio = np.frombuffer(piece, dtype=np.int16)
        result = model.generate(
            input=audio,
            cache=session.cache,
            is_final=is_final,
            chunk_size=CHUNK_SIZE,
            encoder_chunk_look_back=4,
            decoder_chunk_look_back=1,
            disable_pbar=True,
        )
        if not result:
            return ""
        first = result[0] if isinstance(result, list) else result
        return str(first.get("text") or "")

    def _absorb(self, session: LiveSession, text: str) -> None:
        if not text:
            return
        session.current += text
        if any(mark in session.current for mark in _SENTENCE_ENDS) or len(session.current) >= _MAX_LINE_CHARS:
            self._flush(session)

    def _flush(self, session: LiveSession) -> None:
        text = session.current.strip("，。、 ")
        session.current = ""
        if not text:
            return
        session.lines.append({"text": text, "at": session.elapsed_seconds()})
        if len(session.lines) > 600:  # 兜底：极长会议只保留最近 600 行预览
            session.lines = session.lines[-600:]

    def _evict_locked(self) -> None:
        now = time.time()
        stale = [key for key, item in self._sessions.items() if now - item.updated_at > _IDLE_TTL_SECONDS]
        for key in stale:
            self._sessions.pop(key, None)
        while len(self._sessions) > _MAX_SESSIONS:
            self._sessions.popitem(last=False)
