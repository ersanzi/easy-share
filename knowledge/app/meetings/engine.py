"""转写引擎（调用方保留）：实现已迁到通用引擎 `engine/minutecraft`，此处只做转发。

为什么只转发不删：知识服务内部（service.py / tests）仍按 `app.meetings.engine` 导入，
迁移期保留一层薄壳，改名成本与回归风险都最低。真正的实现只有一份：
`engine/minutecraft/_asr_impl.py`（`pip install minutecraft[asr]` 才装 FunASR）。

转写是**采集端**职责：通用引擎只吃转写后的文本，因此 ASR 留在知识服务这一侧。
"""
from __future__ import annotations

import sys
from pathlib import Path

# 让知识服务能直接 import 仓库顶层的通用引擎（`engine/` 不入 site-packages 也能用）
_ENGINE_ROOT = Path(__file__).resolve().parents[3] / "engine"
if str(_ENGINE_ROOT) not in sys.path:
    sys.path.insert(0, str(_ENGINE_ROOT))

from minutecraft._asr_impl import (  # noqa: E402 - 路径注入后再导入
    MeetingTranscriber,
    TranscriptionUnavailable,
    audio_duration,
    funasr_importable,
    render_transcript,
)

__all__ = [
    "MeetingTranscriber",
    "TranscriptionUnavailable",
    "audio_duration",
    "funasr_importable",
    "render_transcript",
]
