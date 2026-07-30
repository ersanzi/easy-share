"""文本与结构化文档切块。

结构感知策略（2026-07-29）：
- 按标题边界（level ≤ split_level，默认 H1/H2）分段，不跨主要章节合并
- 每个切块注入标题层级上下文（如 [公司制度 > 报销标准]），提升 embedding 主题辨识度
- 表格保持完整；超出 chunk_size 时按行拆分并保留表头
- overlap 仅在段内生效，不跨段污染
"""
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


# ---------------------------------------------------------------------------
# 内部工具
# ---------------------------------------------------------------------------

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


def _make_chunk(text: str, blocks: list[DocumentBlock]) -> DocumentChunk:
    """从块列表构建 DocumentChunk，汇总来源追踪 metadata。"""
    locations: list[dict[str, Any]] = []
    methods: list[str] = []
    for block in blocks:
        location = _source_dict(block)
        if location and location not in locations:
            locations.append(location)
        method = str(block.metadata.get("extraction_method", "text_layer"))
        if method not in methods:
            methods.append(method)
    return DocumentChunk(
        text=text.strip(),
        block_ids=[block.id for block in blocks],
        source_locations=locations,
        extraction_methods=methods,
    )


def _render_table_rows(block: DocumentBlock, start_row: int, end_row: int) -> str:
    """渲染表格的指定行范围（始终包含表头行）。"""
    if not block.rows:
        return ""
    width = max(len(row) for row in block.rows)
    header = block.rows[0] + [""] * (width - len(block.rows[0]))

    def escape(cell: str) -> str:
        return cell.replace("|", "\\|").replace("\n", "<br>")

    lines = [
        "| " + " | ".join(escape(c) for c in header) + " |",
        "| " + " | ".join("---" for _ in range(width)) + " |",
    ]
    for row in block.rows[start_row:end_row]:
        padded = row + [""] * (width - len(row))
        lines.append("| " + " | ".join(escape(c) for c in padded) + " |")
    return "\n".join(lines)


def _split_large_table(block: DocumentBlock, chunk_size: int) -> list[str]:
    """超大表格按行拆分，每片保留表头。"""
    if not block.rows or len(block.rows) <= 1:
        return [render_block(block)]
    pieces: list[str] = []
    row_index = 1  # 跳过表头
    while row_index < len(block.rows):
        piece = _render_table_rows(block, row_index, len(block.rows))
        if len(piece) <= chunk_size or row_index == 1:
            # 尝试贪心加入更多行
            end = row_index + 1
            while end < len(block.rows):
                candidate = _render_table_rows(block, row_index, end + 1)
                if len(candidate) > chunk_size:
                    break
                end += 1
            pieces.append(_render_table_rows(block, row_index, end))
            row_index = end
        else:
            # 单行仍超限，强制放入
            pieces.append(_render_table_rows(block, row_index, row_index + 1))
            row_index += 1
    return [p for p in pieces if p.strip()]


# ---------------------------------------------------------------------------
# 结构感知切块
# ---------------------------------------------------------------------------

@dataclass
class _Section:
    """标题边界划分出的文档段落。"""
    context: str  # 标题层级上下文，如 "公司制度 > 报销标准"
    blocks: list[tuple[DocumentBlock, str]] = field(default_factory=list)


def _group_sections(
    rendered: list[tuple[DocumentBlock, str]],
    split_level: int,
) -> list[_Section]:
    """按标题边界将块序列分组为段落。

    - level ≤ split_level 的标题触发新段落
    - 所有层级的标题都参与层级上下文构建
    """
    heading_stack: list[tuple[int, str]] = []  # (level, text)
    sections: list[_Section] = []
    current_blocks: list[tuple[DocumentBlock, str]] = []

    def context_str() -> str:
        return " > ".join(text for _, text in heading_stack)

    def flush() -> None:
        nonlocal current_blocks
        if current_blocks:
            sections.append(_Section(context=context_str(), blocks=current_blocks))
            current_blocks = []

    for block, text in rendered:
        if block.type == "heading":
            level = block.level or 2

            if level <= split_level:
                # 硬分段：先用旧上下文输出已有块，再更新标题栈
                flush()
                while heading_stack and heading_stack[-1][0] >= level:
                    heading_stack.pop()
                heading_stack.append((level, block.text.strip()))
                current_blocks = [(block, text)]
            else:
                # 软分段：高层级标题不切断，但更新上下文
                while heading_stack and heading_stack[-1][0] >= level:
                    heading_stack.pop()
                heading_stack.append((level, block.text.strip()))
                current_blocks.append((block, text))
        else:
            current_blocks.append((block, text))

    flush()
    return sections


