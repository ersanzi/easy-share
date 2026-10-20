"""minutecraft —— 转写文本 → 会议纪要的通用引擎（EasyShare 听记与外部 Agent 共用）。

设计边界（重要）：
  **本引擎只吃「已经转写好的文本」，不做音频转写。** 语音识别（FunASR / whisper / 云端 ASR）
  属于采集端的职责，留在 EasyShare 知识服务的 `app/meetings/engine.py`；仓库里替它保留了一个
  可选的 `asr` 模块（`pip install minutecraft[asr]`），只是给「手上就是一段 wav」的人行个方便，
  内核不依赖它。

三层能力，彼此解耦、可单独调用：
  1. 转写规范化  —— segments/纯文本 → 统一的 turns 结构（带说话人、时间戳）
  2. 结构化产出  —— LLM 通读 → 纪要/要点/待办/章节/关键词/AI洞察/核心发言/可视化报告/导图大纲
  3. 版式导出    —— 钉钉版式 Word、Markdown、可视化报告 JSON

Agent 接入面（WorkBuddy / DSH / Codex 等）：
  - 命令行：`minutecraft run transcript.txt --out out/`（或 `python -m minutecraft`）
  - Python：`from minutecraft import MinuteEngine, Transcript`（见 engine.py）
  - 能力清单：`minutecraft schema --json` 输出机器可读的产物结构，供 Agent 组织提示与校验

环境变量（LLM 走 OpenAI 兼容接口）：
  MINUTECRAFT_LLM_BASE_URL / MINUTECRAFT_LLM_API_KEY / MINUTECRAFT_LLM_MODEL
  兼容回退：LLM_BASE_URL / LLM_API_KEY / LLM_MODEL；MINUTECRAFT_LLM_TRUST_ENV=false 直连不走代理
"""
from __future__ import annotations

from .engine import MinuteEngine, MinuteError, MinuteResult
from .models import SCHEMA, Transcript, TranscriptTurn
from .templates import BUILTIN_TEMPLATES, DEFAULT_TEMPLATE_ID, find_template

__all__ = [
    "BUILTIN_TEMPLATES",
    "DEFAULT_TEMPLATE_ID",
    "MinuteEngine",
    "MinuteError",
    "MinuteResult",
    "SCHEMA",
    "Transcript",
    "TranscriptTurn",
    "find_template",
]

__version__ = "0.1.0"
