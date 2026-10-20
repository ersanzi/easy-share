"""实时转写会话：分片 PCM16 → paraformer-zh-streaming → 增量字幕。

刻意只做「预览」：会话活在内存里（进程重启即丢、闲置 TTL 回收），
停止录音后仍走离线整段重跑（paraformer-zh + VAD + 标点 + cam++ 说话人），
终态转写与说话人以离线结果为准——与钉钉听记「先上屏、后端出正式纪要」一致。

2026-10-20 修正三处真机暴露的问题（用户：「切到别的地方就中止了」「不支持流式展示」）：
  1. **锁粒度**：旧实现 feed 持着全局会话锁跑模型推理，两个会话/预热并发时互相堵死，
     前端那一侧表现为「久等无回包 → 收摊」。现在会话表锁只保护容器读写，
     推理在**会话自己的锁**里跑，不同会话互不阻塞。
  2. **热词**：把用户的个性化热词换成流式模型认识的 `hotword` 参数（与离线同口径），
     不再只做离线后处理。
  3. **断行**：句末**与**逗号都可落行（见 livenow.should_break），旧的 40 字才断
     会让上屏看起来「半天不出字」。
"""
from __future__ import annotations

import logging
import threading
import time
from collections import OrderedDict
from dataclasses import dataclass, field

import numpy as np

from app.meetings.livenow import should_break, strip_line

logger = logging.getLogger(__name__)

TARGET_RATE = 16000
# 流式模型的 chunk_size 是 [左上下文, 当前, 右上下文]（单位 60ms 帧）；
# 每次喂 chunk_stride = 当前帧数 × 960 样本，即 10×960 = 9600 样本 = 600ms
CHUNK_SIZE = [0, 10, 5]
CHUNK_SAMPLES = CHUNK_SIZE[1] * 960
CHUNK_BYTES = CHUNK_SAMPLES * 2  # s16le

_IDLE_TTL_SECONDS = 30 * 60
_MAX_SESSIONS = 32

# 模型实例全进程一份：约 1GB 常驻，每场会话各建一份等于自杀
_SHARED_MODEL = None
_SHARED_MODEL_LOCK = threading.Lock()


def shared_model(device: str = "cpu"):
    """进程内唯一的流式模型（首次调用会下载/加载权重，之后直接返回）。"""
    global _SHARED_MODEL
    if _SHARED_MODEL is not None:
        return _SHARED_MODEL
    with _SHARED_MODEL_LOCK:
        if _SHARED_MODEL is None:
            from funasr import AutoModel

            logger.info("拉起流式转写模型 paraformer-zh-streaming（首次会下载权重）")
            _SHARED_MODEL = AutoModel(
                model="paraformer-zh-streaming", device=device, disable_update=True, disable_pbar=True
            )
    return _SHARED_MODEL


@dataclass
class LiveSession:
    meeting_id: str
    owner: str | None = None
    cache: dict = field(default_factory=dict)
    pending: bytes = b""
    current: str = ""
    lines: list[dict] = field(default_factory=list)
    samples: int = 0
    # 当前这句开始时的采样位置：落行时用它算「这句话从第几秒开始说」。
    # 以前直接用 elapsed_seconds()（= 此刻总共喂了多少音频），于是每行时间戳都是
    # 「被标点触发的那一刻」，实测同一段 0:03 开始的话被标成了 [7s]/[16s]——
    # 桌面端点验时一眼就能看出来，与离线逐字稿的时间轴也对不上。
    current_start_samples: int = 0
    # 上屏时刻（墙钟，秒）：前端据此显示「刷新于 N 秒前」，不必分片时间对齐
    updated_at: float = field(default_factory=time.time)
    # 本会话串行推理用（同一会话的两片绝不能被两个线程同时喂进模型）
    lock: threading.Lock = field(default_factory=threading.Lock)
    speaking_at_call: bool = True

    def elapsed_seconds(self) -> int:
        return int(self.samples / TARGET_RATE)

    def last_line_at(self) -> float:
        return float(self.lines[-1]["at"]) if self.lines else 0.0

    def recent_lines(self, count: int = 4) -> list[dict]:
        """最近几行（前端「快检」面板用：只给上下文，不给整场）。"""
        return [dict(item) for item in self.lines[-count:]]

    def view(self, hotword: str = "") -> dict:
        return {
            "lines": list(self.lines),
            "current": self.current,
            "elapsed_seconds": self.elapsed_seconds(),
            "last_line_at": self.last_line_at(),
            "active_seconds": self.speaking_at_call,
            "recent": self.recent_lines(3),
            "hotword": hotword,
        }

    def session_view(self) -> dict:
        """录制会话状态（GET /stream/live）：recording=True 表示服务端确实有这个会话。"""
        return {"recording": True, **self.view()}


