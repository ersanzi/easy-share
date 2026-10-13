"""FunASR 转写引擎：paraformer（识别）+ fsmn-vad（端点）+ ct-punc（标点）+ cam++（声纹）。

模型在首次转写时才加载（加载耗时数十秒、常驻内存约 2GB），进程内单例。
cam++ 聚类输出说话人序号，重叠语音场景可能错并——已知开源水平，前端支持改名兜底。
"""
from __future__ import annotations

import logging
import re
import threading
import wave
from pathlib import Path

logger = logging.getLogger(__name__)

# 相邻同说话人分段间隔小于该值（秒）时合并为一条发言轮次，贴近阅读习惯
_MERGE_GAP_SECONDS = 1.0

# 纯标点/空白段（声纹聚类偶发碎片）过滤：不含任何文字、数字或字母
_HAS_TEXT = re.compile(r"[\w\u4e00-\u9fff]")


class TranscriptionUnavailable(Exception):
    """funasr 未安装或模型加载失败。"""


def funasr_importable() -> bool:
    """轻量探测 funasr 是否可安装态可用（不真正 import，避免拉起 torch）。"""
    import importlib.util

    return importlib.util.find_spec("funasr") is not None


class MeetingTranscriber:
    """懒加载的转写引擎；spk_enabled=False 时只出转写不出发言人。"""

    def __init__(self, speaker_enabled: bool = True) -> None:
        self._speaker_enabled = speaker_enabled
        self._lock = threading.Lock()
        self._model = None

    def _ensure_model(self):
        if self._model is not None:
            return self._model
        with self._lock:
            if self._model is not None:
                return self._model
            try:
                from funasr import AutoModel
            except ImportError as exc:  # pragma: no cover - 环境缺依赖
                raise TranscriptionUnavailable("funasr 未安装（pip install -r requirements-asr.txt）") from exc
            kwargs: dict = {
                "model": "paraformer-zh",
                "vad_model": "fsmn-vad",
                "vad_kwargs": {"max_single_segment_time": 30000},
                "punc_model": "ct-punc",
                "disable_update": True,
            }
            if self._speaker_enabled:
                kwargs["spk_model"] = "cam++"
            logger.info("加载 FunASR 模型（首次转写触发）: %s", sorted(kwargs))
            self._model = AutoModel(**kwargs)
            return self._model

    def transcribe(self, wav_path: str | Path, hotword: str = "") -> list[dict]:
        """转写单个 WAV，返回发言轮次列表 [{start, end, text, speaker}]（start/end 秒）。

        hotword 是空格分隔的个性化热词串（seaco 模型级上下文增强）；本机 FunASR 实测
        只接受字符串，传 list 会在 os.path.isfile 处 TypeError。
        """
        model = self._ensure_model()
        kwargs: dict = {"input": str(wav_path), "batch_size_s": 300}
        if hotword.strip():
            kwargs["hotword"] = hotword.strip()
        result = model.generate(**kwargs)
        if not result:
            return []
        data = result[0]
        raw: list[dict] = []
        for sentence in data.get("sentence_info") or []:
            text = (sentence.get("text") or "").strip()
            if not text or not _HAS_TEXT.search(text):
                # 空文本或纯标点碎片（聚类/VAD 偶发产物）直接丢弃
                continue
            raw.append(
                {
                    "start": (sentence.get("start") or 0) / 1000.0,
                    "end": (sentence.get("end") or 0) / 1000.0,
                    "text": text,
                    "speaker": str(sentence.get("spk", 0)),
                }
            )
        if not raw and (data.get("text") or "").strip():
            # 无说话人模型等场景退化为整段输出
            raw.append({"start": 0.0, "end": audio_duration(wav_path), "text": data["text"].strip(), "speaker": "0"})
        return merge_turns(raw)

    @property
    def speaker_enabled(self) -> bool:
        return self._speaker_enabled


def audio_duration(wav_path: str | Path) -> float:
    """读取 WAV 时长（秒）；解析失败返回 0。"""
    try:
        with wave.open(str(wav_path), "rb") as handle:
            return round(handle.getnframes() / handle.getframerate(), 2)
    except Exception:  # noqa: BLE001 - 时长仅展示用，失败不阻断
        return 0.0


def merge_turns(segments: list[dict], max_gap: float = _MERGE_GAP_SECONDS) -> list[dict]:
    """相邻同说话人且间隔不大的分段合并为一条发言轮次。"""
    merged: list[dict] = []
    for segment in segments:
        previous = merged[-1] if merged else None
        if (
            previous
            and previous["speaker"] == segment["speaker"]
            and segment["start"] - previous["end"] <= max_gap
        ):
            previous["text"] += segment["text"]
            previous["end"] = segment["end"]
        else:
            merged.append(dict(segment))
    return merged


def render_transcript(segments: list[dict], speaker_names: dict[str, str]) -> str:
    """渲染为带说话人名的纯文本（喂给 LLM 总结 / 归档入库共用）。"""
    lines: list[str] = []
    for segment in segments:
        who = speaker_names.get(segment["speaker"], f"说话人 {int(segment['speaker']) + 1}")
        minutes, seconds = divmod(int(segment["start"]), 60)
        lines.append(f"{who} [{minutes:02d}:{seconds:02d}] {segment['text']}")
    return "\n".join(lines)
