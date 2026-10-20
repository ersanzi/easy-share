"""会议导图 harness：对标 mindmap-generator（Dicklesworthstone）的多阶段思想，
裁剪为适配 EasyShare 规模的三阶段管线：

  阶段一 议题切分 —— LLM 通读转写，输出议题列表（名称/概要/时间范围）
  阶段二 逐议题深挖 —— 按时间范围切片转写，逐议题提取要点+细节+负责人（串行，≤6 题）
  阶段三 确定性装配 —— 代码拼装 markdown 大纲（不经 LLM 自由发挥，结构必然合法）

任一阶段失败回退单次调用（summarizer.summarize 的 mindmap 字段），不会比旧版差。
"""
from __future__ import annotations

import json
import logging

logger = logging.getLogger(__name__)

MAX_TOPICS = 6
MAX_POINTS_PER_TOPIC = 8

_STAGE1_SYSTEM = (
    "你是会议结构分析助手。阅读带说话人标签的会议转写，识别会议讨论的议题模块"
    "（如 基础设施与部署 / 文档与知识库 / 客户演示 / 机制与工具 等），"
    '输出严格 JSON：{"topics": [{"name": "议题名(≤12字)", "summary": "一句话概要", '
    '"start": 起始秒数, "end": 结束秒数}]}。'
    "要求：议题 2-6 个，按讨论顺序排列，时间范围覆盖该议题的讨论区间，不重叠；只依据转写内容。"
)

_STAGE2_SYSTEM_TEMPLATE = (
    "你是会议纪要助手。以下是会议中「{topic}」议题的转写片段。提取该议题的讨论要点：\n"
    '输出严格 JSON：{{"points": [{{"text": "要点（保留数据/版本/期限等硬信息）", '
    '"detail": "补充细节或空串", "owner": "负责人说话人名或空串"}}, ...]}}\n'
    "要求：3-{max_points} 条要点；要点一句话，细节拆到 detail；"
    "detail 是转写中的补充事实（数字/期限/背景），不要出现「转写中提到」「概要显示」这类元话语；"
    "owner 填转写中的说话人名（如 说话人1）；只依据片段内容，不编造。"
)


def build_mindmap_harness(
    ask_json,  # callable(system, user) -> dict | None（LLM JSON 询问器）
    transcript_turns: list[dict],
    speaker_names: dict[str, str],
    meeting_title: str = "",
) -> tuple[str, list[str]]:
    """跑三阶段管线，返回 (markdown 大纲, 阶段日志)。任何阶段失败抛 HarnessError。"""
    log: list[str] = []

    def name_of(speaker: str) -> str:
        return speaker_names.get(speaker, f"说话人 {int(speaker) + 1}")

    turns = [
        {
            "start": float(s.get("start") or 0),
            "end": float(s.get("end") or 0),
            "who": name_of(str(s.get("speaker", "0"))),
            "text": str(s.get("text") or "").strip(),
        }
        for s in transcript_turns
        if str(s.get("text") or "").strip()
    ]
    if not turns:
        raise HarnessError("转写为空")
    total_end = max(t["end"] for t in turns)

    # 转写的 LLM 视图（与 render_transcript 相同格式，但带秒号便于切片对齐）
    def render_slice(items: list[dict]) -> str:
        return "\n".join(f"[{t['start']:.0f}-{t['end']:.0f}s] {t['who']}：{t['text']}" for t in items)

    full_view = render_slice(turns)

    # ── 阶段一：议题切分 ──
    stage1 = ask_json(_STAGE1_SYSTEM, "会议转写如下：\n" + full_view)
    if not stage1 or not isinstance(stage1.get("topics"), list) or not stage1["topics"]:
        raise HarnessError("议题切分未返回有效 topics")
    topics = []
    for topic in stage1["topics"][:MAX_TOPICS]:
        name = str(topic.get("name") or "").strip()
        if not name:
            continue
        topics.append(
            {
                "name": name[:24],
                "summary": str(topic.get("summary") or "").strip(),
                "start": max(0.0, float(topic.get("start") or 0)),
                "end": min(total_end, float(topic.get("end") or total_end)),
            }
        )
    if not topics:
        raise HarnessError("议题切分无可用品名")
    # 覆盖全时间轴：首题从头、末题到尾，消除切片盲区
    topics[0]["start"] = 0.0
    topics[-1]["end"] = total_end
    log.append(f"阶段一：识别 {len(topics)} 个议题（{'、'.join(t['name'] for t in topics)}）")

    # ── 阶段二：逐议题深挖（按时间切片，串行） ──
    branches: list[dict] = []
    for index, topic in enumerate(topics):
        span_end = topics[index + 1]["start"] if index + 1 < len(topics) else total_end
        segment = [t for t in turns if topics[index]["start"] <= t["start"] < max(span_end, topics[index]["end"])]
        if not segment:
            segment = [t for t in turns if t["start"] < span_end and t["end"] > topics[index]["start"]]
        view = render_slice(segment) or full_view
        stage2 = ask_json(
            _STAGE2_SYSTEM_TEMPLATE.format(topic=topic["name"], max_points=MAX_POINTS_PER_TOPIC),
            "议题概要：" + (topic["summary"] or "（无）") + "\n\n转写片段：\n" + view,
        )
        points = (stage2 or {}).get("points") if isinstance(stage2, dict) else None
        if not isinstance(points, list) or not points:
            log.append(f"阶段二：议题「{topic['name']}」提取失败，以概要兜底")
            branches.append({"title": topic["name"], "items": [(topic["summary"] or topic["name"], [])]})
            continue
        items = []
        for point in points[:MAX_POINTS_PER_TOPIC]:
            text = str(point.get("text") or "").strip()
            if not text:
                continue
            children = []
            detail = str(point.get("detail") or "").strip()
            if detail:
                children.append(detail)
            owner = str(point.get("owner") or "").strip()
            if owner:
                children.append(f"负责人：{owner}")
            items.append((text, children))
        if not items:
            log.append(f"阶段二：议题「{topic['name']}」要点全空，以概要兜底")
            items = [(topic["summary"] or topic["name"], [])]
        branches.append({"title": topic["name"], "items": items})
        log.append(f"阶段二：议题「{topic['name']}」提取 {len(items)} 条要点")

    # ── 阶段三：确定性装配（不经 LLM，结构必然合法） ──
    lines = [f"# {meeting_title or '会议导图'}"]
    for branch in branches:
        lines.append(f"## {branch['title']}")
        for text, children in branch["items"]:
            lines.append(f"- {text}")
            for child in children:
                lines.append(f"  - {child}")
    log.append("阶段三：装配完成")
    return "\n".join(lines), log


class HarnessError(Exception):
    """harness 管线失败（由调用方回退单次生成）。"""
