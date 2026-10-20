"""可视化报告（汇报卡）结构：从 LLM 产物里取 / 兜底出 01-06 色卡模块。

钉钉那套「一键导出」的封面就是这张图。前端（MeetingVisualReport.vue）与 Word 导出共用
同一份结构，保证「页面上看到的」和「导出的」一致；Agent 也可以直接拿 JSON 自己画。
"""
from __future__ import annotations

CN_NUM = "一二三四五六七八九十"
ACCENT_ORDER = ("rose", "blue", "teal", "amber", "rose", "green")
"""钉钉报告六色带：01/05 同玫红、02 蓝、03 青、04 琥珀、06 绿。"""


def _fallback_sections(meta: dict, summary: dict) -> list[dict]:
    """老记录/弱模型没有 visual 时，按 minutes/key_points/sections/todos 就地折叠。"""
    fallback: list[dict] = []
    if summary.get("sections"):
        for section in summary["sections"]:
            bullets = [
                line.lstrip("-•* ").strip()
                for line in str(section.get("text") or "").splitlines()
                if line.strip()
            ]
            fallback.append({
                "title": section.get("title", ""),
                "layout": "lists",
                "items": [{"title": "", "bullets": bullets[:6], "tag": "", "note": ""}],
            })
    if summary.get("key_points"):
        fallback.append({
            "title": "要点",
            "layout": "lists",
            "items": [{"title": "", "bullets": list(summary["key_points"])[:6], "tag": "", "note": ""}],
        })
    if summary.get("minutes"):
        fallback.insert(0, {
            "title": "会议纪要",
            "layout": "closing",
            "items": [{"title": "", "bullets": [], "tag": "", "note": str(summary["minutes"])[:120]}],
        })
    if meta.get("todos"):
        fallback.append({
            "title": "待办",
            "layout": "lists",
            "items": [{
                "title": "",
                "bullets": [str(todo.get("task") or "") for todo in meta["todos"][:6]],
                "tag": "",
                "note": "",
            }],
        })
    for index, section in enumerate(fallback):
        section.setdefault("accent", ACCENT_ORDER[index % len(ACCENT_ORDER)])
    return fallback


def visual_sections(meta: dict, summary: dict) -> list[dict]:
    """取 LLM 的结构化 visual；没有就兜底折叠，保证永远有卡可画。"""
    sections = ((summary or {}).get("visual") or {}).get("sections") or []
    if sections:
        return sections
    return _fallback_sections(meta, summary or {})


def build_visual_report(meta: dict) -> dict:
    """对外发布的汇报卡结构（CLI `visual` 子命令 / `visual.json` / 前端共用的形状）。"""
    summary = meta.get("summary") or {}
    sections = visual_sections(meta, summary)
    return {
        "title": meta.get("title") or "会议纪要",
        "created_at": meta.get("created_at") or "",
        "speaker_names": meta.get("speaker_names") or {},
        "summary": (summary.get("visual") or {}).get("summary") or "",
        "sections": sections,
    }
