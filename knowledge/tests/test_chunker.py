from __future__ import annotations

from app.kb.chunker import chunk_document
from app.parsing.models import DocumentBlock, ParsedDocument, SourceLocation


def test_chunk_document_preserves_immediate_source_metadata_without_transitive_leakage() -> None:
    document = ParsedDocument(
        filename="mixed.pdf",
        media_type="application/pdf",
        blocks=[
            DocumentBlock(
                id="text-p1",
                type="paragraph",
                text="A" * 12,
                source=SourceLocation(page=1),
            ),
            DocumentBlock(
                id="ocr-p2-1",
                type="paragraph",
                text="B" * 12,
                source=SourceLocation(page=2),
                metadata={"extraction_method": "ocr"},
            ),
            DocumentBlock(
                id="text-p3",
                type="paragraph",
                text="C" * 12,
                source=SourceLocation(page=3),
            ),
        ],
    )

    chunks = chunk_document(document, chunk_size=12, overlap=3)

    assert len(chunks) == 3
    assert chunks[0].block_ids == ["text-p1"]
    assert chunks[1].block_ids == ["text-p1", "ocr-p2-1"]
    assert chunks[1].source_locations == [{"page": 1}, {"page": 2}]
    assert chunks[1].extraction_methods == ["text_layer", "ocr"]
    assert chunks[2].block_ids == ["ocr-p2-1", "text-p3"]
    assert chunks[2].source_locations == [{"page": 2}, {"page": 3}]
    assert {"page": 1} not in chunks[2].source_locations


def test_chunk_document_splits_long_block_and_keeps_source() -> None:
    document = ParsedDocument(
        filename="scan.png",
        media_type="image/png",
        blocks=[
            DocumentBlock(
                id="ocr-p1-1",
                type="paragraph",
                text="0123456789ABCDEFGHIJ",
                source=SourceLocation(page=1),
                metadata={"extraction_method": "ocr"},
            )
        ],
    )

    chunks = chunk_document(document, chunk_size=10, overlap=2)

    assert [chunk.text for chunk in chunks] == ["0123456789", "89ABCDEFGHIJ"]
    assert all(chunk.block_ids == ["ocr-p1-1"] for chunk in chunks)
    assert all(chunk.source_locations == [{"page": 1}] for chunk in chunks)
    assert all(chunk.extraction_methods == ["ocr"] for chunk in chunks)


# ---------------------------------------------------------------------------
# 结构感知切块测试
# ---------------------------------------------------------------------------


def _heading(id: str, text: str, level: int) -> DocumentBlock:
    return DocumentBlock(id=id, type="heading", text=text, level=level)


def _para(id: str, text: str, page: int | None = None) -> DocumentBlock:
    return DocumentBlock(id=id, type="paragraph", text=text, source=SourceLocation(page=page))


def _table(id: str, rows: list[list[str]]) -> DocumentBlock:
    return DocumentBlock(id=id, type="table", rows=rows)


def test_heading_boundary_prevents_cross_section_merge() -> None:
    """H2 标题应切断段落，两侧内容不合并到同一切块。"""
    document = ParsedDocument(
        filename="policy.docx",
        media_type="application/vnd.openxmlformats-officedocument.wordprocessingml.document",
        blocks=[
            _heading("h1", "公司制度", 1),
            _heading("h2a", "报销标准", 2),
            _para("p1", "住宿每晚 400 元"),
            _heading("h2b", "考勤制度", 2),
            _para("p2", "弹性工作制"),
        ],
    )

    chunks = chunk_document(document, chunk_size=800, overlap=0)

    # 两个 H2 段落应产出至少两个切块
    assert len(chunks) >= 2
    # 第一个切块包含报销内容，不包含考勤内容
    reimburse_chunks = [c for c in chunks if "400 元" in c.text]
    attendance_chunks = [c for c in chunks if "弹性工作制" in c.text]
    assert reimburse_chunks
    assert attendance_chunks
    # 不应有切块同时包含两个章节的内容
    for chunk in chunks:
        assert not ("400 元" in chunk.text and "弹性工作制" in chunk.text)