class StreamingTranscriber:
    """单模型多会话：模型常驻进程内复用，会话状态各自独立。"""

    def __init__(self, device: str = "cpu") -> None:
        self._device = device
        self._sessions: OrderedDict[str, LiveSession] = OrderedDict()
        self._lock = threading.Lock()
        # 模型加载与会话表分开上锁：prewarm 与首片 feed 并发时不能双份加载，
        # 也不能因为 feed 持着会话锁就把自己堵死
        self._model_lock = threading.Lock()

    def _ensure_model(self):
        return shared_model(self._device)

    def prewarm(self) -> bool:
        """提前扛掉流式模型冷加载（本机实测首次约 70s，否则首个分片会卡住上屏）。

        返回 True 表示本次真的加载了模型。
        """
        before = _SHARED_MODEL
        self._ensure_model()
        return before is None and _SHARED_MODEL is not None

    def is_ready(self) -> bool:
        return _SHARED_MODEL is not None

    def start(self, meeting_id: str, owner: str | None = None) -> dict:
        with self._lock:
            self._sessions[meeting_id] = LiveSession(meeting_id=meeting_id, owner=owner)
            self._evict_locked()
            session = self._sessions[meeting_id]
        return session.view()

    def feed(self, meeting_id: str, pcm: bytes, hotword: str = "") -> dict:
        """喂进一段 s16le 单声道 PCM（长度任意），攒够 600ms 就推一次模型。"""
        session = self._get_or_create(meeting_id)
        with session.lock:
            session.pending += pcm
            session.updated_at = time.time()
            if len(session.pending) < CHUNK_BYTES:
                session.speaking_at_call = bool(pcm.strip(b"\x00"))
                return session.view(hotword)
            model = self._ensure_model()
            while len(session.pending) >= CHUNK_BYTES:
                piece = session.pending[:CHUNK_BYTES]
                session.pending = session.pending[CHUNK_BYTES:]
                session.samples += CHUNK_SAMPLES
                text = self._recognize(model, session, piece, hotword=hotword)
                self._absorb(session, text)
            session.speaking_at_call = bool(pcm.strip(b"\x00"))
            return session.view(hotword)

    def stop(self, meeting_id: str) -> dict:
        with self._lock:
            session = self._sessions.pop(meeting_id, None)
        if session is None:
            return {"lines": [], "current": "", "elapsed_seconds": 0, "last_line_at": 0.0}
        with session.lock:
            if session.pending:
                # 尾包：剩余不足一片的音频用 is_final 冲掉，避免丢最后一个字
                model = self._ensure_model()
                session.samples += len(session.pending) // 2
                text = self._recognize(model, session, bytes(session.pending), is_final=True)
                session.pending = b""
                self._absorb(session, text)
            self._flush(session)
        return session.view()

    def snapshot(self, meeting_id: str, hotword: str = "") -> dict:
        with self._lock:
            session = self._sessions.get(meeting_id)
        if session is None:
            # recording=False：没有这个会话（进程重启/闲置回收），前端据此改为只等离线重跑
            return {"recording": False, "lines": [], "current": "", "elapsed_seconds": 0, "last_line_at": 0.0}
        with session.lock:
            return session.session_view()

    def has_session(self, meeting_id: str) -> bool:
        with self._lock:
            return meeting_id in self._sessions

    def recent_text(self, meeting_id: str, count: int = 4) -> tuple[str, float]:
        """最近几行的纯文本 + 最后一行的上屏时刻（0 表示还没出过字）。"""
        session = self._get_or_create(meeting_id)
        with session.lock:
            lines = session.recent_lines(count)
            tail = [strip_line(str(item.get("text") or "")) for item in lines]
            if session.current:
                tail.append(session.current)
            return "\n".join(line for line in tail if line), session.last_line_at()

    def active_lines(self, meeting_id: str) -> list[str]:
        """会中要点用：已上屏的行拼成纯文本。"""
        view = self.snapshot(meeting_id)
        lines = [str(item.get("text") or "") for item in view["lines"]]
        if view.get("current"):
            lines.append(str(view["current"]))
        return [line for line in lines if line.strip()]

    # --- 内部 ---

    def _get_or_create(self, meeting_id: str) -> LiveSession:
        with self._lock:
            session = self._sessions.get(meeting_id)
            if session is None:
                session = LiveSession(meeting_id=meeting_id)
                self._sessions[meeting_id] = session
                self._evict_locked()
            return session

    def _recognize(self, model, session: LiveSession, piece: bytes, is_final: bool = False, hotword: str = "") -> str:
        audio = np.frombuffer(piece, dtype=np.int16)
        kwargs = {
            "input": audio,
            "cache": session.cache,
            "is_final": is_final,
            "chunk_size": CHUNK_SIZE,
            "encoder_chunk_look_back": 4,
            "decoder_chunk_look_back": 1,
            "disable_pbar": True,
        }
        # 热词与离线转写同口径（空格分隔），流式模型也支持 seaco 上下文增强；
        # 个别 funasr 版本不接受该参数时不影响上屏，退化为无热词
        if hotword.strip():
            kwargs["hotword"] = hotword.strip()
        try:
            result = model.generate(**kwargs)
        except TypeError:
            kwargs.pop("hotword", None)
            result = model.generate(**kwargs)
        if not result:
            return ""
        first = result[0] if isinstance(result, list) else result
        return str(first.get("text") or "")

    def _absorb(self, session: LiveSession, text: str) -> None:
        if not text:
            return
        # 空句被写上第一个字时，记下这句的起点（此刻的采样位置）
        if not session.current:
            session.current_start_samples = session.samples
        session.current += text
        if should_break(session.current):
            self._flush(session)

    def _flush(self, session: LiveSession) -> None:
        text = strip_line(session.current)
        started = int(session.current_start_samples / TARGET_RATE)
        session.current = ""
        if not text:
            return
        session.lines.append({"text": text, "at": started})
        if len(session.lines) > 600:  # 兜底：极长会议只保留最近 600 行预览
            session.lines = session.lines[-600:]

    def _evict_locked(self) -> None:
        now = time.time()
        stale = [key for key, item in self._sessions.items() if now - item.updated_at > _IDLE_TTL_SECONDS]
        for key in stale:
            self._sessions.pop(key, None)
        while len(self._sessions) > _MAX_SESSIONS:
            self._sessions.popitem(last=False)
