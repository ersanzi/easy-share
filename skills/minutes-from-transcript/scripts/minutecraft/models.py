"""数据结构：转写输入与产物结构的权威定义。

`SCHEMA` 是给 Agent 用的机器可读说明（`minutecraft schema --json`），
和 dataclass 一起维护——改字段时两处必须同步，测试里有一条对账用例。
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .textutil import is_filler, join_text


@dataclass(slots=True)
class TranscriptTurn:
    """一条发言。speaker 是引擎侧序号字符串（"0"/"1"…），展示名由 speaker_names 映射。"""

    start: float
    end: float
    text: str
    speaker: str = "0"


@dataclass(slots=True)
class Transcript:
    """引擎的输入：已转写好的文本 +（可选的）说话人与元信息。

    - turns：结构化发言轮次，最准确；纯文本也能给（见 Transcript.from_text 自行构造）
    - speaker_names：{"0": "张三"}，决定转写里的人名与「核心发言」认谁
    - title / created_at / duration_seconds：写进纪要头部的元信息
    """

    turns: list[TranscriptTurn] = field(default_factory=list)
    speaker_names: dict[str, str] = field(default_factory=dict)
    title: str = "会议纪要"
    created_at: str = ""
    duration_seconds: float | None = None

    def _display_name(self, turn: TranscriptTurn) -> str:
        if turn.speaker in self.speaker_names:
            return self.speaker_names[turn.speaker]
        return f"说话人 {int(turn.speaker) + 1}" if turn.speaker.isdigit() else turn.speaker

    def render(
        self,
        with_timestamps: bool = True,
        *,
        compact: bool = True,
        drop_filler: bool = True,
        max_chars: int = 220,
        gap_seconds: float = 45.0,
    ) -> str:
        """渲染成喂给 LLM 的转写文本：「张三 [00:12] 内容」。

        `compact=True`（默认）会做两件**只影响喂模型材料**的事，原始 turns 不变：

        1. 丢掉纯语气词碎句（「嗯」「行嘞行」「好的好的」）——ASR 按秒切段时这类行极多，
           每行都带时间戳与说话人，一场长会能白烧几千 tokens；
        2. 把同一说话人、相邻且间隔不超过 `gap_seconds` 的短句合并成一段（每段不超过
           `max_chars` 字），只保留段首时间戳。时间分辨率仍在几十秒级，足够章节/议题切分，
           但喂进去的「说话人 + 时间戳」从几百行降到几十行。

        实测一份 82 分钟、逐秒切段的转写，材料从 ~1.9 万字降到 ~1.1 万字，且模型不再
        被「嗯/好的」这类噪音干扰。要原样逐行喂模型时传 `compact=False`。
        """
        if not compact:
            return self._render_lines(with_timestamps)

        turns = [turn for turn in self.turns if turn.text.strip()]
        if drop_filler:
            kept = [turn for turn in turns if not is_filler(turn.text)]
            # 整份都是「嗯/好的」时别删空，退回原始行
            turns = kept or turns
        if not turns:
            return ""

        blocks: list[dict] = []
        current: dict | None = None
        for turn in turns:
            text = turn.text.strip()
            if (
                current is None
                or turn.speaker != current["speaker"]
                or len(current["text"]) + len(text) > max_chars
                or turn.start - current["end"] > gap_seconds
            ):
                current = {"speaker": turn.speaker, "start": turn.start, "end": max(turn.end, turn.start), "text": text}
                blocks.append(current)
                continue
            current["text"] = join_text(current["text"], text)
            current["end"] = max(turn.end, turn.start, current["end"])

        lines: list[str] = []
        for block in blocks:
            who = self._display_name(TranscriptTurn(block["start"], block["end"], block["text"], block["speaker"]))
            if not with_timestamps:
                lines.append(f"{who}: {block['text']}")
                continue
            minutes, seconds = divmod(int(block["start"]), 60)
            lines.append(f"{who} [{minutes:02d}:{seconds:02d}] {block['text']}")
        return "\n".join(lines)

    def _render_lines(self, with_timestamps: bool) -> str:
        """逐行渲染（`compact=False` 的旧行为）。"""
        lines: list[str] = []
        for turn in self.turns:
            who = self._display_name(turn)
            if not with_timestamps:
                lines.append(f"{who}: {turn.text}")
                continue
            minutes, seconds = divmod(int(turn.start), 60)
            lines.append(f"{who} [{minutes:02d}:{seconds:02d}] {turn.text}")
        return "\n".join(lines)

    @property
    def speakers(self) -> list[str]:
        seen: list[str] = []
        for turn in self.turns:
            if turn.speaker not in seen:
                seen.append(turn.speaker)
        return seen


# 产物结构（供 Agent 组织提示与校验）。与 summarizer._parse 的归一输出一一对应。
SCHEMA: dict = {
    "version": "0.1.0",
    "input": {
        "turns": [{"start": "秒", "end": "秒", "text": "发言内容", "speaker": "说话人序号"}],
        "speaker_names": {"0": "展示名"},
        "title": "会议标题",
        "created_at": "ISO 时间",
        "duration_seconds": "秒（可选）",
    },
    "output": {
        "minutes": "默认模板：400 字以内纪要段落",
        "key_points": ["要点，保留数字/期限"],
        "sections": [{"title": "小节标题", "text": "段落或 - 列表"}],
        "chapters": [{"title": "章节名", "start": "秒", "end": "秒", "gist": "一句话"}],
        "todos": [{"task": "待办", "owner_hint": "负责人", "due_hint": "期限"}],
        "keywords": ["关键词"],
        "insights": ["AI 洞察"],
        "quote_kp": {"speaker": "核心发言人", "points": ["观点"], "quotes": ["原话"]},
        "mindmap": "markdown 层级大纲",
        "topics": [
            {
                "title": "报告议题名",
                "start": "秒", "end": "秒",
                "gist": "一句话摘要",
                "focus": ["本段讨论聚焦于…（可含 <span class=\"warm\">关键词</span>）"],
                "sections": [
                    {"title": "分节名", "groups": [{"title": "看法或子标题", "items": ["要点"]}]}
                ],
                "insights": ["针对本议题的判断"],
            }
        ],
        "visual": {
            "summary": "一句话主题",
            "sections": [
                {
                    "title": "模块名",
                    "layout": "cards|duo|flow|list|closing",
                    "accent": "rose|blue|teal|amber|green",
                    "items": [{"title": "卡片名", "tag": "已解决/待明确/风险…", "bullets": ["要点"], "note": "小结"}],
                }
            ],
        },
    },
    "exports": {
        "docx": "钉钉版式纪要（封面可视化报告 + 文档体 + AI洞察 + 待办 + 章节 + 核心发言 + 行动项表）",
        "md": "全量 Markdown（纪要/要点/关键词/待办/章节/洞察/导图/转写）",
        "visual.json": "可视化报告结构（色卡封面用，供前端画出汇报卡）",
        "report.json": "长网页报告的现成数据（`minutecraft render report.json -o x.png` 直接出图）",
    },
}