def test_heading_context_injected_into_chunks() -> None:
    """每个切块应注入 [标题层级] 上下文前缀。"""
    document = ParsedDocument(
        filename="policy.docx",
        media_type="application/vnd.openxmlformats-officedocument.wordprocessingml.document",
        blocks=[
            _heading("h1", "公司制度", 1),
            _heading("h2", "报销标准", 2),
            _para("p1", "住宿每晚 400 元"),
        ],
    )

    chunks = chunk_document(document, chunk_size=800, overlap=0)

    assert len(chunks) >= 1
    # 包含正文的切块应有层级上下文
    content_chunks = [c for c in chunks if "400 元" in c.text]
    assert content_chunks
    assert "[公司制度 > 报销标准]" in content_chunks[0].text


def test_table_kept_intact_as_single_chunk() -> None:
    """表格应作为独立切块，不与前后文本混合。"""
    document = ParsedDocument(
        filename="report.xlsx",
        media_type="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
        blocks=[
            _heading("h1", "项目进度", 1),
            _para("p1", "以下是项目状态："),
            _table("t1", [
                ["项目", "负责人", "状态"],
                ["OCR", "Bob", "完成"],
                ["清洗", "Alice", "进行中"],
            ]),
            _para("p2", "以上为汇总。"),
        ],
    )

    chunks = chunk_document(document, chunk_size=800, overlap=0)

    # 表格应独立成块
    table_chunks = [c for c in chunks if "OCR" in c.text and "Bob" in c.text]
    assert len(table_chunks) == 1
    table_chunk = table_chunks[0]
    assert "Alice" in table_chunk.text  # 表格完整
    assert "以下是项目状态" not in table_chunk.text  # 不混入前文
    assert "以上为汇总" not in table_chunk.text  # 不混入后文


def test_large_table_split_preserves_header() -> None:
    """超大表格按行拆分时，每片保留表头。"""
    rows = [["名称", "值"]] + [[f"item{i}", f"val{i}"] for i in range(50)]
    document = ParsedDocument(
        filename="big.xlsx",
        media_type="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
        blocks=[_table("t1", rows)],
    )

    chunks = chunk_document(document, chunk_size=200, overlap=0)

    assert len(chunks) > 1
    # 每个切块都应包含表头
    for chunk in chunks:
        assert "名称" in chunk.text
        assert "值" in chunk.text


def test_h3_does_not_split_but_updates_context() -> None:
    """H3 标题不触发硬分段，H2 段落内的所有内容保持在一起。"""
    document = ParsedDocument(
        filename="manual.docx",
        media_type="application/vnd.openxmlformats-officedocument.wordprocessingml.document",
        blocks=[
            _heading("h1", "操作手册", 1),
            _heading("h2", "安装指南", 2),
            _para("p1", "第一步下载安装包"),
            _heading("h3", "Windows 环境", 3),
            _para("p2", "运行 exe 文件"),
        ],
    )

    chunks = chunk_document(document, chunk_size=800, overlap=0)

    # H1 和 H2 各触发一个段落 → 2 个切块；H3 不引起第 3 次切分
    assert len(chunks) == 2
    # H2 段落应包含所有后续内容（含 H3 下的内容）
    h2_chunk = [c for c in chunks if "安装指南" in c.text and "第一步" in c.text]
    assert len(h2_chunk) == 1
    assert "运行 exe 文件" in h2_chunk[0].text
    # H3 参与层级上下文
    assert "Windows 环境" in h2_chunk[0].text


def test_source_metadata_preserved_with_headings() -> None:
    """结构感知切块仍保留来源追踪 metadata。"""
    document = ParsedDocument(
        filename="scan.pdf",
        media_type="application/pdf",
        blocks=[
            _heading("h1", "扫描件", 1),
            DocumentBlock(
                id="ocr-p1",
                type="paragraph",
                text="识别出的文字",
                source=SourceLocation(page=1),
                metadata={"extraction_method": "ocr"},
            ),
        ],
    )

    chunks = chunk_document(document, chunk_size=800, overlap=0)

    content_chunks = [c for c in chunks if "识别出的文字" in c.text]
    assert content_chunks
    chunk = content_chunks[0]
    assert "ocr-p1" in chunk.block_ids
    assert {"page": 1} in chunk.source_locations
    assert "ocr" in chunk.extraction_methods
