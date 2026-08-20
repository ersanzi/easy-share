from __future__ import annotations

import io
from dataclasses import dataclass, field

from app.config import Settings
from app.jobs.store import ProcessingJob
from app.kb.embedder import HashEmbedder
from app.kb.store import VectorStore
from app.ocr import UnavailableOCRProvider
from app.parsing.mineru import (
    MinerUCapability,
    MinerUError,
    MinerUParseResult,
    UnavailableMinerUProvider,
    build_mineru_provider,
    document_from_mineru,
    parse_table_html,
)
from app.parsing.pdf_router import PdfRoute, build_pdf_router
from app.pipeline.service import DocumentPipeline
from tests.helpers import FakeStorage

# 手工构造的最小文本型 PDF（单页、Helvetica 文本），供本地管线回退路径测试使用
_TEXT_PDF_OBJECTS = [
    b"<< /Type /Catalog /Pages 2 0 R >>",
    b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
    b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
    b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
]
_TEXT_PDF_CONTENT = (
    b"BT /F1 18 Tf 72 770 Td (Quarterly Report) Tj ET\n"
    b"BT /F1 12 Tf 72 740 Td (EasyShare Knowledge Platform local extraction fallback.) Tj ET\n"
)


def _build_text_pdf() -> bytes:
    objects = list(_TEXT_PDF_OBJECTS)
    objects.append(b"<< /Length " + str(len(_TEXT_PDF_CONTENT)).encode() + b" >>\nstream\n" + _TEXT_PDF_CONTENT + b"endstream")
    out = io.BytesIO()
    out.write(b"%PDF-1.4\n")
    offsets = []
    for index, obj in enumerate(objects, start=1):
        offsets.append(out.tell())
        out.write(f"{index} 0 obj\n".encode() + obj + b"\nendobj\n")
    xref_pos = out.tell()
    out.write(b"xref\n0 " + str(len(objects) + 1).encode() + b"\n")
    out.write(b"0000000000 65535 f \n")
    for off in offsets:
        out.write(f"{off:010d} 00000 n \n".encode())
    out.write(f"trailer\n<< /Size {len(objects) + 1} /Root 1 0 R >>\nstartxref\n{xref_pos}\n%%EOF".encode())
    return out.getvalue()


CONTENT_LIST = [
    {"type": "title", "text": "采购管理制度", "text_level": 1, "page_idx": 0},
    {"type": "text", "text": "第一条 为规范采购行为，制定本制度。", "page_idx": 0},
    {
        "type": "table",
        "table_body": "<table><tr><td>物品</td><td>数量</td></tr><tr><td>打印纸</td><td>10</td></tr></table>",
        "table_caption": ["附表：常用物品"],
        "page_idx": 1,
    },
    {"type": "equation", "text": "E=mc^2", "page_idx": 1},
    {"type": "image", "img_caption": ["图 1 组织架构"], "page_idx": 2},
]


@dataclass
class FakeMineruOK:
    name: str = "fake-mineru"
    calls: int = 0

    def capability(self) -> MinerUCapability:
        return MinerUCapability(available=True, provider=self.name)

    def parse_pdf(self, content: bytes, *, filename: str) -> MinerUParseResult:
        self.calls += 1
        return MinerUParseResult(
            markdown="# 采购管理制度\n\n第一条 为规范采购行为制定本制度。",
            content_list=[dict(item) for item in CONTENT_LIST],
            backend="pipeline",
            duration_ms=5,
        )


@dataclass
class FakeMineruFail:
    name: str = "fake-mineru"

    def capability(self) -> MinerUCapability:
        return MinerUCapability(available=True, provider=self.name)

    def parse_pdf(self, content: bytes, *, filename: str) -> MinerUParseResult:
        raise MinerUError("服务不可达（模拟）")


@dataclass
class FakeRouter:
    route: PdfRoute
    name: str = "fake-router"

    def analyze(self, content: bytes) -> PdfRoute:
        return self.route


@dataclass
class ExplodingRouter:
    name: str = "exploding-router"

    def analyze(self, content: bytes) -> PdfRoute:
        raise RuntimeError("路由器崩溃（模拟）")


def _job(filename: str = "scan.pdf") -> ProcessingJob:
    return ProcessingJob(
        id="job-1",
        file_id="file-1",
        version_id="v1",
        object_key="source/v1/scan.pdf",
        filename=filename,
        status="processing",
        stage="starting",
        progress=1,
        retry_count=0,
        error_code=None,
        error_message=None,
        result=None,
        created_at="2026-08-20T00:00:00+00:00",
        updated_at="2026-08-20T00:00:00+00:00",
        started_at="2026-08-20T00:00:00+00:00",
        finished_at=None,
    )


def _pipeline(tmp_path, *, mineru=None, router=None) -> DocumentPipeline:
    return DocumentPipeline(
        storage=FakeStorage({"source/v1/scan.pdf": _build_text_pdf()}),
        embedder=HashEmbedder(32),
        vector_store=VectorStore(str(tmp_path / "vectors.json")),
        chunk_size=200,
        chunk_overlap=20,
        max_source_bytes=1024 * 1024,
        ocr_provider=UnavailableOCRProvider("OCR 已在测试配置中关闭"),
        ocr_min_text_chars=20,
        mineru_provider=mineru,
        pdf_router=router,
    )


# ---------- adapter 映射 ----------


