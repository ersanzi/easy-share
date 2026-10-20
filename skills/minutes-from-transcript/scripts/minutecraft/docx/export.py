"""听记 Word 导出：对标钉钉 AI 听记「一键导出」的纪要文档版式（2026-10-18）。

版式取自用户提供的两份钉钉原生导出件（`纪要_09-09 数转诊断与智能体平台集成方案.pdf`、
`纪要_06-25 工业AI中台与融合产品构建.pdf`）实测：

  第 1 页 封面：24pt「纪要」 + 12pt「主题/时间/参与人」 + 18pt 标题 + 灰色一句话主题
                + 可视化报告（01-06 带序号彩色模块、卡片、标签、小结条、页脚水印）
  第 2 页+ 文档体：24pt「纪要」 + 12pt 元信息 + 18pt 青绿章节标题 + 两级项目符号列表
                + AI洞察（小标题 + 三点） + 待办（方框复选框） + 章节（时间戳 + 概述）
                + 核心发言提炼（观点 + 灰斜体原文引用）
                + 全局行动项汇总（三列表格：行动项/负责人/时间节点）+ 汇报卡

为什么要另起一个模块：原 `export_docx` 只输出「标题 + 纪要段落 + 项目符号」，钉钉那份的
观感差异主要在**版式结构**（封面可视化卡、文档体层级、洞察/待办/章节分区），不在字体。
颜色靠 run 级 hex 直接写，不改 Normal 样式，避免污染调用方文档。
"""
from __future__ import annotations

import io
from datetime import datetime

from docx import Document
from docx.enum.table import WD_TABLE_ALIGNMENT
from docx.enum.text import WD_ALIGN_PARAGRAPH
from docx.oxml import OxmlElement
from docx.oxml.ns import qn
from docx.shared import Cm, Pt, RGBColor

from .visual import ACCENT_ORDER, visual_sections

# 版式基准（A4、2.5cm 左右边距≈钉钉原件的 36pt）
_FONT = "微软雅黑"
_BODY = RGBColor(0x1F, 0x23, 0x29)
_MUTED = RGBColor(0x7C, 0x82, 0x8E)
_RULE = "E6E8EC"
_WATERMARK = RGBColor(0xC9, 0xCD, 0xD4)
# 章节标题的钉钉色：偏暗青绿
_HEADING = RGBColor(0x1A, 0x70, 0x76)

# 钉钉报告六色带（01/05 同玫红、02 蓝、03 青、04 琥珀、06 绿）
ACCENTS: dict[str, dict[str, str]] = {
    "rose": {"line": "E8557A", "bg": "FDF1F4", "band": "F9D5DE"},
    "blue": {"line": "3B78D8", "bg": "EFF4FC", "band": "D5E3F7"},
    "teal": {"line": "1E8E88", "bg": "E8F5F4", "band": "CFEAE7"},
    "amber": {"line": "C98A1E", "bg": "FDF6E8", "band": "F5E3BE"},
    "green": {"line": "1E8E62", "bg": "EAF6F0", "band": "CDEBDC"},
}
CN_NUM = "一二三四五六七八九十"


def _rgb(hex_color: str) -> RGBColor:
    return RGBColor.from_string(hex_color.upper())


def _set_run(run, *, size=10.5, bold=False, color=_BODY, italic=False, font=_FONT) -> None:
    """统一中西文字体（python-docx 只设 ascii 时中文字符仍走主题字体，必须补 eastAsia）。"""
    run.font.size = Pt(size)
    run.font.bold = bold
    run.font.italic = italic
    run.font.color.rgb = color
    run.font.name = font
    run._element.rPr.rFonts.set(qn("w:eastAsia"), font)


def _spacing(paragraph, *, before=0, after=0, line=None) -> None:
    fmt = paragraph.paragraph_format
    fmt.space_before = Pt(before)
    fmt.space_after = Pt(after)
    if line:
        fmt.line_spacing = line


def _para(container, text="", *, size=10.5, bold=False, color=_BODY, italic=False,
          before=0, after=6, line=1.5, align=None):
    paragraph = container.add_paragraph()
    _spacing(paragraph, before=before, after=after, line=line)
    if align is not None:
        paragraph.alignment = align
    if text:
        _set_run(paragraph.add_run(text), size=size, bold=bold, color=color, italic=italic)
    return paragraph


