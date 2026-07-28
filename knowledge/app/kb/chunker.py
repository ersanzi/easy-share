"""文本与结构化文档切块。"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from app.parsing.models import DocumentBlock, ParsedDocument
from app.parsing.renderer import render_block


@dataclass(slots=True)
class DocumentChunk:
    """包含正文及来源追踪 metadata 的文档切块。"""

    text: str
    block_ids: list[str] = field(default_factory=list)
    source_locations: list[dict[str, Any]] = field(default_factory=list)
    extraction_methods: list[str] = field(default_factory=list)

    def metadata(self) -> dict[str, Any]:
        return {
            "block_ids": self.block_ids,
            "source_locations": self.source_locations,
            "extraction_methods": self.extraction_methods,
        }


def chunk_text(text: str, chunk_size: int = 800, overlap: int = 120) -> list[str]:
    text = text.strip()
    if not text:
        return []

    paragraphs = [p.strip() for p in text.split("\n") if p.strip()]
    chunks: list[str] = []
    buf = ""
    for para in paragraphs:
        if len(buf) + len(para) + 1 <= chunk_size:
            buf = f"{buf}\n{para}" if buf else para
            continue
        if buf:
            chunks.append(buf)
        if len(para) > chunk_size:
            step = max(1, chunk_size - overlap)
            for i in range(0, len(para), step):
                chunks.append(para[i : i + chunk_size])
            buf = ""
        else:
            buf = para
    if buf:
        chunks.append(buf)

    if overlap > 0 and len(chunks) > 1:
        merged = [chunks[0]]
        for i in range(1, len(chunks)):
            merged.append(chunks[i - 1][-overlap:] + chunks[i])
        chunks = merged

    return [c for c in chunks if c.strip()]


def _source_dict(block: DocumentBlock) -> dict[str, Any]:
    source = block.source
    return {key: value for key, value in {
        "page": source.page,
        "sheet": source.sheet,
        "slide": source.slide,
        "paragraph": source.paragraph,
        "table": source.table,
        "row": source.row,
    }.items() if value is not None}


def chunk_document(document: ParsedDocument, chunk_size: int = 800, overlap: int = 120) -> list[DocumentChunk]:
    """按结构化文档块切分，并保留块标识、来源位置与提取方式。"""
    rendered: list[tuple[DocumentBlock, str]] = [
        (block, render_block(block)) for block in document.blocks
    ]
    rendered = [(block, text.strip()) for block, text in rendered if text.strip()]
    if not rendered:
        return []

    chunks: list[DocumentChunk] = []
    buffer_text = ""
    buffer_blocks: list[DocumentBlock] = []

    def emit() -> None:
        nonlocal buffer_text, buffer_blocks
        if not buffer_text.strip():
            return
        locations = []
        methods = []
        for block in buffer_blocks:
            location = _source_dict(block)
            if location and location not in locations:
                locations.append(location)
            method = str(block.metadata.get("extraction_method", "text_layer"))
            if method not in methods:
                methods.append(method)
        chunks.append(DocumentChunk(
            text=buffer_text.strip(),
            block_ids=[block.id for block in buffer_blocks],
            source_locations=locations,
            extraction_methods=methods,
        ))
        buffer_text = ""
        buffer_blocks = []

    for block, text in rendered:
        candidate = f"{buffer_text}\n\n{text}" if buffer_text else text
        if len(candidate) <= chunk_size:
            buffer_text = candidate
            buffer_blocks.append(block)
            continue
        emit()
        if len(text) <= chunk_size:
            buffer_text = text
            buffer_blocks = [block]
            continue
        for start in range(0, len(text), chunk_size):
            piece = text[start : start + chunk_size]
            if piece.strip():
                chunks.append(DocumentChunk(
                    text=piece,
                    block_ids=[block.id],
                    source_locations=[_source_dict(block)] if _source_dict(block) else [],
                    extraction_methods=[str(block.metadata.get("extraction_method", "text_layer"))],
                ))
    emit()

    if overlap > 0 and len(chunks) > 1:
        raw_chunks = chunks
        merged: list[DocumentChunk] = [raw_chunks[0]]
        for index, current in enumerate(raw_chunks[1:], start=1):
            previous = raw_chunks[index - 1]
            locations = list(previous.source_locations)
            for location in current.source_locations:
                if location not in locations:
                    locations.append(location)
            methods = list(previous.extraction_methods)
            for method in current.extraction_methods:
                if method not in methods:
                    methods.append(method)
            merged.append(DocumentChunk(
                text=previous.text[-overlap:] + current.text,
                block_ids=list(dict.fromkeys(previous.block_ids + current.block_ids)),
                source_locations=locations,
                extraction_methods=methods,
            ))
        chunks = merged
    return chunks
