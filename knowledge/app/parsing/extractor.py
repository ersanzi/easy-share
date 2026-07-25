"""多格式文档解析：TXT/Markdown/DOCX/文本型 PDF/XLSX/PPTX → 统一结构。"""
from __future__ import annotations

import io
import logging
import os
import re
from collections.abc import Iterable

from app.parsing.cleaner import clean_document
from app.parsing.models import DocumentBlock, ParsedDocument, SourceLocation
from app.parsing.renderer import render_markdown

logger = logging.getLogger(__name__)
SUPPORTED_EXTENSIONS = {".txt", ".md", ".markdown", ".docx", ".pdf", ".xlsx", ".pptx"}


class DocumentParseError(ValueError):
    """文件格式不支持、损坏或没有可提取内容。"""


def parse_document(filename: str, content: bytes) -> ParsedDocument:
    extension = os.path.splitext(filename.lower())[1]
    if extension not in SUPPORTED_EXTENSIONS:
        supported = ", ".join(sorted(SUPPORTED_EXTENSIONS))
        raise DocumentParseError(f"不支持的文件格式 {extension or '(无扩展名)'}，当前支持: {supported}")
    if not content:
        raise DocumentParseError("文件内容为空")

    try:
        if extension in {".txt", ".md", ".markdown"}:
            document = _parse_text(filename, content, markdown=extension != ".txt")
        elif extension == ".docx":
            document = _parse_docx(filename, content)
        elif extension == ".pdf":
            document = _parse_pdf(filename, content)
        elif extension == ".xlsx":
            document = _parse_xlsx(filename, content)
        else:
            document = _parse_pptx(filename, content)
    except DocumentParseError:
        raise
    except Exception as exc:
        raise DocumentParseError(f"解析 {filename} 失败: {exc}") from exc

    document = clean_document(document)
    if not document.blocks:
        raise DocumentParseError(f"{filename} 没有可提取的文本或表格内容")
    return document


def extract_text(filename: str, content: bytes) -> str:
    """兼容旧入口：新解析器完成结构化解析后渲染为 Markdown 文本。"""
    return render_markdown(parse_document(filename, content)).strip()


def _decode_text(content: bytes) -> str:
    for encoding in ("utf-8-sig", "gb18030"):
        try:
            return content.decode(encoding)
        except UnicodeDecodeError:
            continue
    raise DocumentParseError("文本文件编码无法识别，仅支持 UTF-8 或 GB18030 兼容编码")


def _parse_text(filename: str, content: bytes, *, markdown: bool) -> ParsedDocument:
    text = _decode_text(content)
    if markdown:
        return _parse_markdown(filename, text)

    blocks: list[DocumentBlock] = []
    for index, raw in enumerate(re.split(r"\n\s*\n", text), start=1):
        value = raw.strip()
        if not value:
            continue
        blocks.append(
            DocumentBlock(
                id=f"b{len(blocks) + 1}",
                type="paragraph",
                text=value,
                source=SourceLocation(paragraph=index),
            )
        )
    return ParsedDocument(filename=filename, media_type="text/plain", blocks=blocks)


def _parse_markdown(filename: str, text: str) -> ParsedDocument:
    """按标题边界切分 Markdown，避免标题与紧随正文被合并成普通段落。"""
    blocks: list[DocumentBlock] = []
    paragraph_lines: list[str] = []
    paragraph_start = 1

    def flush_paragraph() -> None:
        if not paragraph_lines:
            return
        blocks.append(
            DocumentBlock(
                id=f"b{len(blocks) + 1}",
                type="paragraph",
                text="\n".join(paragraph_lines).strip(),
                source=SourceLocation(paragraph=paragraph_start),
            )
        )
        paragraph_lines.clear()

    for line_number, raw_line in enumerate(text.splitlines(), start=1):
        heading = re.fullmatch(r"\s*(#{1,6})\s+(.+?)\s*", raw_line)
        if heading:
            flush_paragraph()
            blocks.append(
                DocumentBlock(
                    id=f"b{len(blocks) + 1}",
                    type="heading",
                    text=heading.group(2),
                    level=len(heading.group(1)),
                    source=SourceLocation(paragraph=line_number),
                )
            )
            continue
        if not raw_line.strip():
            flush_paragraph()
            continue
        if not paragraph_lines:
            paragraph_start = line_number
        paragraph_lines.append(raw_line)

    flush_paragraph()
    return ParsedDocument(filename=filename, media_type="text/markdown", blocks=blocks)

def _iter_docx_blocks(document) -> Iterable[tuple[str, object]]:
    from docx.table import Table
    from docx.text.paragraph import Paragraph
    from docx.oxml.table import CT_Tbl
    from docx.oxml.text.paragraph import CT_P

    for child in document.element.body.iterchildren():
        if isinstance(child, CT_P):
            yield "paragraph", Paragraph(child, document)
        elif isinstance(child, CT_Tbl):
            yield "table", Table(child, document)


