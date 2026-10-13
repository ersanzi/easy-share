"""声纹（轻口径）：标注过的发言人下次自动认出来。

不做单独采集入口——用户在听记里把「说话人 2」改成真名，就从他那几段音频抽 192 维
cam++ 声纹存下来；下一场会议转写完，拿每个说话人簇的声纹来比对，命中就直接写名字。

阈值不拍脑袋：本机三条真实会议录音实测同人跨场 0.863–0.907，不同人 ≤0.482
（含同一场内的两个说话人 0.482），取中值 0.67，两侧各留约 0.19 余量。

声纹属生物特征个人信息，因此必须可查可删：词表按用户隔离，删除即彻底不保留向量。
"""
from __future__ import annotations

import base64
import json
import logging
import threading
import wave
from datetime import datetime
from pathlib import Path

import numpy as np

logger = logging.getLogger(__name__)

SAMPLE_RATE = 16000
MATCH_THRESHOLD = 0.67  # 实测分布中值，见模块头
MIN_SAMPLE_SECONDS = 3.0  # 太短的片段抽不出稳定声纹，直接不采纳
MAX_SAMPLE_SECONDS = 20.0  # 声纹不需要全场内容，截断省算力
MAX_PER_USER = 50


def _cosine(a: np.ndarray, b: np.ndarray) -> float:
    return float(np.dot(a, b) / (np.linalg.norm(a) * np.linalg.norm(b) + 1e-9))


def _encode(vector: np.ndarray) -> str:
    return base64.b64encode(vector.astype(np.float32).tobytes()).decode("ascii")


def _decode(text: str) -> np.ndarray | None:
    try:
        return np.frombuffer(base64.b64decode(text), dtype=np.float32)
    except Exception:  # noqa: BLE001 - 单条向量坏了不影响其他人
        return None


class VoiceprintStore:
    """按用户（会议 owner）隔离的声纹向量表，文件型存储，与听记记录同目录。"""

    def __init__(self, path: str | Path) -> None:
        self._path = Path(path)
        self._lock = threading.Lock()
        self._sv = None
        self._sv_lock = threading.Lock()

    # --- 持久化 ---

    def _read(self) -> dict:
        if not self._path.is_file():
            return {}
        try:
            data = json.loads(self._path.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError):
            logger.warning("声纹文件损坏，按空表处理: %s", self._path)
            return {}
        return data if isinstance(data, dict) else {}

    def _write(self, data: dict) -> None:
        self._path.parent.mkdir(parents=True, exist_ok=True)
        temporary = self._path.with_suffix(".json.tmp")
        temporary.write_text(json.dumps(data, ensure_ascii=False), encoding="utf-8")
        temporary.replace(self._path)

    def _models(self):
        if self._sv is None:
            with self._sv_lock:
                if self._sv is None:
                    from funasr import AutoModel

                    logger.info("加载声纹模型 cam++（192 维说话人向量）")
                    self._sv = AutoModel(model="cam++", disable_update=True)
        return self._sv

    # --- 音频取段与向量 ---

    def embedding_of_speaker(self, wav_path: str | Path, segments: list[dict],
                             speaker: str) -> tuple[np.ndarray | None, float]:
        """把某说话人的所有发言段拼起来抽一条声纹，返回 (向量, 参与时长秒)。

        时长不足 MIN_SAMPLE_SECONDS 或原始音频不可读时返回 (None, 0)。
        """
        chunks: list[np.ndarray] = []
        total = 0.0
        try:
            with wave.open(str(wav_path), "rb") as handle:
                if handle.getnchannels() != 1 or handle.getframerate() != SAMPLE_RATE:
                    return None, 0.0
                pcm = np.frombuffer(handle.readframes(handle.getnframes()), dtype=np.int16)
        except Exception:  # noqa: BLE001 - 原始音频读不出就不做声纹，不阻断转写
            logger.warning("声纹取样失败：%s", wav_path, exc_info=True)
            return None, 0.0
        for segment in segments:
            if str(segment.get("speaker")) != str(speaker):
                continue
            start = int(float(segment.get("start") or 0) * SAMPLE_RATE)
            end = int(float(segment.get("end") or 0) * SAMPLE_RATE)
            piece = pcm[max(0, start) : min(len(pcm), end)]
            if piece.size == 0:
                continue
            chunks.append(piece)
            total += piece.size / SAMPLE_RATE
        if total < MIN_SAMPLE_SECONDS or not chunks:
            return None, 0.0
        joined = np.concatenate(chunks)[: int(MAX_SAMPLE_SECONDS * SAMPLE_RATE)]
        result = self._models().generate(input=joined.astype(np.int16).tobytes())
        if not result:
            return None, 0.0
        vector = np.asarray(result[0]["spk_embedding"], dtype=np.float32).reshape(-1)
        return (vector, total) if vector.size else (None, 0.0)

    # --- 对外动作 ---

    def list(self, owner: str | None) -> list[dict]:
        """只回元数据（名字/样本时长/更新时间），不外泄向量本体。"""
        entries = self._read().get(owner or "shared") or {}
        return [
            {"name": name, "sample_seconds": item.get("sample_seconds", 0), "updated_at": item.get("updated_at", "")}
            for name, item in sorted(entries.items())
        ]

    def enroll(self, owner: str | None, name: str, wav_path: str | Path, segments: list[dict], speaker: str) -> dict | None:
        """把某说话人的声纹记在该用户名下（同名覆盖为最新一次标注）。

        时长不足或原始音频不可读时返回 None——标注仍然生效（名字照写），只是不沉淀声纹。
        """
        name = (name or "").strip()
        if not name:
            return None
        vector, seconds = self.embedding_of_speaker(wav_path, segments, speaker)
        if vector is None:
            return None
        key = owner or "shared"
        with self._lock:
            data = self._read()
            entries = dict(data.get(key) or {})
            if name not in entries and len(entries) >= MAX_PER_USER:
                logger.info("声纹条目已达上限 %d，忽略新标注: owner=%s", MAX_PER_USER, key)
                return None
            entries[name] = {
                "vector": _encode(vector),
                "sample_seconds": round(seconds, 1),
                "updated_at": datetime.now().isoformat(timespec="seconds"),
            }
            data[key] = entries
            self._write(data)
        return {"name": name, "sample_seconds": round(seconds, 1)}

    def remove(self, owner: str | None, name: str) -> list[dict]:
        key = owner or "shared"
        with self._lock:
            data = self._read()
            entries = dict(data.get(key) or {})
            entries.pop(name, None)
            data[key] = entries
            self._write(data)
        return self.list(owner)

    def recognize(self, owner: str | None, wav_path: str | Path, segments: list[dict]) -> dict[str, str]:
        """转写完成后：给每个说话人簇找最像的已登记声纹，返回 {speaker: name}。"""
        key = owner or "shared"
        entries = self._read().get(key) or {}
        if not entries or not segments:
            return {}
        matches: dict[str, str] = {}
        for speaker in sorted({str(segment.get("speaker")) for segment in segments}):
            vector, _seconds = self.embedding_of_speaker(wav_path, segments, speaker)
            if vector is None:
                continue
            best_name, best_score = "", 0.0
            for name, item in entries.items():
                stored = _decode(str(item.get("vector") or ""))
                if stored is None or stored.shape != vector.shape:
                    continue
                score = _cosine(vector, stored)
                if score > best_score:
                    best_name, best_score = name, score
            if best_name and best_score >= MATCH_THRESHOLD:
                matches[speaker] = best_name
                logger.info("声纹命中: owner=%s 说话人%s→%s 相似度%.3f", key, speaker, best_name, best_score)
        return matches
