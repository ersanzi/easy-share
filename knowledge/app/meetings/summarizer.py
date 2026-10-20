"""会议总结（调用方保留）：实现已迁到通用引擎 `engine/minutecraft/summarizer.py`。

知识服务继续按 `from app.meetings.summarizer import MeetingSummarizer` 使用；
实现只有一份，测试（54 例听记用例）也继续通过这层壳回归。
"""
from __future__ import annotations

import sys
from pathlib import Path

_ENGINE_ROOT = Path(__file__).resolve().parents[3] / "engine"
if str(_ENGINE_ROOT) not in sys.path:
    sys.path.insert(0, str(_ENGINE_ROOT))

from minutecraft.summarizer import *  # noqa: F401,F403,E402 - 薄壳转发
from minutecraft.summarizer import MeetingSummarizer  # noqa: F401,E402

__all__ = ["MeetingSummarizer"]
