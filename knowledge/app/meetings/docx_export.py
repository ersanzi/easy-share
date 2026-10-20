"""听记 Word 导出（调用方保留）：实现已迁到 `engine/minutecraft/docx/`。

- `build_minutes_docx`：钉钉版式纪要（封面汇报卡 + 文档体 + AI洞察 + 待办 + 章节 + 行动项表）
- `visual_sections` / `build_visual_report`：可视化报告结构，前端汇报卡共用同一口径
"""
from __future__ import annotations

import sys
from pathlib import Path

_ENGINE_ROOT = Path(__file__).resolve().parents[3] / "engine"
if str(_ENGINE_ROOT) not in sys.path:
    sys.path.insert(0, str(_ENGINE_ROOT))

from minutecraft.docx.export import ACCENTS, build_minutes_docx  # noqa: F401,E402
from minutecraft.docx.visual import build_visual_report, visual_sections  # noqa: F401,E402

__all__ = ["ACCENTS", "build_minutes_docx", "build_visual_report", "visual_sections"]
