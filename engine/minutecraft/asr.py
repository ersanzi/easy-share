"""`minutecraft.asr` —— **可选**附赠：手上有 wav 时顺带转写。

内核（`minutecraft.engine`）只吃转写文本，不依赖本模块；只有显式
`pip install 'minutecraft[asr]'` 并调用它时才需要 FunASR。产品侧的转写仍在 EasyShare 知识服务里，
这里是为了让 Agent 在「手上只有一个录音文件」的场景也能一条命令跑通。

用法：
    from minutecraft.asr import transcribe_wav
    turns = transcribe_wav("meeting.wav", speaker_enabled=True)  # -> [TranscriptTurn, ...]
"""
from __future__ import annotations

from .models import TranscriptTurn


def asr_available() -> bool:
    """funasr 是否可导入（轻量探测，不真的 import，避免拉起 torch）。"""
    import importlib.util

    return importlib.util.find_spec("funasr") is not None


def transcribe_wav(path: str, *, speaker_enabled: bool = True, hotword: str = "") -> list[TranscriptTurn]:
    """转写单个 16kHz 单声道 wav，返回发言轮次。缺 funasr 时抛 RuntimeError（带安装指引）。"""
    try:
        from ._asr_impl import MeetingTranscriber
    except ImportError as exc:  # pragma: no cover - 未装可选依赖
        raise RuntimeError("未安装 ASR 依赖：pip install 'minutecraft[asr]'") from exc
    segments = MeetingTranscriber(speaker_enabled=speaker_enabled).transcribe(path, hotword=hotword)
    return [
        TranscriptTurn(
            start=float(item.get("start") or 0),
            end=float(item.get("end") or 0),
            text=str(item.get("text") or ""),
            speaker=str(item.get("speaker", "0")),
        )
        for item in segments
    ]