def _shade(cell, hex_color: str) -> None:
    shading = OxmlElement("w:shd")
    shading.set(qn("w:val"), "clear")
    shading.set(qn("w:color"), "auto")
    shading.set(qn("w:fill"), hex_color)
    cell._tc.get_or_add_tcPr().append(shading)


def _cell_borders(cell, *, color="FFFFFF", size=8, edges=("top", "bottom", "left", "right")) -> None:
    props = cell._tc.get_or_add_tcPr()
    borders = props.find(qn("w:tcBorders"))
    if borders is None:
        borders = OxmlElement("w:tcBorders")
        props.append(borders)
    for edge in edges:
        element = OxmlElement(f"w:{edge}")
        element.set(qn("w:val"), "single")
        element.set(qn("w:sz"), str(size))
        element.set(qn("w:space"), "0")
        element.set(qn("w:color"), color)
        borders.append(element)


def _table_borders(table, *, color=_RULE, size=4) -> None:
    props = table._tbl.tblPr
    borders = OxmlElement("w:tblBorders")
    for edge in ("top", "left", "bottom", "right", "insideH", "insideV"):
        element = OxmlElement(f"w:{edge}")
        element.set(qn("w:val"), "single")
        element.set(qn("w:sz"), str(size))
        element.set(qn("w:space"), "0")
        element.set(qn("w:color"), color)
        borders.append(element)
    props.append(borders)


def _cell_margins(table, *, top=100, bottom=100, left=140, right=140) -> None:
    props = table._tbl.tblPr
    margins = OxmlElement("w:tblCellMar")
    for edge, value in (("top", top), ("left", left), ("bottom", bottom), ("right", right)):
        element = OxmlElement(f"w:{edge}")
        element.set(qn("w:w"), str(value))
        element.set(qn("w:type"), "dxa")
        margins.append(element)
    props.append(margins)


def _no_borders(table) -> None:
    props = table._tbl.tblPr
    borders = OxmlElement("w:tblBorders")
    for edge in ("top", "left", "bottom", "right", "insideH", "insideV"):
        element = OxmlElement(f"w:{edge}")
        element.set(qn("w:val"), "nil")
        borders.append(element)
    props.append(borders)


def _bullet(container, text: str, level: int = 0, *, color=_BODY, size=10.5, char="•") -> None:
    """两级项目符号：钉钉原件是「· 主干 / ○ 补充」，这里用悬挂缩进复刻同一层级感。"""
    paragraph = container.add_paragraph()
    _spacing(paragraph, before=0, after=2, line=1.45)
    indent = Cm(0.4) + Cm(0.55) * level
    fmt = paragraph.paragraph_format
    fmt.left_indent = indent
    fmt.first_line_indent = -Cm(0.45)
    _set_run(paragraph.add_run(f"{char} "), size=size, color=color)
    _set_run(paragraph.add_run(text), size=size, color=color)


def _section_heading(container, text: str, *, size=15) -> None:
    _para(container, text, size=size, bold=True, color=_HEADING, before=18, after=6, line=1.3)


def _sub_heading(container, text: str) -> None:
    _para(container, text, size=11, bold=True, before=8, after=4, line=1.4)


def _divider(container) -> None:
    paragraph = container.add_paragraph()
    _spacing(paragraph, before=2, after=10)
    props = paragraph._p.get_or_add_pPr()
    borders = OxmlElement("w:pBdr")
    bottom = OxmlElement("w:bottom")
    bottom.set(qn("w:val"), "single")
    bottom.set(qn("w:sz"), "6")
    bottom.set(qn("w:space"), "1")
    bottom.set(qn("w:color"), _RULE)
    borders.append(bottom)
    props.append(borders)


def _page_break(document) -> None:
    """分页：给一个空段落打 pageBreakBefore。

    不用 `document.add_page_break()`——它在段落里插裸 <w:br type="page"/>，
    加一个空段后容易与后续段落的间距叠出整页空白（本机 Word 导出实测出现过）。
    """
    marker = document.add_paragraph()
    _spacing(marker, before=0, after=0, line=1.0)
    marker.paragraph_format.page_break_before = True


# ─── 可视化报告（封面页的那张色卡图） ───


def _visual_sections(meta: dict, summary: dict) -> list[dict]:
    """可视化报告分节：正向取 visual，缺失时兜底折叠（实现见 visual.py，前端共用同一口径）。"""
    return visual_sections(meta, summary)


