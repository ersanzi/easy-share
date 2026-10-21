"""会议总结：转写全文 → LLM 生成 {minutes, key_points, todos}。

复用与 /query 同一套 LLM 配置（OpenAI 兼容）；未配置 LLM 或生成失败时返回 None，
听记仍是完整转写——总结是增值层，不是可用性前提。
"""
from __future__ import annotations

import json
import logging
import os
import re
import time

logger = logging.getLogger(__name__)

# 超过该字符数先分段小结再汇总（map-reduce），保护长会议的上下文
_REDUCE_THRESHOLD = 45000
_REDUCE_CHUNK = 30000

# --- 汇报卡（对标钉钉「可视化报告」：封面六模块色卡 + 结构化字段） ---

# 钉钉实测三色：01/05 玫红、02 蓝、03 青、04 琥珀、06 绿
_ACCENTS = ("rose", "blue", "teal", "amber", "rose", "green")
_LAYOUT_KINDS = ("cards", "duo", "flow", "list", "closing")


def _visual_prompt(template: dict | None) -> str:
    """可视化报告字段的形状与口径；默认模板把纪要/要点折叠成前两卡，其余模板按 sections 展开。"""
    if not template or template.get("id") == "meeting-minutes":
        shape = (
            '"visual": {"summary": "一句话主题说明，30字内，写清诊断/讨论出了什么", "sections": ['
            '{"title": "会议纪要", "layout": "cards", "items": ['
            '{"title": "小节名", "tag": "已解决", "bullets": ["要点"], "note": "小结一句话"}]}, '
            '{"title": "关键要点", "layout": "list", "items": [{"title": "要点名", "bullets": ["细节"]}]}, '
            '{"title": "关键共识", "layout": "closing", "items": [{"title": "", "note": "共识一句话"}]}]}'
        )
    else:
        shape = (
            '"visual": {"summary": "一句话主题说明，30字内", "sections": ['
            '{"title": "小节名（与 sections 对应）", "layout": "cards|duo|flow|list|closing", "items": ['
            '{"title": "卡片名", "tag": "已解决|待明确|待确认|风险|新规", '
            '"bullets": ["要点"], "note": "小结一句话"}]}]}'
        )
    return (
        shape
        + "\nvisual 字段是供导出「可视化报告 / 汇报卡」的结构化结果，口径：\n"
        "1. sections 3-6 个，每个 section 的 title 为 6-12 字、概括一个议题；\n"
        "2. items 每节 1-4 个；title 4-12 字，是该节下的子议题；bullets 2-4 条，"
        "保留具体数据、人名、期限（数字必须来自转写）；note 为可选小结（20 字内）；\n"
        "3. tag 只能从「已解决/待明确/待确认/风险/新规」中选，判断不了就留空字符串；\n"
        '4. layout 取值：cards=并列色卡（2x2），duo=两栏对比，flow=左右进程与节点，'
        "list=清单，closing=单条共识；按内容形态自选，不要每条都用 cards；\n"
        "5. 最后一节用 closing 收口本场达成的共识。只依据转写，不编造。"
    )


def _template_prompt(template: dict | None, with_keywords: bool = False,
                     with_chapters: bool = True, with_mindmap: bool = True) -> str:
    """按模板生成 sections 结构指令；默认模板走旧版固定字段（纪要/要点），避免回归。

    `with_keywords=True` 时把 `keywords` 一并要在这个回答里——关键词本来只要 8-10 个词，
    单独跑一次调用要重发一整份转写，在 Agent 场景里等于多一个来回（一次工具调用几十秒）。
    合并后由 `_parse` 取出，`finish` 仍然接受单独补答的 `answers.keywords`。

    `with_chapters=False` / `with_mindmap=False` 用于「议题已另答」的两步走：这两个字段
    都能从 `topics` 无损派生（标题/起止/gist + 分节要点），再让模型写一遍等于白烧一遍
    output token —— 而宿主 Agent 慢就慢在 output token 上。
    """
    keywords_shape = (
        '"keywords": ["关键词", "按重要性从高到低，6-10 个名词或短短语，每个不超过 10 字"], '
        if with_keywords
        else ""
    )
    chapters_shape = (
        '"chapters": [{"title": "章节标题", "start": "mm:ss", "end": "mm:ss", "gist": "一句话内容概括"}], '
        if with_chapters
        else ""
    )
    insights_shape = (
        '"insights": ["AI 洞察 2-4 条，每条 40 字内：给出转写里没有明写、由内容推出的判断或风险"], '
        '"quote_kp": {"speaker": "核心发言人（用转写里的说话人名）", "points": ["该发言人的观点，2-4 条"], '
        '"quotes": ["该发言人的原文引用，1-2 句，必须逐字来自转写"]}, '
    )
    mindmap_shape = ', "mindmap": "..."' if with_mindmap else ""
    if not template or template.get("id") == "meeting-minutes":
        return (
            '{"minutes": "400字以内的会议纪要，按议题归纳，保留数据与结论", '
            '"key_points": ["讨论要点，每条一句话，保留数据/数字/期限"], '
            + keywords_shape + chapters_shape + insights_shape +
            '"todos": [...]' + mindmap_shape + '}'
        )
    return (
        '{"sections": [{"title": "小节标题", "content": "本节内容：要么是连贯段落，要么是多行以 `- ` 开头的列表"}], '
        + keywords_shape + chapters_shape + insights_shape +
        '"todos": [...]' + mindmap_shape + '}\n'
        f"sections 按以下结构组织（小节标题与顺序遵循）：{template.get('sections')}"
    )


_SYSTEM_PROMPT_HEAD = (
    "你是企业会议纪要助手。根据带说话人标签的会议转写，输出严格的 JSON 对象。JSON 形状为：\n"
)

_TAIL_TODOS = (
    '\ntodos 形如 [{"task": "待办事项", "owner_hint": "负责人或空字符串", "due_hint": "期限或空字符串"}]，'
    "任何模板都必须提取待办。\n"
)
_TAIL_CHAPTERS = (
    'chapters 是智能切片：把转写按话题切成 3-8 段，start/end 直接抄转写行里的 [mm:ss] 时间戳'
    "（start 为该话题第一句的时间，end 为下一话题第一句的时间或最后一句时间）；"
    "title 10 字以内概括话题，gist 一句话说明这段聊了什么；按时间顺序排列、不重叠。\n"
)
_TAIL_MINDMAP = (
    'mindmap 是会议结构的 markdown 层级大纲：第一行 "# 会议主题"；二级 "## 议题/模块"'
    "（按会议实际议题划分）；三级 \"- 要点\"；四级 \"  - 补充细节\"（可选）。要求：\n"
    "1. 总节点 15-40 个，宁可信息完整也不要干瘪；\n"
    "2. 要点保留具体数据（数量/版本/期限/负责人），负责人用转写中的说话人名；\n"
    "3. 结论与待办单独成支；\n"
    "4. 只依据转写内容，不编造。"
)
_SYSTEM_PROMPT_TAIL = _TAIL_TODOS + _TAIL_CHAPTERS + _visual_prompt(None) + "\n" + _TAIL_MINDMAP

# 议题已单独成请求时，这两项由 `chapters_from_topics` / `mindmap_from_topics` 派生：
# 提示词里既不给形状也不给写作规范，避免模型又写一遍（实测这两项占 summary 输出约 1/3）。
_DERIVE_NOTE = "\nchapters 与 mindmap 由报告议题结果派生，本次**不要输出**这两个字段。\n"


