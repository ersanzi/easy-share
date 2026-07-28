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


def render_block(block: DocumentBlock) -> str:
    """把单个结构化文档块渲染为 Markdown。"""
    if block.type == "heading":
        level = min(max(block.level or 2, 1), 6)
        return f"{'#' * level} {block.text}" if block.text else ""
    if block.type == "table":
        return _render_table(block)
    return block.text or ""


def render_markdown(document: ParsedDocument) -> str:
    parts = [render_block(block) for block in document.blocks]
    parts = [part for part in parts if part.strip()]
    return "\n\n".join(parts).strip() + "\n"