def _card_content(cell, item: dict, accent: dict) -> None:
    _shade(cell, accent["bg"])
    _cell_borders(cell, color=accent["bg"], size=6)
    # 钉钉卡片行距很紧（原文实测约 1.1 倍），空格子上下各留一点内边距
    cell.paragraphs[0].paragraph_format.space_before = Pt(3)
    # 卡头：色块 + 小标题 + 标签（closing 卡没有标题时不能留空段，否则多出一条空行）
    head = cell.paragraphs[0]
    _spacing(head, before=3, after=2 if (item.get("title") or item.get("tag")) else 0, line=1.15)
    if item.get("title") or item.get("tag"):
        _set_run(head.add_run("▮ "), size=9, color=_rgb(accent["line"]), bold=True)
        if item.get("title"):
            _set_run(head.add_run(item["title"]), size=9.5, bold=True, color=_body_color())
        if item.get("tag"):
            _set_run(head.add_run("  "), size=8)
            _set_run(head.add_run(f" {item['tag']} "), size=7.5, color=_rgb(accent["line"]), bold=True)
    for bullet in item.get("bullets") or []:
        line = cell.add_paragraph()
        _spacing(line, before=0, after=1, line=1.3)
        _set_run(line.add_run("· "), size=8.5, color=_MUTED)
        _set_run(line.add_run(bullet), size=8.5, color=_BODY)
    if item.get("note"):
        note = cell.add_paragraph()
        _spacing(note, before=3, after=3, line=1.25)
        _shade_paragraph(note, accent["band"])
        _set_run(note.add_run(item["note"]), size=8, color=_BODY)


def _body_color() -> RGBColor:
    return _BODY


def _shade_paragraph(paragraph, hex_color: str) -> None:
    props = paragraph._p.get_or_add_pPr()
    shading = OxmlElement("w:shd")
    shading.set(qn("w:val"), "clear")
    shading.set(qn("w:color"), "auto")
    shading.set(qn("w:fill"), hex_color)
    props.append(shading)