def test_adapter_maps_content_list_to_blocks() -> None:
    result = MinerUParseResult(markdown="", content_list=[dict(item) for item in CONTENT_LIST], backend="pipeline")
    document = document_from_mineru("制度.pdf", result)

    assert [block.type for block in document.blocks] == [
        "heading", "paragraph", "paragraph", "table", "paragraph", "paragraph",
    ]
    assert document.blocks[0].text == "采购管理制度"
    assert document.blocks[0].level == 1
    assert document.blocks[0].source.page == 0
    assert document.blocks[2].text == "附表：常用物品"
    assert document.blocks[3].rows == [["物品", "数量"], ["打印纸", "10"]]
    assert document.blocks[3].source.page == 1
    assert "table_html" in document.blocks[3].metadata
    assert document.blocks[4].metadata["latex"] == "E=mc^2"
    assert document.blocks[5].metadata["figure"] is True
    assert document.media_type == "application/pdf"
    assert document.metadata["mineru"]["backend"] == "pipeline"


def test_adapter_falls_back_to_markdown_when_content_list_empty() -> None:
    result = MinerUParseResult(markdown="# 标题\n\n正文段落内容。", content_list=[], backend="pipeline")
    document = document_from_mineru("doc.pdf", result)

    assert document.blocks[0].type == "heading"
    assert document.media_type == "application/pdf"
    assert document.metadata["mineru"]["fallback"] == "markdown"


def test_adapter_raises_when_both_outputs_empty() -> None:
    import pytest

    result = MinerUParseResult(markdown="  ", content_list=[], backend="pipeline")
    with pytest.raises(ValueError, match="为空"):
        document_from_mineru("doc.pdf", result)


def test_parse_table_html_extracts_rows() -> None:
    rows = parse_table_html("<table><tr><th>列A</th><th>列B</th></tr><tr><td>1</td><td>2</td></tr></table>")
    assert rows == [["列A", "列B"], ["1", "2"]]


# ---------- Provider 构建与降级 ----------


def test_build_mineru_provider_disabled_by_default() -> None:
    config = Settings(_env_file=None, mineru_enabled=False)
    provider = build_mineru_provider(config)
    assert isinstance(provider, UnavailableMinerUProvider)
    assert provider.capability().available is False


def test_build_pdf_router_returns_none_when_disabled() -> None:
    assert build_pdf_router(False) is None


def test_build_pdf_router_returns_none_when_dependency_missing(monkeypatch) -> None:
    import sys

    monkeypatch.setitem(sys.modules, "pdf_inspector", None)
    assert build_pdf_router(True) is None


# ---------- 管线路由 ----------


def test_mineru_used_for_pdf_when_no_router(tmp_path) -> None:
    mineru = FakeMineruOK()
    manifest = _pipeline(tmp_path, mineru=mineru).process(_job(), lambda *_: None)

    assert manifest["parsing"]["provider"] == "mineru"
    assert manifest["parsing"]["backend"] == "pipeline"
    assert mineru.calls == 1


def test_mineru_failure_falls_back_to_local_pipeline(tmp_path) -> None:
    manifest = _pipeline(tmp_path, mineru=FakeMineruFail()).process(_job(), lambda *_: None)

    assert manifest["parsing"]["provider"] == "local"
    assert "服务不可达" in manifest["parsing"]["fallback_reason"]


def test_text_based_pdf_extracts_locally_and_skips_mineru(tmp_path) -> None:
    mineru = FakeMineruOK()
    router = FakeRouter(
        PdfRoute(
            kind="text_based",
            confidence=0.9,
            page_count=1,
            markdown="# 季度报告\n\n本地快速提取的正文内容，长度足以通过最小字符数校验。",
        )
    )
    manifest = _pipeline(tmp_path, mineru=mineru, router=router).process(_job(), lambda *_: None)

    assert manifest["parsing"]["provider"] == "pdf-inspector"
    assert manifest["parsing"]["router"]["kind"] == "text_based"
    assert mineru.calls == 0


def test_scanned_pdf_routes_to_mineru(tmp_path) -> None:
    mineru = FakeMineruOK()
    router = FakeRouter(PdfRoute(kind="scanned", confidence=0.9, page_count=1))
    manifest = _pipeline(tmp_path, mineru=mineru, router=router).process(_job(), lambda *_: None)

    assert manifest["parsing"]["provider"] == "mineru"
    assert manifest["parsing"]["router"]["kind"] == "scanned"
    assert mineru.calls == 1


def test_router_failure_records_error_and_falls_through(tmp_path) -> None:
    manifest = _pipeline(tmp_path, mineru=FakeMineruFail(), router=ExplodingRouter()).process(_job(), lambda *_: None)

    assert manifest["parsing"]["provider"] == "local"
    assert "路由器崩溃" in manifest["parsing"]["router_error"]
    assert "服务不可达" in manifest["parsing"]["fallback_reason"]


def test_non_pdf_never_touches_routing(tmp_path) -> None:
    mineru = FakeMineruOK()
    pipeline = _pipeline(tmp_path, mineru=mineru)
    pipeline.storage.objects["source/v1/scan.pdf"] = "第一段知识内容。\n\n第二段知识内容。".encode()
    manifest = pipeline.process(_job("note.txt"), lambda *_: None)

    assert manifest["parsing"]["provider"] == "local"
    assert mineru.calls == 0


def test_default_configuration_matches_legacy_behavior(tmp_path) -> None:
    # 两个可选层全关：manifest 有 parsing 字段且 provider=local，行为与引入路由前一致
    manifest = _pipeline(tmp_path).process(_job(), lambda *_: None)
    assert manifest["parsing"] == {"provider": "local"}