def _system_prompt(template: dict | None, with_keywords: bool = False,
                   derive_from_topics: bool = False) -> str:
    if derive_from_topics:
        tail = _TAIL_TODOS + _DERIVE_NOTE + _visual_prompt(None) + "\n"
        return _SYSTEM_PROMPT_HEAD + _template_prompt(
            template, with_keywords, with_chapters=False, with_mindmap=False
        ) + tail
    return _SYSTEM_PROMPT_HEAD + _template_prompt(template, with_keywords) + _SYSTEM_PROMPT_TAIL



_TAG_SET = ("已解决", "待明确", "待确认", "风险", "新规")


def _clean_str(value, limit: int = 200) -> str:
    """归一为单行文本并截断（LLM 常往标题里塞换行与多余空白）。"""
    return " ".join(str(value or "").split())[:limit]


def _parse_visual(raw) -> dict | None:
    """归一 LLM 给的可视化报告结构；形状不合法返回 None，由导出侧兜底成普通卡。"""
    if not isinstance(raw, dict):
        return None
    sections: list[dict] = []
    for section in raw.get("sections") or []:
        if not isinstance(section, dict):
            continue
        title = _clean_str(section.get("title"), 20)
        if not title:
            continue
        layout = _clean_str(section.get("layout"), 12)
        if layout not in _LAYOUT_KINDS:
            layout = "cards"
        items: list[dict] = []
        for item in section.get("items") or []:
            if not isinstance(item, dict):
                continue
            bullets = [
                _clean_str(bullet, 120)
                for bullet in (item.get("bullets") or [])
                if _clean_str(bullet, 120)
            ][:5]
            note = _clean_str(item.get("note"), 60)
            item_title = _clean_str(item.get("title"), 20)
            if not (item_title or bullets or note):
                continue
            tag = _clean_str(item.get("tag"), 8)
            items.append({
                "title": item_title,
                "tag": tag if tag in _TAG_SET else "",
                "bullets": bullets,
                # 钉钉版式的小结条：空则导出侧不画
                "note": note,
            })
        if not items:
            continue
        if layout == "duo" and len(items) > 2:
            layout = "cards"
        sections.append({
            "title": title,
            "layout": layout,
            "items": items[:4],
            # 色带轮转：钉钉 01/05 同为玫红，故按固定序列取色保证跨节不撞
            "accent": _ACCENTS[len(sections) % len(_ACCENTS)],
        })
    if not sections:
        return None
    return {"summary": _clean_str(raw.get("summary"), 60), "sections": sections[:6]}


def _parse_insights(raw) -> list[str]:
    points: list[str] = []
    for item in raw or []:
        text = _clean_str(item, 80)
        if text and text not in points:
            points.append(text)
    return points[:4]


def _parse_quote_kp(raw) -> dict | None:
    if not isinstance(raw, dict):
        return None
    quotes = [_clean_str(item, 160) for item in raw.get("quotes") or [] if _clean_str(item, 160)]
    points = [_clean_str(item, 100) for item in raw.get("points") or [] if _clean_str(item, 100)]
    speaker = _clean_str(raw.get("speaker"), 20)
    if not quotes and not points:
        return None
    return {"speaker": speaker, "points": points[:4], "quotes": quotes[:2]}


def _parse_mmss(value) -> int | None:
    """把 LLM 给的时间锚点归一为秒；接受 "mm:ss"、"hh:mm:ss" 或纯秒数，坏值返回 None。"""
    if isinstance(value, (int, float)) and value >= 0:
        return int(value)
    text = str(value or "").strip()
    if not text:
        return None
    parts = text.split(":")
    if len(parts) > 3 or not all(part.strip().isdigit() for part in parts):
        return None
    seconds = 0
    for part in parts:
        seconds = seconds * 60 + int(part)
    return seconds


def _format_mmss(seconds: int) -> str:
    minutes, rest = divmod(max(int(seconds), 0), 60)
    return f"{minutes:02d}:{rest:02d}"


def _parse_chapters(raw) -> list[dict]:
    """智能切片归一：坏项丢弃、按时间排序；全坏返回空列表（前端回退无章节条）。"""
    if not isinstance(raw, list):
        return []
    chapters: list[dict] = []
    for item in raw:
        if not isinstance(item, dict):
            continue
        title = str(item.get("title") or "").strip()
        start = _parse_mmss(item.get("start"))
        if not title or start is None:
            continue
        end = _parse_mmss(item.get("end"))
        if end is None or end < start:
            end = start
        chapters.append({
            "title": title[:30],
            "start": start,
            "end": end,
            "gist": str(item.get("gist") or "").strip(),
        })
    chapters.sort(key=lambda chapter: chapter["start"])
    # 模型常只给 start 不给 end（或给了 ≤start 的值），留成零长度会让导航条显示「20:32 – 20:32」。
    # 用下一章的 start 补齐，最后一章落回自己的 start（时长由渲染侧用转写总长兜底）。
    for index, chapter in enumerate(chapters):
        if chapter["end"] <= chapter["start"] and index + 1 < len(chapters):
            chapter["end"] = max(chapters[index + 1]["start"], chapter["start"])
    return chapters


def _parse_topics(raw) -> list[dict] | None:
    """归一「报告议题」结构（长网页报告的正文骨架，对标钉钉听记的章节报告）。

    与 `_parse_chapters`（智能切片，只带一句 gist）的区别：这里的议题自带分节、
    讨论聚焦与议题级洞察，是**正文**而不是导航条。任何一项不合法都只丢那一项，
    整段无法识别返回 None（调用方据此回退到旧渲染路径，不会让渲染直接失败）。
    """
    if not isinstance(raw, list):
        return None
    topics: list[dict] = []
    for item in raw:
        if not isinstance(item, dict):
            continue
        title = _clean_str(item.get("title"), 30)
        if not title:
            continue
        start = _parse_mmss(item.get("start"))
        topic: dict = {"title": title, "start": start if start is not None else 0}
        end = _parse_mmss(item.get("end"))
        if end is not None and end >= topic["start"]:
            topic["end"] = end
        gist = _clean_str(item.get("gist") or item.get("summary"), 120)
        if gist:
            topic["gist"] = gist

        focus = [
            text for text in (_clean_str(point, 200) for point in (item.get("focus") or [])) if text
        ][:3]
        if focus:
            topic["focus"] = focus

        sections: list[dict] = []
        for section in item.get("sections") or []:
            if not isinstance(section, dict):
                continue
            section_title = _clean_str(section.get("title"), 24)
            if not section_title:
                continue
            groups: list[dict] = []
            for group in section.get("groups") or []:
                if not isinstance(group, dict):
                    continue
                group_title = _clean_str(group.get("title"), 40)
                items = [
                    text for text in (_clean_str(line, 200) for line in (group.get("items") or [])) if text
                ][:4]
                if items:
                    groups.append({"title": group_title, "items": items})
            # 也接受不分组的 bullets（弱模型常见）：折成同一形状，导出侧只认 groups
            bullets = [
                text for text in (_clean_str(line, 200) for line in (section.get("bullets") or [])) if text
            ][:6]
            if bullets:
                groups.append({"title": "", "items": bullets})
            if groups:
                sections.append({"title": section_title, "groups": groups[:4]})
        if sections:
            topic["sections"] = sections[:4]

        insights = [
            text for text in (_clean_str(point, 160) for point in (item.get("insights") or [])) if text
        ][:3]
        if insights:
            topic["insights"] = insights
        topics.append(topic)

    if not topics:
        return None
    # 按时间排序；没给 end 的用下一个议题的 start 补齐（最后一个用上一段的时长）
    topics.sort(key=lambda topic: topic["start"])
    for index, topic in enumerate(topics):
        if "end" not in topic:
            nxt = topics[index + 1]["start"] if index + 1 < len(topics) else topic["start"]
            topic["end"] = max(nxt, topic["start"])
    return topics[:10]