def _parse_docx(filename: str, content: bytes) -> ParsedDocument:
    from docx import Document

    document = Document(io.BytesIO(content))
    blocks: list[DocumentBlock] = []
    paragraph_number = 0
    table_number = 0
    for kind, item in _iter_docx_blocks(document):
        if kind == "paragraph":
            paragraph_number += 1
            text = item.text.strip()
            if not text:
                continue
            style_name = (item.style.name if item.style is not None else "") or ""
            match = re.search(r"(?:Heading|标题)\s*([1-6])", style_name, re.IGNORECASE)
            block_type = "heading" if match else "paragraph"
            blocks.append(
                DocumentBlock(
                    id=f"b{len(blocks) + 1}",
                    type=block_type,
                    text=text,
                    level=int(match.group(1)) if match else None,
                    source=SourceLocation(paragraph=paragraph_number),
                    metadata={"style": style_name} if style_name else {},
                )
            )
        else:
            table_number += 1
            rows = [[cell.text.strip() for cell in row.cells] for row in item.rows]
            blocks.append(
                DocumentBlock(
                    id=f"b{len(blocks) + 1}",
                    type="table",
                    rows=rows,
                    source=SourceLocation(table=table_number),
                )
            )
    return ParsedDocument(
        filename=filename,
        media_type="application/vnd.openxmlformats-officedocument.wordprocessingml.document",
        blocks=blocks,
        metadata={"paragraphs": paragraph_number, "tables": table_number},
    )


def _parse_pdf(filename: str, content: bytes) -> ParsedDocument:
    from pypdf import PdfReader

    reader = PdfReader(io.BytesIO(content))
    blocks: list[DocumentBlock] = []
    empty_pages = 0
    for page_number, page in enumerate(reader.pages, start=1):
        text = page.extract_text() or ""
        paragraphs = [part.strip() for part in re.split(r"\n\s*\n|(?<=。)\s*\n", text) if part.strip()]
        if not paragraphs:
            empty_pages += 1
            continue
        for paragraph in paragraphs:
            blocks.append(
                DocumentBlock(
                    id=f"b{len(blocks) + 1}",
                    type="paragraph",
                    text=paragraph,
                    source=SourceLocation(page=page_number),
                )
            )
    if not blocks:
        raise DocumentParseError("PDF 没有可提取的文本层；扫描件需要后续 OCR 管线")
    warnings = []
    if empty_pages:
        warnings.append(f"有 {empty_pages} 页未提取到文本，可能包含扫描页或纯图片")
    return ParsedDocument(
        filename=filename,
        media_type="application/pdf",
        blocks=blocks,
        warnings=warnings,
        metadata={"pages": len(reader.pages), "empty_pages": empty_pages},
    )


def _cell_text(value: object) -> str:
    if value is None:
        return ""
    if hasattr(value, "isoformat"):
        try:
            return value.isoformat()
        except TypeError:
            pass
    return str(value)


def _parse_xlsx(filename: str, content: bytes) -> ParsedDocument:
    from openpyxl import load_workbook

    workbook = load_workbook(io.BytesIO(content), read_only=True, data_only=True)
    blocks: list[DocumentBlock] = []
    sheet_count = len(workbook.sheetnames)
    try:
        for worksheet in workbook.worksheets:
            blocks.append(
                DocumentBlock(
                    id=f"b{len(blocks) + 1}",
                    type="heading",
                    text=worksheet.title,
                    level=1,
                    source=SourceLocation(sheet=worksheet.title),
                )
            )
            rows: list[list[str]] = []
            start_row: int | None = None
            for row_number, values in enumerate(worksheet.iter_rows(values_only=True), start=1):
                cells = [_cell_text(value) for value in values]
                while cells and not cells[-1]:
                    cells.pop()
                if not any(cells):
                    if rows:
                        blocks.append(
                            DocumentBlock(
                                id=f"b{len(blocks) + 1}",
                                type="table",
                                rows=rows,
                                source=SourceLocation(sheet=worksheet.title, row=start_row),
                            )
                        )
                        rows = []
                        start_row = None
                    continue
                if start_row is None:
                    start_row = row_number
                rows.append(cells)
                if len(rows) >= 200:
                    blocks.append(
                        DocumentBlock(
                            id=f"b{len(blocks) + 1}",
                            type="table",
                            rows=rows,
                            source=SourceLocation(sheet=worksheet.title, row=start_row),
                        )
                    )
                    rows = []
                    start_row = None
            if rows:
                blocks.append(
                    DocumentBlock(
                        id=f"b{len(blocks) + 1}",
                        type="table",
                        rows=rows,
                        source=SourceLocation(sheet=worksheet.title, row=start_row),
                    )
                )
    finally:
        workbook.close()
    return ParsedDocument(
        filename=filename,
        media_type="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
        blocks=blocks,
        metadata={"sheets": sheet_count},
    )


def _parse_pptx(filename: str, content: bytes) -> ParsedDocument:
    from pptx import Presentation

    presentation = Presentation(io.BytesIO(content))
    blocks: list[DocumentBlock] = []
    for slide_number, slide in enumerate(presentation.slides, start=1):
        title_shape = slide.shapes.title
        for shape in slide.shapes:
            if getattr(shape, "has_table", False):
                rows = [[cell.text.strip() for cell in row.cells] for row in shape.table.rows]
                blocks.append(
                    DocumentBlock(
                        id=f"b{len(blocks) + 1}",
                        type="table",
                        rows=rows,
                        source=SourceLocation(slide=slide_number),
                    )
                )
                continue
            if not getattr(shape, "has_text_frame", False):
                continue
            for paragraph_number, paragraph in enumerate(shape.text_frame.paragraphs, start=1):
                text = paragraph.text.strip()
                if not text:
                    continue
                is_title = shape is title_shape
                blocks.append(
                    DocumentBlock(
                        id=f"b{len(blocks) + 1}",
                        type="heading" if is_title else "paragraph",
                        text=text,
                        level=1 if is_title else None,
                        source=SourceLocation(slide=slide_number, paragraph=paragraph_number),
                    )
                )
    return ParsedDocument(
        filename=filename,
        media_type="application/vnd.openxmlformats-officedocument.presentationml.presentation",
        blocks=blocks,
        metadata={"slides": len(presentation.slides)},
    )
