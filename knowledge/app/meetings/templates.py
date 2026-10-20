"""听记总结模板（调用方保留）：实现已迁到 `engine/minutecraft/templates.py`。"""
from __future__ import annotations

import sys
from pathlib import Path

_ENGINE_ROOT = Path(__file__).resolve().parents[3] / "engine"
if str(_ENGINE_ROOT) not in sys.path:
    sys.path.insert(0, str(_ENGINE_ROOT))

from minutecraft.templates import *  # noqa: F401,F403,E402 - 薄壳转发
from minutecraft.templates import BUILTIN_TEMPLATES, DEFAULT_TEMPLATE_ID, find_template  # noqa: F401,E402

__all__ = ["BUILTIN_TEMPLATES", "DEFAULT_TEMPLATE_ID", "find_template"]
