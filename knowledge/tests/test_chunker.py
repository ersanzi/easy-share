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
