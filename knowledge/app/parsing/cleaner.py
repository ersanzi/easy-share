"""基础文本清洗：保留文档结构，只规范化块内容并去掉相邻重复块。"""
from __future__ import annotations

import re
import unicodedata

from app.parsing.models import DocumentBlock, ParsedDocument

_CONTROL_CHARS = re.compile(r"[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]")
_INLINE_SPACE = re.compile(r"[ \t\u00a0\u3000]+")
_BLANK_LINES = re.compile(r"\n{3,}")


def clean_text(value: str) -> str:
    """规范 Unicode、换行和多余空白，不改变中文标点与段落语义。"""
    value = unicodedata.normalize("NFKC", value or "")
    value = value.replace("\r\n", "\n").replace("\r", "\n")
    value = _CONTROL_CHARS.sub("", value)
    lines = [_INLINE_SPACE.sub(" ", line).strip() for line in value.split("\n")]
    value = "\n".join(lines).strip()
    return _BLANK_LINES.sub("\n\n", value)


def clean_document(document: ParsedDocument) -> ParsedDocument:
    cleaned: list[DocumentBlock] = []
    previous_signature: tuple | None = None
    removed_duplicates = 0

    for block in document.blocks:
        text = clean_text(block.text)
        rows = [[clean_text(cell) for cell in row] for row in block.rows]
        rows = [row for row in rows if any(row)]
        if not text and not rows:
            continue

        signature = (block.type, text, tuple(tuple(row) for row in rows))
        if signature == previous_signature:
            removed_duplicates += 1
            continue

        cleaned.append(
            DocumentBlock(
                id=f"b{len(cleaned) + 1}",
                type=block.type,
                text=text,
                level=block.level,
                rows=rows,
                source=block.source,
                metadata=block.metadata,
            )
        )
        previous_signature = signature

    warnings = list(document.warnings)
    if removed_duplicates:
        warnings.append(f"清洗时移除了 {removed_duplicates} 个相邻重复内容块")

    return ParsedDocument(
        filename=document.filename,
        media_type=document.media_type,
        blocks=cleaned,
        warnings=warnings,
        metadata=document.metadata,
    )
