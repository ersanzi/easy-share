"""会议导图 harness（调用方保留）：实现已迁到 `engine/minutecraft/mindmap_harness.py`。"""
from __future__ import annotations

import sys
from pathlib import Path

_ENGINE_ROOT = Path(__file__).resolve().parents[3] / "engine"
if str(_ENGINE_ROOT) not in sys.path:
    sys.path.insert(0, str(_ENGINE_ROOT))

from minutecraft.mindmap_harness import *  # noqa: F401,F403,E402 - 薄壳转发
from minutecraft.mindmap_harness import build_mindmap_harness  # noqa: F401,E402

__all__ = ["build_mindmap_harness"]
