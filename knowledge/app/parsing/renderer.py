"""把统一文档结构渲染为便于人工检查和后续切块的 Markdown。"""
from __future__ import annotations

from app.parsing.models import DocumentBlock, ParsedDocument


def _escape_cell(value: str) -> str:
    return value.replace("|", "\\|").replace("\n", "<br>")


def _render_table(block: DocumentBlock) -> str:
    if not block.rows:
        return ""
    width = max(len(row) for row in block.rows)
    rows = [row + [""] * (width - len(row)) for row in block.rows]
    header = rows[0]
    lines = [
        "| " + " | ".join(_escape_cell(cell) for cell in header) + " |",
        "| " + " | ".join("---" for _ in range(width)) + " |",
    ]
    for row in rows[1:]:
        lines.append("| " + " | ".join(_escape_cell(cell) for cell in row) + " |")
    return "\n".join(lines)


def render_markdown(document: ParsedDocument) -> str:
    parts: list[str] = []
    for block in document.blocks:
        if block.type == "heading":
            level = min(max(block.level or 2, 1), 6)
            parts.append(f"{'#' * level} {block.text}")
        elif block.type == "table":
            rendered = _render_table(block)
            if rendered:
                parts.append(rendered)
        elif block.text:
            parts.append(block.text)
    return "\n\n".join(parts).strip() + "\n"
