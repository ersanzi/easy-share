from __future__ import annotations

import sys
from pathlib import Path

KNOWLEDGE_ROOT = Path(__file__).resolve().parents[1]
if str(KNOWLEDGE_ROOT) not in sys.path:
    sys.path.insert(0, str(KNOWLEDGE_ROOT))

# 通用引擎（engine/minutecraft）不入 site-packages，产品侧靠 app.meetings.engine 的路径注入导入；
# 测试直接 `from minutecraft import ...`，这里补同一条路径，避免「产品能跑、pytest 收集就红」。
ENGINE_ROOT = KNOWLEDGE_ROOT.parent / "engine"
if ENGINE_ROOT.is_dir() and str(ENGINE_ROOT) not in sys.path:
    sys.path.insert(0, str(ENGINE_ROOT))
