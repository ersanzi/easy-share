"""数据结构：转写输入与产物结构的权威定义。

`SCHEMA` 是给 Agent 用的机器可读说明（`minutecraft schema --json`），
和 dataclass 一起维护——改字段时两处必须同步，测试里有一条对账用例。
"""
from __future__ import annotations

from dataclasses import dataclass, field


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

    def render(self, with_timestamps: bool = True) -> str:
        """渲染成喂给 LLM 的转写文本：「张三 [00:12] 内容」。"""
        lines: list[str] = []
        for turn in self.turns:
            who = self.speaker_names.get(turn.speaker, f"说话人 {int(turn.speaker) + 1}" if turn.speaker.isdigit() else turn.speaker)
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