def _chunk_section(
    section: _Section,
    chunk_size: int,
    overlap: int,
) -> list[DocumentChunk]:
    """在单个段落内贪心合并块，注入标题上下文。"""
    context_prefix = f"[{section.context}]\n" if section.context else ""
    prefix_len = len(context_prefix)
    # 前缀占比过高时放弃前缀，保证正文切块粒度不被吞掉
    if prefix_len > chunk_size // 3:
        context_prefix = ""
        prefix_len = 0
    effective_size = chunk_size - prefix_len

    chunks: list[DocumentChunk] = []
    buf_text = ""
    buf_blocks: list[DocumentBlock] = []

    def emit() -> None:
        nonlocal buf_text, buf_blocks
        if not buf_text.strip():
            return
        full_text = context_prefix + buf_text if context_prefix else buf_text
        chunks.append(_make_chunk(full_text, buf_blocks))
        buf_text = ""
        buf_blocks = []

    for block, text in section.blocks:
        # 表格特殊处理：保持完整，不与前后文本混合
        if block.type == "table":
            emit()  # 先输出已有缓冲
            if len(text) <= effective_size:
                full_text = context_prefix + text if context_prefix else text
                chunks.append(_make_chunk(full_text, [block]))
            else:
                for piece in _split_large_table(block, effective_size):
                    full_text = context_prefix + piece if context_prefix else piece
                    chunks.append(_make_chunk(full_text, [block]))
            continue

        candidate = f"{buf_text}\n\n{text}" if buf_text else text
        if len(candidate) <= effective_size:
            buf_text = candidate
            buf_blocks.append(block)
            continue

        emit()

        if len(text) <= effective_size:
            buf_text = text
            buf_blocks = [block]
            continue

        # 超长块按固定步长拆分（overlap 由段内后处理统一叠加）
        for start in range(0, len(text), effective_size):
            piece = text[start : start + effective_size]
            if piece.strip():
                full_text = context_prefix + piece if context_prefix else piece
                chunks.append(_make_chunk(full_text, [block]))

    emit()

    # 段内 overlap：把前一切块尾部并入下一切块
    if overlap > 0 and len(chunks) > 1:
        merged: list[DocumentChunk] = [chunks[0]]
        for index in range(1, len(chunks)):
            prev = chunks[index - 1]
            curr = chunks[index]
            locations = list(prev.source_locations)
            for loc in curr.source_locations:
                if loc not in locations:
                    locations.append(loc)
            methods = list(prev.extraction_methods)
            for method in curr.extraction_methods:
                if method not in methods:
                    methods.append(method)
            merged.append(DocumentChunk(
                text=prev.text[-overlap:] + curr.text,
                block_ids=list(dict.fromkeys(prev.block_ids + curr.block_ids)),
                source_locations=locations,
                extraction_methods=methods,
            ))
        chunks = merged

    return chunks


def chunk_document(
    document: ParsedDocument,
    chunk_size: int = 800,
    overlap: int = 120,
    split_level: int = 2,
) -> list[DocumentChunk]:
    """按结构化文档块切分，保留块标识、来源位置与提取方式。

    结构感知策略：
    - 标题（level ≤ split_level）触发段落边界，不跨主要章节合并
    - 每个切块注入 [标题层级] 上下文前缀
    - 表格保持完整，超大表格按行拆分并保留表头
    - overlap 仅在段内生效
    """
    rendered: list[tuple[DocumentBlock, str]] = [
        (block, render_block(block)) for block in document.blocks
    ]
    rendered = [(block, text.strip()) for block, text in rendered if text.strip()]
    if not rendered:
        return []

    # 无标题时退化为单段，行为与旧版一致
    has_headings = any(block.type == "heading" for block, _ in rendered)
    if not has_headings:
        section = _Section(context="", blocks=rendered)
        return _chunk_section(section, chunk_size, overlap)

    sections = _group_sections(rendered, split_level)
    all_chunks: list[DocumentChunk] = []
    for section in sections:
        all_chunks.extend(_chunk_section(section, chunk_size, overlap))
    return all_chunks