def _render_visual_report(document, meta: dict, summary: dict) -> None:
    sections = _visual_sections(meta, summary)
    if not sections:
        return
    # 模块标题「01 ▍标题」 + 该节色卡。每节一个两列无边框表格，卡片按 items 排布。
    for index, section in enumerate(sections):
        accent = ACCENTS[section.get("accent") or ACCENT_ORDER[index % len(ACCENT_ORDER)]]
        label = CN_NUM[index] if index < len(CN_NUM) else str(index + 1)
        head = document.add_paragraph()
        _spacing(head, before=6 if index else 2, after=2, line=1.15)
        _set_run(head.add_run(f"{index + 1:02d} "), size=9, bold=True, color=_rgb(accent["line"]))
        _set_run(head.add_run(" ▍"), size=9, bold=True, color=_rgb(accent["line"]))
        _set_run(head.add_run(section.get("title") or ""), size=9.5, bold=True, color=_rgb(accent["line"]))
        del label  # 钉钉用「01 02…」阿拉伯序号，CN_NUM 仅留给正文分节

        items = section.get("items") or []
        if section.get("layout") == "closing" or len(items) == 1:
            # 单条共识：整行一条色带
            table = document.add_table(rows=1, cols=1)
            table.alignment = WD_TABLE_ALIGNMENT.CENTER
            _card_content(table.rows[0].cells[0], items[0], accent)
            continue
        if len(items) == 2:
            table = document.add_table(rows=1, cols=2)
            for col, item in enumerate(items):
                _card_content(table.rows[0].cells[col], item, accent)
        else:
            rows = (len(items) + 1) // 2
            table = document.add_table(rows=rows, cols=2)
            for pos, item in enumerate(items):
                _card_content(table.rows[pos // 2].cells[pos % 2], item, accent)
            if len(items) % 2:
                empty = table.rows[-1].cells[-1]
                _shade(empty, "FFFFFF")
                _cell_borders(empty, color="FFFFFF", size=6)
        _cell_margins(table, top=20, bottom=20, left=100, right=100)


# ─── 文档体 ───


def _render_body_sections(document, meta: dict, summary: dict) -> None:
    """章节：非默认模板走 sections；默认模板走 minutes/key_points。标题按钉钉 18pt 青绿。"""
    names = meta.get("speaker_names") or {}
    if summary.get("sections"):
        for section in summary["sections"]:
            title = str(section.get("title") or "").strip()
            if title:
                _section_heading(document, title)
            for raw in str(section.get("text") or "").splitlines():
                line = raw.strip()
                if not line:
                    continue
                if line.startswith(("- ", "•", "*")):
                    _bullet(document, line.lstrip("-•* ").strip(), 1, char="○")
                else:
                    _para(document, line, line=1.6, after=4)
    else:
        if summary.get("minutes"):
            _section_heading(document, "会议背景")
            for line in str(summary["minutes"]).splitlines():
                if line.strip():
                    _para(document, line.strip(), line=1.6, after=4)
        if summary.get("key_points"):
            _section_heading(document, "关键要点")
            for point in summary["key_points"]:
                _bullet(document, str(point), 0)
    del names


def _render_insights(document, summary: dict) -> None:
    insights = summary.get("insights") or []
    if not insights:
        return
    _para(document, "AI洞察", size=9, bold=True, color=_MUTED, before=14, after=4)
    for point in insights:
        _bullet(document, str(point), 0)


def _render_quote_kp(document, summary: dict) -> None:
    kp = summary.get("quote_kp") or {}
    if not (kp.get("points") or kp.get("quotes")):
        return
    _section_heading(document, "核心发言提炼")
    speaker = kp.get("speaker") or "发言人"
    _bullet(document, f"{speaker}（KP）", 0)
    if kp.get("points"):
        _bullet(document, "核心观点与指示：", 1, char="○")
        for point in kp["points"]:
            _bullet(document, str(point), 2, char="○")
    for quote in kp.get("quotes") or []:
        paragraph = _para(document, f'“{quote}”', size=10, color=_MUTED, italic=True,
                          before=6, after=6, line=1.5)
        paragraph.paragraph_format.left_indent = Cm(1.2)


def _render_todos(document, todos: list[dict]) -> None:
    if not todos:
        return
    _section_heading(document, "待办", size=18)
    for todo in todos:
        paragraph = document.add_paragraph()
        _spacing(paragraph, before=0, after=3, line=1.5)
        paragraph.paragraph_format.left_indent = Cm(0.4)
        paragraph.paragraph_format.first_line_indent = -Cm(0.4)
        _set_run(paragraph.add_run("· "), size=10.5)
        # 钉钉用空方框（未勾选）；已完成项画实心方块
        _set_run(paragraph.add_run("☒ " if todo.get("done") else "☐ "), size=11, color=_MUTED)
        _set_run(paragraph.add_run(str(todo.get("task") or "")), size=10.5)
        suffix = [value for value in (todo.get("owner_hint"), todo.get("due_hint")) if value]
        if suffix:
            _set_run(paragraph.add_run(f"（{'，'.join(suffix)}）"), size=9.5, color=_MUTED)


def _render_chapters(document, chapters: list[dict]) -> None:
    if not chapters:
        return
    _section_heading(document, "章节", size=18)
    for chapter in chapters:
        start = int(chapter.get("start") or 0)
        stamp = f"{start // 3600:02d}:{start % 3600 // 60:02d}:{start % 60:02d}"
        _bullet(document, f"{stamp} {chapter.get('title') or ''}", 0)
        if chapter.get("gist"):
            _bullet(document, str(chapter["gist"]), 1, char="○")


def _render_action_table(document, todos: list[dict]) -> None:
    if not todos:
        return
    _section_heading(document, "全局行动项汇总")
    table = document.add_table(rows=1, cols=3)
    _table_borders(table)
    headers = ("行动项", "负责人", "时间节点")
    for col, text in enumerate(headers):
        cell = table.rows[0].cells[col]
        _shade(cell, "F5F7FA")
        paragraph = cell.paragraphs[0]
        _spacing(paragraph, before=2, after=2)
        _set_run(paragraph.add_run(text), size=10, bold=True)
    for todo in todos:
        cells = table.add_row().cells
        values = (todo.get("task") or "", todo.get("owner_hint") or "待定", todo.get("due_hint") or "待定")
        for col, value in enumerate(values):
            paragraph = cells[col].paragraphs[0]
            _spacing(paragraph, before=2, after=2, line=1.4)
            _set_run(paragraph.add_run(str(value)), size=9.5)
    widths = (Cm(9.0), Cm(3.4), Cm(3.6))
    for row in table.rows:
        for col, width in enumerate(widths):
            row.cells[col].width = width
    _cell_margins(table)


def build_minutes_docx(meta: dict, *, with_transcript: bool = False) -> bytes:
    """按钉钉版式生成纪要文档。meta 为听记记录的详情字典（store.get 的形状）。"""
    document = Document()
    section = document.sections[0]
    section.page_width = Cm(21)
    section.page_height = Cm(29.7)
    section.left_margin = section.right_margin = Cm(2.5)
    section.top_margin = section.bottom_margin = Cm(2.2)
    style = document.styles["Normal"]
    style.font.name = _FONT
    style.font.size = Pt(10.5)
    style.element.rPr.rFonts.set(qn("w:eastAsia"), _FONT)

    summary = meta.get("summary") or {}
    speaker_names = [name for name in (meta.get("speaker_names") or {}).values() if name]
    title = str(meta.get("title") or "会议纪要")
    created = str(meta.get("created_at") or "")
    try:
        created = datetime.fromisoformat(created).strftime("%Y-%m-%d %H:%M:%S")
    except ValueError:
        created = created.replace("T", " ")[:19]

    # 占位符统一用「—」而不是「待补充」：Agent 直接从转写文本起跑时本来就没有采集侧的元信息，
    # 空字段写满「待补充」会让封面显得像缺料；破折号是文档里表达「无此项」的通行写法
    created_label = created or "—"
    people_label = "、".join(dict.fromkeys(speaker_names)) or "—"

    # ---- 封面页 ----
    _para(document, "纪要", size=24, bold=True, before=0, after=18, line=1.1)
    # 元信息用悬挂缩进：折行时对齐内容起点，而不是顶回页边距（钉钉原件不折行，我们双人发言会折）
    meta_paragraph = document.add_paragraph()
    _spacing(meta_paragraph, before=0, after=22, line=1.5)
    meta_paragraph.paragraph_format.left_indent = Cm(1.5)
    meta_paragraph.paragraph_format.first_line_indent = -Cm(1.5)
    _set_run(meta_paragraph.add_run(
        f"主题: {title}    时间: {created_label}    参与人: {people_label}"
    ), size=12)
    _para(document, title, size=18, bold=True, before=0, after=4, line=1.3)
    visual_summary = str((summary.get("visual") or {}).get("summary") or "").strip()
    if visual_summary:
        _para(document, visual_summary, size=9, color=_MUTED, after=8)
    _divider(document)
    _render_visual_report(document, meta, summary)
    # 水印紧跟内容：用空段撑开会把卡片顶到第二页（钉钉原件的封面是单页）
    _para(document, "内容由 EasyShare 会议听记生成", size=7.5, color=_WATERMARK,
          before=12, after=0, align=WD_ALIGN_PARAGRAPH.CENTER)

    # ---- 文档体 ----
    _page_break(document)
    _para(document, "纪要", size=24, bold=True, before=0, after=16, line=1.1)
    _para(document, f"主题: {title}", size=12, after=2)
    _para(document, f"时间: {created_label}", size=12, after=2)
    _para(document, f"参与人: {people_label}", size=12, after=10)
    _render_body_sections(document, meta, summary)
    _render_insights(document, summary)
    _render_todos(document, meta.get("todos") or [])
    _render_chapters(document, summary.get("chapters") or [])
    _render_quote_kp(document, summary)
    _render_action_table(document, meta.get("todos") or [])
    _para(document, "", size=6, after=6)
    _para(document, "内容由 EasyShare 会议听记生成", size=7.5, color=_WATERMARK,
          align=WD_ALIGN_PARAGRAPH.CENTER)

    # ---- 转写全文（可选附页） ----
    if with_transcript and meta.get("segments"):
        _page_break(document)
        _para(document, "转写全文", size=18, bold=True, color=_HEADING, before=0, after=8, line=1.3)
        names = meta.get("speaker_names") or {}
        for segment in meta["segments"]:
            who = names.get(segment.get("speaker", ""), f"说话人 {int(segment.get('speaker') or 0) + 1}")
            start = int(segment.get("start") or 0)
            paragraph = _para(document, "", before=0, after=3, line=1.5)
            _set_run(paragraph.add_run(f"{who} [{start // 60:02d}:{start % 60:02d}] "), size=9.5, bold=True, color=_MUTED)
            _set_run(paragraph.add_run(str(segment.get("text") or "")), size=10)

    buffer = io.BytesIO()
    document.save(buffer)
    return buffer.getvalue()
