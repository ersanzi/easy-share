"""MinerU content_list → 统一块模型映射：下游清洗/切块/索引零改动。"""
from __future__ import annotations

import re
from html.parser import HTMLParser
from typing import Any

from app.parsing.mineru.base import MinerUParseResult
from app.parsing.models import DocumentBlock, ParsedDocument, SourceLocation


class _TableHTMLParser(HTMLParser):
    """极简 <table>/<tr>/<td> 提取器，避免为表格解析引入第三方依赖。"""

    def __init__(self) -> None:
        super().__init__()
        self.rows: list[list[str]] = []
        self._row: list[str] | None = None
        self._cell: list[str] | None = None

    def handle_starttag(self, tag: str, attrs) -> None:
        if tag == "tr":
            self._row = []
        elif tag in ("td", "th") and self._row is not None:
            self._cell = []

    def handle_endtag(self, tag: str) -> None:
        if tag in ("td", "th") and self._cell is not None and self._row is not None:
            self._row.append("".join(self._cell).strip())
            self._cell = None
        elif tag == "tr" and self._row is not None:
            if self._row:
                self.rows.append(self._row)
            self._row = None

    def handle_data(self, data: str) -> None:
        if self._cell is not None:
            self._cell.append(data)


def parse_table_html(html: str) -> list[list[str]]:
    parser = _TableHTMLParser()
    parser.feed(html or "")
    parser.close()
    return parser.rows


def _clean_text(value: Any) -> str:
    if not isinstance(value, str):
        return ""
    return re.sub(r"\s+", " ", value).strip()


def document_from_mineru(filename: str, result: MinerUParseResult) -> ParsedDocument:
    """content_list 逐条映射为统一块；为空时退回 markdown 解析，两者皆空则报错触发回退。"""
    blocks: list[DocumentBlock] = []
    for index, item in enumerate(result.content_list):
        item_type = item.get("type", "text")
        page = item.get("page_idx")
        source = SourceLocation(page=page if isinstance(page, int) else None)
        if item_type == "title":
            level = item.get("text_level")
            blocks.append(
                DocumentBlock(
                    id=f"mineru-{index}",
                    type="heading",
                    text=_clean_text(item.get("text")),
                    level=level if isinstance(level, int) and level >= 1 else 1,
                    source=source,
                )
            )
        elif item_type == "table":
            table_html = item.get("table_body") or ""
            rows = parse_table_html(table_html)
            captions = [_clean_text(c) for c in item.get("table_caption") or [] if _clean_text(c)]
            if captions:
                blocks.append(
                    DocumentBlock(id=f"mineru-{index}-caption", type="paragraph", text=" ".join(captions), source=source)
                )
            if rows:
                blocks.append(
                    DocumentBlock(
                        id=f"mineru-{index}",
                        type="table",
                        rows=rows,
                        source=source,
                        metadata={"table_html": table_html} if table_html else {},
                    )
                )
        elif item_type == "equation":
            latex = _clean_text(item.get("text"))
            blocks.append(
                DocumentBlock(
                    id=f"mineru-{index}",
                    type="paragraph",
                    text=latex,
                    source=source,
                    metadata={"latex": latex} if latex else {},
                )
            )
        elif item_type == "image":
            captions = [_clean_text(c) for c in item.get("img_caption") or [] if _clean_text(c)]
            if captions:
                blocks.append(
                    DocumentBlock(
                        id=f"mineru-{index}",
                        type="paragraph",
                        text=" ".join(captions),
                        source=source,
                        metadata={"figure": True},
                    )
                )
        else:
            # text 及未知类型按正文兜底，宁可多保内容也不静默丢弃
            text = _clean_text(item.get("text"))
            if text:
                blocks.append(DocumentBlock(id=f"mineru-{index}", type="paragraph", text=text, source=source))

    if blocks:
        return ParsedDocument(
            filename=filename,
            media_type="application/pdf",
            blocks=blocks,
            metadata={"mineru": {"backend": result.backend, "items": len(result.content_list)}},
        )

    if result.markdown.strip():
        from app.parsing.extractor import _parse_markdown

        document = _parse_markdown(filename, result.markdown)
        document.media_type = "application/pdf"
        document.metadata = {"mineru": {"backend": result.backend, "fallback": "markdown"}}
        return document
    raise ValueError("MinerU 返回内容为空（content_list 与 markdown 均缺失）")