def chapters_from_topics(topics: list[dict]) -> list[dict]:
    """报告议题 → 导航章节。

    议题的 title/start/end/gist 与章节同构，而且分得比模型另写一遍更准（同一份转写、
    同一套时间轴）。归一仍走 `_parse_chapters`，坏项过滤与 end 补齐逻辑保持一致。
    """
    return _parse_chapters([
        {
            "title": topic.get("title") or "",
            "start": topic.get("start"),
            "end": topic.get("end"),
            "gist": topic.get("gist") or "",
        }
        for topic in topics or []
    ])


def mindmap_from_topics(meeting_title: str, topics: list[dict], todos: list[dict] | None = None,
                        insights: list[str] | None = None) -> str:
    """报告议题 → markdown 层级大纲（`## 议题` → `- 分组` → `  - 要点`）。

    分节里的 group 本身就是「判断或做法」，比让模型再压缩一遍更具体；待办/洞察单独成支，
    与提示词里对导图的要求（结论与待办单独成支）一致。
    """
    lines: list[str] = [f"# {meeting_title or '会议纪要'}"]
    for topic in topics or []:
        title = str(topic.get("title") or "").strip()
        if not title:
            continue
        lines.append(f"## {title}")
        wrote = False
        for section in topic.get("sections") or []:
            for group in section.get("groups") or []:
                group_title = str(group.get("title") or "").strip()
                items = [str(item).strip() for item in (group.get("items") or []) if str(item).strip()]
                if group_title:
                    lines.append(f"- {group_title}")
                    lines.extend(f"  - {item}" for item in items[:3])
                    wrote = True
                else:
                    # 弱模型只给 bullets 时没有小标题，直接挂在议题下
                    lines.extend(f"- {item}" for item in items[:4])
                    wrote = True
        if not wrote:
            gist = str(topic.get("gist") or "").strip()
            if gist:
                lines.append(f"- {gist}")
    if todos:
        lines.append("## 待办")
        for todo in todos:
            task = str(todo.get("task") or "").strip()
            if not task:
                continue
            suffix = "，".join(value for value in (todo.get("owner_hint"), todo.get("due_hint")) if value)
            lines.append(f"- {task}" + (f"（{suffix}）" if suffix else ""))
    if insights:
        lines.append("## 洞察")
        lines.extend(f"- {insight}" for insight in insights if insight)
    return "\n".join(lines)


def _extract_last_json(text: str) -> dict | None:
    """从混有思考文本的 LLM 输出中提取最后一个可解析的 JSON 对象。

    推理型模型（如 deepseek-flash 经中转）会在 JSON 前泄漏思维链，贪心正则可能
    抓到错误片段；这里逐 "{" 位置尝试 raw_decode，取最后一个完整对象。
    """
    decoder = json.JSONDecoder()
    last: dict | None = None
    position = text.find("{")
    while position != -1:
        try:
            value, _ = decoder.raw_decode(text[position:])
            if isinstance(value, dict):
                last = value
        except json.JSONDecodeError:
            pass
        position = text.find("{", position + 1)
    return last


def _parse_json_object(raw: str | None) -> dict | None:
    """把模型返回的原始文本解析成 JSON 对象（剥 ``` 围栏、容忍前后混入的思维链）。

    关键词与议题两条回答共用，避免「围栏怎么剥」在两处各写一遍、口径分叉。
    """
    text = (raw or "").strip()
    if not text:
        return None
    if text.startswith("```"):
        text = re.sub(r"^```[a-zA-Z]*\n?", "", text).rstrip("`").strip()
    try:
        parsed = json.loads(text)
    except json.JSONDecodeError:
        parsed = _extract_last_json(text)
    return parsed if isinstance(parsed, dict) else None

# 429 / 5xx / 连接抖动的最小重试：Agent 跑在别人家的额度池上，一次限流不该让整份纪要失败。
# 只重试「retryable」类别，400/401/404 这类配置错误立即返回，避免把错配拖成十几秒的空等。
_RETRYABLE_STATUS = {408, 409, 425, 429, 500, 502, 503, 504}
_RETRY_ATTEMPTS = int(os.environ.get("MINUTECRAFT_LLM_RETRIES") or 4)
_RETRY_BASE_SECONDS = float(os.environ.get("MINUTECRAFT_LLM_RETRY_BASE") or 3.0)

# 输出预算。推理型模型（deepseek-v4-flash）的思考与输出共享同一个上限，
# 实测 topics 一次吃掉 18803 思考 token + 5607 输出 token 才出 15267 字，
# 所以「要写很多东西」的请求必须给足，否则会 `finish_reason=length` 且内容为空。
_TOPICS_MAX_TOKENS_DEFAULT = int(os.environ.get("MINUTECRAFT_TOPICS_MAX_TOKENS") or 32768)
_SUMMARY_MAX_TOKENS = int(os.environ.get("MINUTECRAFT_SUMMARY_MAX_TOKENS") or 32768)

# 思考档位 —— 这是本引擎**唯一真正有效的提速开关**（实测同一份 82 分钟转写）：
#   none  → topics 32s（思考 0 token）  summary 27s  keywords 1.5s
#   low   → topics 113s（思考 6848）    summary 16s
#   high  → topics 246s（思考 21427）   summary 90s+
# 关键事实：**思考量由模型自己加码，受输出预算挤不下来**——把预算从 32768 砍到 16384
# 不会让它想少点，只会让它在思考阶段撞墙、`content` 为空（141s 白等）。
# 所以提速要调档位，不是调 max_tokens。
_THINKING_LEVELS = {"none", "low", "medium", "high"}


def _thinking_default() -> str:
    level = (os.environ.get("MINUTECRAFT_THINKING") or "auto").strip().lower()
    if level == "auto":
        return "auto"
    return level if level in _THINKING_LEVELS else "auto"


def _is_hollow_minutes(raw: str) -> bool:
    """回答是不是「合法 JSON 但正文全空」的空壳。

    真踩过：`low` 档下模型回了一个带 `minutes: ""`、`key_points: []` 的对象，
    解析一路绿灯，最后产出 371 字节的 minutes.md 和一份空白 Word——**看着像成功**。
    宁可判成失败重试一次，也不要交一份空文档。
    """
    if not (raw or "").strip():
        return True
    parsed = _parse_json_object(raw)
    if parsed is None:
        return False  # 解析不了交给 _parse 的回退分支，这里不抢判断
    return not any(parsed.get(key) for key in ("minutes", "key_points", "chapters", "sections", "visual", "mindmap"))


def _thinking_for(material: str, explicit: str | None = None) -> str:
    """按转写长短自动选思考档位。

    **为什么内容越多档位越低（而不是越高）**：实测同一份 82 分钟转写，
    `none` 与 `low` 产出的长报告密度相当（10 议题 / 52 要点 vs 8 / 74），但耗时 76s vs 143s；
    而模型对长文本「想得更久」并不换来更好的纪要——真正决定质量的是它照不照做提示词，
    不是它想多久。长会本就慢，再叠高档只会更慢，所以越长越往 `none` 压。

    阈值按实测的秒级延迟算（转写字数 = 分钟数 × 约 165）：
      ≤3k 字  ≈ 3 分钟内的短会 → medium（想清楚，质优）
      ≤8k 字  ≈ 50 分钟内      → low
      >8k 字  ≈ 1 小时以上长会 → none（一整场 82 分钟会议实测 76s vs 143s）
    显式给了档位就用显式的（`--thinking` / `MINUTECRAFT_THINKING` 优先）。
    """
    if explicit in _THINKING_LEVELS:
        return explicit
    size = len(material or "")
    if size >= _THINKING_LONG_CHARS:
        return "none"
    if size >= _THINKING_MID_CHARS:
        return "low"
    return "medium"


_THINKING_MID_CHARS = int(os.environ.get("MINUTECRAFT_THINKING_MID_CHARS") or 3000)
_THINKING_LONG_CHARS = int(os.environ.get("MINUTECRAFT_THINKING_LONG_CHARS") or 8000)


def _reasoning_param(level: str) -> dict:
    """把档位翻成 OpenAI 的 `reasoning_effort`；'none' 要退化成关思考而不是传过去。

    各家实现关思考的方式不一样：OpenAI 系认 `reasoning_effort="none"`，
    部分中转只认 `minimal`/不认这个字段。实测这个端点 `none` 收到的就是 0 思考 token，
    但也见过方言不吃该字段，所以 `_create_with_retry` 会剥掉它再试一次（见 `_drop_reasoning`）。
    """
    return {"reasoning_effort": level}


def _is_retryable(exc: Exception) -> bool:
    status = getattr(exc, "status_code", None)
    if status is None:
        response = getattr(exc, "response", None)
        status = getattr(response, "status_code", None)
    if status is not None:
        return int(status) in _RETRYABLE_STATUS
    # 连接类异常里只有「握手成功、跑到一半掉线」值得重试。
    # 连接根本没建起来（DNS 失败、端口不通、代理要密码）时重试毫无意义，只会白等 3+6+12s，
    # 把一次配置错误拖成半分钟的「卡住」。这类直接抛，让调用方看到真因。
    if exc.__class__.__name__ in {"APITimeoutError", "TimeoutException", "ReadTimeout"}:
        return True
    if exc.__class__.__name__ in {"APIConnectionError", "ConnectError"}:
        message = str(exc).lower()
        return not any(marker in message for marker in _NO_RETRY_MARKERS)
    return False


_NO_RETRY_MARKERS = (
    "connect",  # ConnectError / Connection refused / [Errno 111]
    "getaddrinfo",
    "name or service not known",
    "nodename nor servname",
    "connection refused",
    "proxy",
    "certificate",
    "ssl",
)


def _rejects_reasoning(exc: Exception) -> bool:
    """端点是否在抗议 `reasoning_effort` 这个字段（方言差异，不是我们的错）。"""
    message = str(exc).lower()
    return "reasoning" in message and any(
        marker in message for marker in ("unsupported", "not supported", "unknown", "unexpected", "invalid", "unrecognized")
    )


def _create_with_retry(client, **kwargs):
    """包一层指数退避重试；非可重试异常原样抛出，最终失败也原样抛出（由上层记日志）。

    `reasoning_effort` 做**能力探测**：不同中转/方言对这个字段的支持不一致，
    端点明确抗议时剥掉它重试一次（退回模型默认思考档），而不是让整份纪要挂掉——
    提速是锦上添花，不能因为省 3 分钟而把一次本该成功的生成打成失败。

    调用方可用 `_max_attempts` 参数收紧重试次数：会中快检这类**秒级**路径不能跟着整份纪要的退避链走——
    实测上游 503 时 0.5/1/2/4/8/16s 的退避能把一次快检拖到 120s+，远超调用方等待上限。
    """
    max_attempts = kwargs.pop("_max_attempts", None) or _RETRY_ATTEMPTS
    max_attempts = max(1, int(max_attempts))
    last: Exception | None = None
    for attempt in range(max_attempts):
        try:
            return client.chat.completions.create(**kwargs)
        except Exception as exc:  # noqa: BLE001 - 分类在下面判定
            last = exc
            if "reasoning_effort" in kwargs and _rejects_reasoning(exc):
                logger.info("端点不接受 reasoning_effort（%s），退回默认思考档重试", type(exc).__name__)
                kwargs = {key: value for key, value in kwargs.items() if key != "reasoning_effort"}
                continue
            if not _is_retryable(exc) or attempt >= max_attempts - 1:
                raise
            delay = _RETRY_BASE_SECONDS * (2**attempt)
            logger.warning("LLM 调用失败（可重试）：%s，%.1fs 后重试（%d/%d）",
                           type(exc).__name__, delay, attempt + 1, max_attempts - 1)
            time.sleep(delay)
    assert last is not None
    raise last



def _normalize_keywords(raw) -> list[str]:
    """关键词归一（去重、截断、上限 10）——在线与离线两条路径共用，避免口径分叉。"""
    if not isinstance(raw, list):
        return []
    keywords: list[str] = []
    for item in raw:
        word = " ".join(str(item or "").split())[:12]
        if word and word not in keywords:
            keywords.append(word)
    return keywords[:10]


class MeetingSummarizer:
    def __init__(self, base_url: str, api_key: str, model: str, trust_env: bool = True,
                 thinking: str | None = None) -> None:
        self._base_url = base_url
        self._api_key = api_key
        self._model = model
        self._trust_env = trust_env
        # "auto"（默认）= 按转写长短自动选档；给了具体档位就是全局固定，不再自适应
        if thinking == "auto" or thinking in _THINKING_LEVELS:
            self._thinking = thinking
        else:
            self._thinking = _thinking_default()

    @property
    def thinking(self) -> str:
        """当前思考档位：具体档位（none/low/medium/high）或 `auto`（按内容自适应）。"""
        return self._thinking

    def thinking_for(self, material: str) -> str:
        """这一次调用实际用的档位（auto 时按 material 长度算）。"""
        return _thinking_for(material, self._thinking)

    def _level(self, material: str) -> dict:
        level = self.thinking_for(material)
        logger.info("思考档位 %s（内容 %d 字）", level, len(material or ""))
        return _reasoning_param(level)

    def _client(self):
        from openai import OpenAI

        kwargs: dict = {}
        if not self._trust_env:
            # 直连（不走系统代理）：内网/中转端点走代理常 TLS 握手超时
            import httpx

            kwargs["http_client"] = httpx.Client(trust_env=False)
        return OpenAI(base_url=self._base_url, api_key=self._api_key, **kwargs)

    @property
    def configured(self) -> bool:
        return bool(self._base_url and self._api_key and self._model)

    def ask_json(self, system: str, user: str, max_tokens: int = 8192, max_attempts: int | None = None) -> dict | None:
        """通用 LLM JSON 询问器（供导图 harness 等多阶段管线复用）。失败返回 None。

        `max_tokens` 可调：推理型模型（如 deepseek-v4-flash）有思考 token 与输出 token 共享
        同一个上限，要求产出特别长时（议题 × 分节 × 分组）必须放宽，否则会得到
        `finish_reason=length` 且 `content` 为空——**看起来像限流，实际是预算耗尽**。

        `max_attempts` 可调：会中快检这类秒级路径只给 1~2 次，别让退避链把一次快检拖成两分钟。
        """
        if not self.configured:
            return None
        try:
            client = self._client()
            response = _create_with_retry(
                client,
                model=self._model,
                messages=[
                    {"role": "system", "content": system},
                    {"role": "user", "content": user},
                ],
                temperature=0.2,
                max_tokens=max_tokens,
                _max_attempts=max_attempts,
                **self._level(user),
            )
            raw = (response.choices[0].message.content or "").strip()
            finish = getattr(response.choices[0], "finish_reason", None)
            if finish == "length":
                # 空内容 + length 是最容易误判成「限流」的失败：把真因写清楚
                logger.warning(
                    "LLM 输出被截断（finish_reason=length，内容 %d 字）：输出预算不够，请调大 max_tokens",
                    len(raw),
                )
            elif not raw:
                logger.warning("LLM 返回空内容（finish_reason=%s）", finish)
        except Exception:  # noqa: BLE001
            logger.warning("LLM 调用失败", exc_info=True)
            return None
        if raw.startswith("```"):
            raw = re.sub(r"^```[a-zA-Z]*\n?", "", raw).rstrip("`").strip()
        try:
            return json.loads(raw)
        except json.JSONDecodeError:
            return _extract_last_json(raw)

    def ask_text(self, system: str, messages: list[dict]) -> str | None:
        """多轮纯文本问答（听记 AI 问答用）。messages 含历史与最新提问。失败返回 None。"""
        if not self.configured:
            return None
        try:
            client = self._client()
            response = _create_with_retry(
                client,
                model=self._model,
                messages=[{"role": "system", "content": system}, *messages],
                temperature=0.3,
                max_tokens=4096,
                **self._level(messages[-1].get("content", "") if messages else ""),
            )
            return (response.choices[0].message.content or "").strip()
        except Exception:  # noqa: BLE001
            logger.warning("听记问答 LLM 调用失败", exc_info=True)
            return None

    def summarize(self, transcript: str, template: dict | None = None) -> dict | None:
        if not self.configured:
            return None
        try:
            return self._summarize_guarded(transcript, template)
        except Exception:  # noqa: BLE001 - 总结失败不影响转写可用
            logger.warning("会议总结生成失败", exc_info=True)
            return None

    # --- 会中要点（对标钉钉实时摘要树：随录随长，只看已上屏内容） ---

    _LIVE_POINTS_SYSTEM = (
        "你是会议实时助理。根据目前已上屏的会议片段，提炼 3-6 条本场要点，"
        '输出严格 JSON 对象 {"points": ["要点1", "要点2"]}，不输出其他字段。\n'
        "每条一句话、不超过 30 字，只依据给定内容，不预测未出现的信息，不编造。"
    )
    # 要点关心"最近在聊什么"，取尾部即可，避免长会议把整篇再灌一遍
    _LIVE_POINTS_LIMIT = 4000

    def live_points(self, material: str) -> list[str]:
        if not self.configured or not material.strip():
            return []
        parsed = self.ask_json(self._LIVE_POINTS_SYSTEM, "已上屏内容如下：\n" + material[-self._LIVE_POINTS_LIMIT :])
        if not parsed:
            return []
        points: list[str] = []
        for item in parsed.get("points") or []:
            text = " ".join(str(item or "").split())[:40]
            if text and text not in points:
                points.append(text)
        return points[:6]

    # --- 会中快检（对标钉钉「实时问答/事实核对」：只针对刚说完的一小段话） ---

    _LIVE_CHECK_SYSTEM = (
        "你是会议现场的事实核对助理。用户会给一段**刚说出口的会议片段**（可能因为语音识别而断句不全），"
        "请核对其中值得当众提醒的事实性问题，输出严格 JSON 对象：\n"
        '{"verdict": "ok|attention|unclear", "note": "一句话结论", "points": ["提示1"]}\n'
        "规则：\n"
        "1. 只核对**可验证的事实**：数字/金额/日期/期限是否与片段内其他说法冲突、人名职务是否有明显出入、"
        "与常识明显矛盾的因果或定义；不做观点评价，不做建议。\n"
        "2. 没有发现问题就返回 verdict=ok、points 为空数组，不要为了填满而编造。\n"
        '3. 片段过短（不足 8 个字）或语义不完整时返回 verdict=unclear。\n'
        "4. note 不超过 40 字，points 最多 3 条、每条不超过 30 字，只依据片段本身，不引入外部资料，不编造。"
    )

    def live_check(self, material: str) -> dict | None:
        """对刚上屏的一段话做事实快检；LLM 未配置/失败返回 None（调用方按「暂不可用」展示）。"""
        if not self.configured or len(material.strip()) < 8:
            return None
        # 只给 1 次：快检要的是「几秒内有答案」，上游挂了就该立刻说不可用，
        # 不能跟着整份纪要的退避链一起等（实测能把 1 次快检拖到两分钟以上）。
        parsed = self.ask_json(
            self._LIVE_CHECK_SYSTEM, "刚上屏的片段如下：\n" + material[-1200:], max_tokens=800, max_attempts=1
        )
        if not isinstance(parsed, dict):
            return None
        verdict = str(parsed.get("verdict") or "").strip().lower()
        if verdict not in ("ok", "attention", "unclear"):
            verdict = "unclear"
        points: list[str] = []
        for item in parsed.get("points") or []:
            text = " ".join(str(item or "").split())[:40]
            if text and text not in points:
                points.append(text)
        return {
            "verdict": verdict,
            "note": " ".join(str(parsed.get("note") or "").split())[:80],
            "points": points[:3],
        }

    # --- 关键词（对标钉钉 queryKeywords：独立一次调用，可单独补跑，与总结互不牵连） ---

    _KEYWORDS_SYSTEM = (
        "你是企业会议分析助手。从带说话人标签的会议转写中提取 6-10 个关键词，"
        '输出严格 JSON 对象 {"keywords": ["词1", "词2"]}，不输出其他字段。\n'
        "要求：关键词是名词或短短语（项目名/产品名/人名/技术名词/数字指标/期限），"
        "每个不超过 10 个字，按重要性从高到低排序，只依据转写内容，不编造。"
    )
    # 关键词只要覆盖面不要精确性：超长会议截前段，避免整篇再跑一次 map-reduce
    _KEYWORDS_LIMIT = 20000

    def extract_keywords(self, transcript: str) -> list[str] | None:
        """返回关键词列表；LLM 未配置/调用失败返回 None（调用方据此置 failed）。"""
        if not self.configured or not transcript.strip():
            return None
        parsed = self.ask_json(self._KEYWORDS_SYSTEM, "会议转写如下：\n" + transcript[: self._KEYWORDS_LIMIT])
        if parsed is None:
            return None
        return _normalize_keywords(parsed.get("keywords"))

    # --- 报告议题（对标钉钉听记的章节报告正文：议题 → 分节 → 要点）---
    #
    # 为什么单独一次调用而不是塞进总结：总结已经背着纪要约 400 字 + 要点 + 待办 + 章节 +
    # 洞察 + 核心发言 + 色卡，再塞一层「议题 × 分节 × 分组」很容易被 8192 token 截断，
    # 而一旦截断整份纪要就废了。单独一次调用失败只丢报告正文，纪要照常产出。

    # 篇幅口径来自钉钉听记**实测**：一份 82 分钟会议（1640×36978 截图）切 10 个议题，
    # 单个议题 1900-2700px（约一屏半到两屏），内含 2-4 个分节、每分节 2-4 组、每 2-3 条整句要点。
    # 按 1080 宽折算，一个完整议题约 2500px——低于这个量说明只在复述，不是在整理。
    _TOPICS_SYSTEM = (
        "你是企业会议纪要编辑。把带说话人标签的会议转写整理成一份**按议题组织的结构化报告**，"
        "输出严格 JSON 对象，形状为：\n"
        '{"topics": [{"title": "议题名", "start": "mm:ss", "end": "mm:ss", '
        '"gist": "一句话摘要", "focus": ["本段讨论聚焦于…"], '
        '"sections": [{"title": "分节名", "groups": [{"title": "看法或子标题", "items": ["要点"]}]}], '
        '"insights": ["针对本议题的判断"]}]}\n'
        "口径：\n"
        "1. topics 按时间先后排列，**切到 4-8 个议题**（一场会聊了几件事就切几个，不要合并成两三块，"
        "也不要拆到一句话一个）；title 6-14 字，动词或名词短语；\n"
        "2. start/end 直接抄转写行里的 [mm:ss]，start 为该议题第一句的时间，end 为下一个议题的第一句"
        "或最后一句；不重叠、不留空档；\n"
        "3. gist 一句话说明这个议题谈出了什么（30 字内），写结论不写过程；\n"
        "4. focus 2-3 条，每条 50-110 字，依次说明「讨论聚焦什么 / 核心矛盾或要求是什么 / "
        "结论或动作是什么」；**需要强调的关键词**用 <span class=\"warm\">词</span> 包起来，每条最多标 2 个；\n"
        "5. **篇幅按议题时长给足**：\n"
        "   - 议题时长 ≥ 10 分钟：sections 2-4 个，每个 section 下 groups 2-4 个；\n"
        "   - 议题时长 3-10 分钟：sections 1-2 个，每个 section 下 groups 2-3 个；\n"
        "   - 议题时长 < 3 分钟：sections 1 个、groups 1-2 个即可，**别为了凑数编**；\n"
        "   - 全篇至少有一半议题达到 2 个以上分节——只给一个分节说明还没整理透；\n"
        "6. group.title 6-20 字，写一个**判断或做法**（如「SSL 证书无法证明具体算法」「推荐通过抓包或前端文件证明」），"
        "不要写「讨论情况」「其他」这类空标题；\n"
        "7. items 每组 2-3 条，每条 30-90 字的**完整句子**——谁、做了什么、什么后果、依据是什么，"
        "不要写光秃秃的名词短语；句中的具体数字、期限、系统名、文件名用反引号包起来"
        "（如 `RSA 2048`、`6 个月`、`1-1.5 小时`），数字必须来自转写；\n"
        "8. insights 可选 0-2 条，只写从转写推得出来但没被明说的判断或风险；\n"
        "9. 只依据转写内容，**不编造**；转写里没有细节支撑的论点宁可不写，也不要拿套话充数。"
    )
    # 议题结构比整份纪要更吃 token（每议题 2-4 分节 × 2-4 组），长会议先分段小结再汇总
    _TOPICS_LIMIT = 30000
    # 输出预算：实测 deepseek-v4-flash 单次思考约 7000-19000 token，8192 会被思考吃光、内容为空；
    # 32768 下稳定产出（实测 completion 24410，其中 reasoning 18803）。
    # 可用环境变量覆盖，端点不支持时自动回退 8192。
    _TOPICS_MAX_TOKENS = _TOPICS_MAX_TOKENS_DEFAULT

    # 精简档（`--density concise` / auto 判到吞吐低时启用）：**只收议题正文**，summary 纪要不动。
    # 与上面那份的差别全在「条数与句长」，重点口径一字不少——结论、数字、期限、责任人、
    # `warm` 高亮、反引号数字、空标题禁令、不编造，全部保留；砍掉的是过程叙述与发言复述。
    # 为什么这一刀有效：宿主的墙钟几乎全花在输出上（实测一次 59 分钟会的 summary 腿
    # 9604 字节 / 13.5 分钟 ≈ 12 字节/秒），正文从单议题 800-1400 字收到 200-400 字，
    # 就是同一条腿的输出时间按同比例缩短；档位由 `engine.resolve_density` 按实测吞吐判。
    _TOPICS_SYSTEM_LEAN = (
        "你是企业会议纪要编辑。把带说话人标签的会议转写整理成一份**按议题组织的精简报告**："
        "条目少、但每条都是重点。输出严格 JSON 对象，形状为：\n"
        '{"topics": [{"title": "议题名", "start": "mm:ss", "end": "mm:ss", '
        '"gist": "一句话摘要", "focus": ["本段讨论聚焦于…"], '
        '"sections": [{"title": "分节名", "groups": [{"title": "看法或子标题", "items": ["要点"]}]}], '
        '"insights": ["针对本议题的判断"]}]}\n'
        "口径：\n"
        "1. topics 按时间先后排列，切到 4-8 个议题（一场会聊了几件事就切几个）；title 6-14 字；\n"
        "2. start/end 直接抄转写行里的 [mm:ss]，不重叠、不留空档；\n"
        "3. gist 一句话说明这个议题谈出了什么（30 字内），写结论不写过程；\n"
        "4. focus **只 1 条**，≤60 字，一句话说清「聚焦什么 + 结论是什么」；"
        "需要强调的关键词用 <span class=\"warm\">词</span> 包起来，最多标 2 个；\n"
        "5. **篇幅收紧**：sections 1-2 个（议题 <5 分钟只给 1 个），每个 section 下 groups 1-2 个，"
        "每组 items 1-2 条；group.title 6-20 字，写一个**判断或做法**"
        "（如「SSL 证书无法证明具体算法」），不要写「讨论情况」「其他」这类空标题；\n"
        "6. items 每条 **≤45 字**，且必须是**结论 / 数字 / 期限 / 责任人 / 依据**之一；"
        "过程叙述、谁说了什么、「大家讨论了…」这类一律不写；"
        "具体数字、期限、系统名、文件名用反引号包起来（如 `RSA 2048`、`6 个月`），数字必须来自转写；\n"
        "7. insights 0-1 条，≤40 字，只写从转写推得出来但没被明说的判断或风险；没有就不写；\n"
        "8. **宁少不凑**：单个议题正文 200-400 字就够，凑数的分节比缺一节更糟；"
        "只依据转写内容，**不编造**，没有细节支撑的论点不写。"
    )

    def topics_request(self, transcript: str, *, dense: bool = True) -> dict:
        """组装报告议题请求（可选步骤；失败只影响长图正文，不影响纪要）。

        `dense=False` 走精简档：条数与句长收紧，重点口径不变。
        """
        material = transcript if len(transcript) <= self._TOPICS_LIMIT else transcript[: self._TOPICS_LIMIT]
        return {
            "system": self._TOPICS_SYSTEM if dense else self._TOPICS_SYSTEM_LEAN,
            "user": "会议转写如下：\n" + material,
        }

    def topics_from_answer(self, answer: str) -> list[dict] | None:
        """解析议题回答（原始文本，含 ``` 围栏也能吃）。不可解析返回 None。"""
        parsed = _parse_json_object(answer)
        if not parsed:
            return None
        return _parse_topics(parsed.get("topics"))

    # --- 两阶段议题（长会用）：先切大纲，再按议题并行扩写 ---
    #
    # 单次 topics 调用的耗时随会议时长线性上涨（82 分钟约 4-5 分钟），而且输出越长越容易
    # 被 token 上限截断。拆成「大纲（短输出）+ 每议题只看自己的时间窗（短输入短输出）」后，
    # 支持并发的宿主可以把扩写全部并行，墙钟时间从「一场会一次长调用」降到
    # 「大纲 + 最慢的一个议题」。代价是多一个来回，所以默认关闭（`--split-topics`）。

    _TOPICS_OUTLINE_SYSTEM = (
        "你是企业会议纪要编辑。这次只做议题切分，**不写正文**。输出严格 JSON 对象，形状为：\n"
        '{"topics": [{"title": "议题名", "start": "mm:ss", "end": "mm:ss", "gist": "一句话摘要"}]}\n'
        "口径：\n"
        "1. 按时间先后切到 4-8 个议题（一场会聊了几件事就切几个，不要合并成两三块，"
        "也不要拆到一句话一个）；title 6-14 字，动词或名词短语；\n"
        "2. start/end 直接抄转写行里的 [mm:ss]，start 为该议题第一句的时间，"
        "end 为下一个议题的第一句或最后一句；不重叠、不留空档；\n"
        "3. gist 30 字内说明这个议题谈出了什么，写结论不写过程；\n"
        "4. 只输出 topics 清单，**不要** focus / sections / insights（正文下一步再写）。"
    )

    _TOPIC_EXPAND_SYSTEM = (
        "你是企业会议纪要编辑。下面是会议中**某一个议题**的转写片段（前后可能带少量上下文），"
        "请只为这个议题写报告正文。输出严格 JSON 对象，形状为：\n"
        '{"focus": ["本段讨论聚焦于…"], '
        '"sections": [{"title": "分节名", "groups": [{"title": "看法或子标题", "items": ["要点"]}]}], '
        '"insights": ["针对本议题的判断"]}\n'
        "口径：\n"
        "1. focus 2-3 条，每条 50-110 字，依次说明「讨论聚焦什么 / 核心矛盾或要求是什么 / "
        "结论或动作是什么」；**需要强调的关键词**用 <span class=\"warm\">词</span> 包起来，每条最多标 2 个；\n"
        "2. **篇幅按议题时长给足**：\n"
        "   - 议题时长 ≥ 10 分钟：sections 2-4 个，每个 section 下 groups 2-4 个；\n"
        "   - 议题时长 3-10 分钟：sections 1-2 个，每个 section 下 groups 2-3 个；\n"
        "   - 议题时长 < 3 分钟：sections 1 个、groups 1-2 个即可，**别为了凑数编**；\n"
        "3. group.title 6-20 字，写一个**判断或做法**（如「SSL 证书无法证明具体算法」），"
        "不要写「讨论情况」「其他」这类空标题；\n"
        "4. items 每组 2-3 条，每条 30-90 字的**完整句子**——谁、做了什么、什么后果、依据是什么，"
        "不要写光秃秃的名词短语；句中的具体数字、期限、系统名、文件名用反引号包起来"
        "（如 `RSA 2048`、`6 个月`），数字必须来自转写；\n"
        "5. insights 可选 0-2 条，只写从转写推得出来但没被明说的判断或风险；\n"
        "6. 只依据给定片段，**不编造**；区间外内容只作上下文，不要为它生成分节。"
    )

    def topics_outline_request(self, transcript: str) -> dict:
        """第一阶段：只切议题大纲（输出很短，通常几十秒）。

        大纲腿与密度档无关——它本来就不写正文，两条路的输出量一样。
        """
        material = transcript if len(transcript) <= self._TOPICS_LIMIT else transcript[: self._TOPICS_LIMIT]
        return {"system": self._TOPICS_OUTLINE_SYSTEM, "user": "会议转写如下：\n" + material}

    # 精简档的逐议题扩写（配合 `_TOPICS_SYSTEM_LEAN` 同一口径）。
    # 与 `_TOPIC_EXPAND_SYSTEM` 共享「warm 高亮 / 反引号数字 / 不编造 / 区间外不生成分节」四条，
    # 只把分节、分组、条数、句长收下来——`test_both_expand_prompts_keep_the_shared_invariants` 钉住。
    _TOPIC_EXPAND_SYSTEM_LEAN = (
        "你是企业会议纪要编辑。下面是会议中**某一个议题**的转写片段（前后可能带少量上下文），"
        "请只为这个议题写**精简版**报告正文：条目少、但每条都是重点。输出严格 JSON 对象，形状为：\n"
        '{"focus": ["本段讨论聚焦于…"], '
        '"sections": [{"title": "分节名", "groups": [{"title": "看法或子标题", "items": ["要点"]}]}], '
        '"insights": ["针对本议题的判断"]}\n'
        "口径：\n"
        "1. focus **只 1 条**，≤60 字，一句话说清「聚焦什么 + 结论是什么」；"
        "**需要强调的关键词**用 <span class=\"warm\">词</span> 包起来，最多标 2 个；\n"
        "2. **篇幅收紧**：sections 1-2 个（议题 <5 分钟只给 1 个），每个 section 下 groups 1-2 个；\n"
        "3. group.title 6-20 字，写一个**判断或做法**，不要写「讨论情况」「其他」这类空标题；\n"
        "4. items 每组 **1-2 条**、每条 **≤45 字**，且必须是**结论 / 数字 / 期限 / 责任人 / 依据**之一；"
        "过程叙述、谁说了什么一律不写；句中的具体数字、期限、系统名、文件名用反引号包起来"
        "（如 `RSA 2048`、`6 个月`），数字必须来自转写；\n"
        "5. insights 0-1 条，≤40 字，只写从转写推得出来但没被明说的判断或风险；\n"
        "6. **宁少不凑**：这个议题的正文 200-400 字就够，凑数的分节比缺一节更糟；\n"
        "7. 只依据给定片段，**不编造**；区间外内容只作上下文，不要为它生成分节。"
    )

    def topic_expand_request(self, topic: dict, transcript: str, index: int, total: int,
                             *, dense: bool = True) -> dict:
        """第二阶段：只扩写一个议题；输入是切片后的转写，输出只有正文三件套。

        `dense=False` 用精简档口径——同样的重点要求，条数与句长收下来。
        """
        start = int(topic.get("start") or 0)
        end = int(topic.get("end") or start)
        user = (
            f"议题 {index}/{total}：{topic.get('title') or ''}"
            f"（{_format_mmss(start)}–{_format_mmss(end)}）\n"
            f"议题摘要：{topic.get('gist') or '（无）'}\n"
            "转写片段如下：\n" + transcript
        )
        return {
            "system": self._TOPIC_EXPAND_SYSTEM if dense else self._TOPIC_EXPAND_SYSTEM_LEAN,
            "user": user,
        }

    def topic_body_from_answer(self, answer: str, *, title: str = "", start: int = 0,
                               end: int = 0) -> dict | None:
        """解析单个议题的扩写回答；缺 title/start/end 时用大纲里的值补上。

        模型很容易只回正文（`{"sections": ...}`），而 `_parse_topics` 要求 title 才收，
        所以这里先补字段再归一，避免「答得挺好但整段被丢」。
        """
        parsed = _parse_json_object(answer)
        if not parsed:
            return None
        item = parsed.get("topic") if isinstance(parsed.get("topic"), dict) else parsed
        if isinstance(parsed.get("topics"), list) and parsed["topics"]:
            item = parsed["topics"][0]
        if not isinstance(item, dict):
            return None
        item = dict(item)
        item["title"] = item.get("title") or title
        item["start"] = item.get("start") if item.get("start") is not None else start
        item["end"] = item.get("end") if item.get("end") is not None else end
        normalized = _parse_topics([item])
        return normalized[0] if normalized else None

    def extract_topics(self, transcript: str) -> list[dict] | None:
        """在线路径：独立一次调用产出议题结构；未配置/失败返回 None（调用方回退旧渲染）。"""
        if not self.configured or not transcript.strip():
            return None
        request = self.topics_request(transcript)
        started = time.time()
        parsed = self.ask_json(request["system"], request["user"], max_tokens=self._TOPICS_MAX_TOKENS)
        logger.info("议题请求完成：%.1fs", time.time() - started)
        if not parsed and self._TOPICS_MAX_TOKENS > 8192:
            # 有些端点（含部分中转）不接受超大 max_tokens，直接 400；退回保守值再试一次。
            # 注意这一次是**整段重跑**：实测议题调用单次 4-5 分钟，误判一次就白等这么久，
            # 所以只在「确定是预算/参数问题」时才值得——日志里记清楚，别让它悄悄叠时延。
            logger.warning(
                "议题请求失败（max_tokens=%d），回退 8192 重试；这次重试会再花一遍完整调用时间",
                self._TOPICS_MAX_TOKENS,
            )
            parsed = self.ask_json(request["system"], request["user"], max_tokens=8192)
        if not parsed:
            return None
        return _parse_topics(parsed.get("topics"))

    # ── 无 LLM 形态：把「要问什么」组装出来，交给调用方（通常是 Agent）自己回答 ──
    #
    # 这是 skill 化的关键：Agent 本来就有模型能力，不该再要一把 key 或一套端点配置。
    # 本模块只负责「组装请求」与「解析回答」两端，中间那次模型调用由上层完成。

    def build_request(self, transcript: str, template: dict | None = None,
                      with_keywords: bool = False, derive_from_topics: bool = False) -> dict:
        """组装总结请求 {"system", "user"}，可直接喂给任何 OpenAI 兼容的 chat 接口。

        `with_keywords=True` 时提示词里多要一个 `keywords` 字段：关键词与纪要走同一次
        调用，Agent 少发一次请求、少发一遍转写。`finish` 从 summary 回答里取用。

        `derive_from_topics=True` 时不再要 `chapters` / `mindmap`：`finish` 会用
        `answers.topics` 派生，保持产物字段齐全但少写约三分之一的 output token。
        """
        return {
            "system": _system_prompt(template, with_keywords, derive_from_topics=derive_from_topics),
            "user": "会议转写如下：\n" + transcript,
        }

    def summarize_from_answer(self, answer: str, template: dict | None = None) -> dict | None:
        """把模型返回的**原始文本**解析成总结结构（含归一兜底）。空/不可解析返回 None。"""
        if not (answer or "").strip():
            return None
        return self._parse(answer, template)

    def keywords_request(self, transcript: str) -> dict:
        """组装关键词请求（可选步骤）。"""
        return {"system": self._KEYWORDS_SYSTEM, "user": "会议转写如下：\n" + transcript[: self._KEYWORDS_LIMIT]}

    def keywords_from_answer(self, answer: str) -> list[str] | None:
        """解析关键词回答（原始文本，含 ``` 围栏也能吃）。"""
        parsed = _parse_json_object(answer)
        if not parsed:
            return None
        return _normalize_keywords(parsed.get("keywords"))

    def _summarize_guarded(self, transcript: str, template: dict | None = None) -> dict | None:
        client = self._client()
        if len(transcript) > _REDUCE_THRESHOLD:
            partials: list[str] = []
            for start in range(0, len(transcript), _REDUCE_CHUNK):
                piece = transcript[start : start + _REDUCE_CHUNK]
                partials.append(self._ask(client, "以下是长会议转写的一段，输出该段的要点小结（纯文本，300字内）：\n" + piece))
            material = "各段小结：\n" + "\n".join(partials)
        else:
            material = transcript
        raw = self._ask(client, material, template)
        return self._parse(raw, template)

    def _ask(self, client, material: str, template: dict | None = None) -> str:
        # 空壳重试：实测出现过「模型返回了一个合法 JSON 对象，但 minutes / key_points / chapters
        # 全为空」——解析层不会报错，产物里就是一份 0 字的纪要，而且看起来像「生成成功」。
        # 这种事在小样本上抓不到，必须重试一次；仍为空就把原因带出来，别再静默出空文档。
        for attempt in range(2):
            response = _create_with_retry(
                client,
                model=self._model,
                messages=[
                    {"role": "system", "content": _system_prompt(template)},
                    {"role": "user", "content": "会议转写如下：\n" + material},
                ],
                temperature=0.2,
                # 推理型模型答案前大量思考，默认补全上限会被中途截断。
                # 实测（deepseek-v4-flash）：summary 的回答本身就 >8192，8192 会 `finish_reason=length`、
                # 内容为空，整次调用（约 90s）作废；放宽到 32768 才稳定产出。
                max_tokens=_SUMMARY_MAX_TOKENS,
                **self._level(material),
            )
            raw = response.choices[0].message.content or ""
            if not _is_hollow_minutes(raw):
                return raw
            logger.warning(
                "纪要回答是空壳（%d 字但无 minutes/key_points/chapters，finish_reason=%s），重试一次",
                len(raw), getattr(response.choices[0], "finish_reason", None),
            )
        return raw

    @staticmethod
    def _parse(raw: str, template: dict | None = None) -> dict | None:
        text = raw.strip()
        # 防御性解析：剥掉可能的 markdown 代码栅栏；解析失败回退把全文当纪要
        if text.startswith("```"):
            text = re.sub(r"^```[a-zA-Z]*\n?", "", text).rstrip("`").strip()
        try:
            parsed = json.loads(text)
        except json.JSONDecodeError:
            parsed = _extract_last_json(raw)
            if parsed is None:
                return {"minutes": raw.strip(), "key_points": [], "todos": []} if raw.strip() else None
        # 模型可能返回 JSON 数组/字符串而不是对象（本步约定的是对象）。
        # 不在这里挡住的话，下面的 parsed.get 会抛 AttributeError，变成一句和真正原因
        # 无关的堆栈——两步走模式下这里是「Agent 转交的模型输出」，必须给出可读原因。
        if not isinstance(parsed, dict):
            return None
        todos = [
            {
                "task": str(item.get("task") or ""),
                "owner_hint": str(item.get("owner_hint") or ""),
                "due_hint": str(item.get("due_hint") or ""),
            }
            for item in parsed.get("todos") or []
            if isinstance(item, dict) and item.get("task")
        ]
        result: dict = {"todos": todos, "mindmap": str(parsed.get("mindmap") or "")}
        chapters = _parse_chapters(parsed.get("chapters"))
        if chapters:
            result["chapters"] = chapters
        sections = parsed.get("sections")
        is_default = not template or template.get("id") == "meeting-minutes"
        if isinstance(sections, list) and sections:
            # 模板 sections 结构（title + 段落或列表）
            result["sections"] = [
                {
                    "title": str(section.get("title") or "").strip(),
                    "text": str(section.get("content") or "").strip(),
                }
                for section in sections
                if isinstance(section, dict) and str(section.get("title") or "").strip()
            ]
        elif not is_default and (parsed.get("minutes") or parsed.get("key_points")):
            # 弱模型不守 sections 形状时兜底：把旧形状转换成 sections，保证模板结构可见
            sections_backfill = []
            if parsed.get("minutes"):
                sections_backfill.append({"title": "总结", "text": str(parsed.get("minutes"))})
            if parsed.get("key_points"):
                sections_backfill.append({
                    "title": "要点",
                    "content": chr(10).join("- " + str(point) for point in parsed["key_points"]),
                })
            result["sections"] = sections_backfill
        if is_default:
            # 默认模板：兼容旧固定字段（纪要/要点）
            result["minutes"] = str(parsed.get("minutes") or "")
            result["key_points"] = [str(item) for item in parsed.get("key_points") or []]
        # 汇报卡字段（老记录没有这三项，导出侧按旧字段兜底）
        visual = _parse_visual(parsed.get("visual"))
        if visual:
            result["visual"] = visual
        insights = _parse_insights(parsed.get("insights"))
        if insights:
            result["insights"] = insights
        quote_kp = _parse_quote_kp(parsed.get("quote_kp"))
        if quote_kp:
            result["quote_kp"] = quote_kp
        # 关键词可能在同一次 summary 回答里（prepare 默认合并），单独调用时则为空
        keywords = _normalize_keywords(parsed.get("keywords"))
        if keywords:
            result["keywords"] = keywords
        return result
