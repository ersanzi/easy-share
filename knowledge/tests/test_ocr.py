from __future__ import annotations

import io
import sys
import time
from types import ModuleType

import pytest
from fastapi.testclient import TestClient
from pypdf import PdfWriter

from app.main import create_app
from app.ocr import (
    OCRCapability,
    OCRPageResult,
    OCRTextBlock,
    OCRUnavailableError,
    PaddleOCRProvider,
    UnavailableOCRProvider,
)
from app.parsing.extractor import DocumentParseError, parse_document
from tests.golden.builders import build_pdf_text_with_blank_page
from tests.helpers import FakeStorage, make_services


class FakeOCRProvider:
    name = "fake-ocr"

    def __init__(self) -> None:
        self.calls: list[tuple[str, int]] = []

    def capability(self) -> OCRCapability:
        return OCRCapability(available=True, provider=self.name)

    def recognize_image(self, content: bytes, *, filename: str, page: int) -> OCRPageResult:
        self.calls.append((filename, page))
        return OCRPageResult(
            page=page,
            blocks=[
                OCRTextBlock(
                    text=f"EasyShare OCR page {page}",
                    confidence=0.95,
                    bbox=[0.0, 0.0, 100.0, 0.0, 100.0, 20.0, 0.0, 20.0],
                ),
                OCRTextBlock(text="low confidence", confidence=0.4),
            ],
            duration_ms=7,
        )


def _blank_pdf() -> bytes:
    writer = PdfWriter()
    writer.add_blank_page(width=100, height=100)
    buffer = io.BytesIO()
    writer.write(buffer)
    return buffer.getvalue()


def _wait_for_terminal(client: TestClient, job_id: str) -> dict:
    for _ in range(100):
        response = client.get(f"/jobs/{job_id}")
        assert response.status_code == 200
        payload = response.json()
        if payload["status"] in {"completed", "failed"}:
            return payload
        time.sleep(0.01)
    raise AssertionError("job did not finish in time")


def test_paddleocr_engine_is_initialized_only_on_first_use(monkeypatch) -> None:
    created: list[dict[str, object]] = []
    module = ModuleType("paddleocr")

    class FakePaddleOCR:
        def __init__(self, **kwargs: object) -> None:
            created.append(kwargs)

    module.PaddleOCR = FakePaddleOCR
    monkeypatch.setitem(sys.modules, "paddleocr", module)
    monkeypatch.setattr("app.ocr.paddle.importlib.util.find_spec", lambda _module: object())

    provider = PaddleOCRProvider(lang="ch")

    assert provider.capability().available is True
    assert created == []
    assert provider._ensure_engine() is provider._ensure_engine()
    assert created == [{"lang": "ch", "use_angle_cls": True, "show_log": False}]


def test_paddleocr_initialization_failure_updates_capability(monkeypatch) -> None:
    module = ModuleType("paddleocr")

    class BrokenPaddleOCR:
        def __init__(self, **_kwargs: object) -> None:
            raise RuntimeError("model unavailable")

    module.PaddleOCR = BrokenPaddleOCR
    monkeypatch.setitem(sys.modules, "paddleocr", module)
    monkeypatch.setattr("app.ocr.paddle.importlib.util.find_spec", lambda _module: object())
    provider = PaddleOCRProvider()

    with pytest.raises(OCRUnavailableError, match="PaddleOCR"):
        provider._ensure_engine()

    capability = provider.capability()
    assert capability.available is False
    assert capability.reason is not None
    assert "model unavailable" in capability.reason


def test_paddleocr_legacy_result_parser_keeps_text_confidence_and_bbox() -> None:
    raw = [[[
        [[0, 0], [10, 0], [10, 5], [0, 5]],
        ["EasyShare", 0.93],
    ]]]

    blocks = PaddleOCRProvider._parse_legacy_result(raw)

    assert len(blocks) == 1
    assert blocks[0].text == "EasyShare"
    assert blocks[0].confidence == pytest.approx(0.93)
    assert blocks[0].bbox == [0.0, 0.0, 10.0, 0.0, 10.0, 5.0, 0.0, 5.0]


def test_pdf_render_dependency_error_is_not_hidden_as_ocr_failure(monkeypatch) -> None:
    provider = FakeOCRProvider()

    def fail_render(*_args) -> bytes:
        raise DocumentParseError("PyMuPDF missing")

    monkeypatch.setattr("app.parsing.extractor._render_pdf_page", fail_render)

    with pytest.raises(DocumentParseError, match="PyMuPDF missing"):
        parse_document("scan.pdf", _blank_pdf(), ocr_provider=provider)


def test_image_ocr_preserves_page_confidence_bbox_and_manifest_fields() -> None:
    provider = FakeOCRProvider()

    document = parse_document("scan.png", b"fake-image", ocr_provider=provider)

    assert provider.calls == [("scan.png", 1)]
    assert document.media_type == "image/png"
    assert document.blocks[0].source.page == 1
    assert document.blocks[0].metadata == {
        "extraction_method": "ocr",
        "ocr_confidence": 0.95,
        "ocr_bbox": [0.0, 0.0, 100.0, 0.0, 100.0, 20.0, 0.0, 20.0],
    }
    assert document.metadata["ocr"] == {
        "provider": "fake-ocr",
        "pages_total": 1,
        "pages_ocr": [1],
        "failed_pages": [],
        "low_confidence_blocks": 1,
        "duration_ms": 7,
    }


def test_scanned_pdf_uses_ocr_and_mixed_pdf_only_ocr_empty_page(monkeypatch) -> None:
    provider = FakeOCRProvider()
    monkeypatch.setattr("app.parsing.extractor._render_pdf_page", lambda *_: b"rendered-page")

    scanned = parse_document("scan.pdf", _blank_pdf(), ocr_provider=provider)
    assert scanned.metadata["empty_pages"] == 1
    assert scanned.metadata["ocr"]["pages_ocr"] == [1]
    assert scanned.metadata["ocr"]["low_confidence_blocks"] == 1
    assert [block.source.page for block in scanned.blocks] == [1, 1]

    provider.calls.clear()
    mixed = parse_document(
        "mixed.pdf",
        build_pdf_text_with_blank_page(),
        ocr_provider=provider,
    )
    assert provider.calls == [("mixed.pdf", 2)]
    assert mixed.metadata["ocr"]["pages_ocr"] == [2]
    assert any(block.source.page == 1 and block.metadata == {} for block in mixed.blocks)
    assert any(block.source.page == 2 and block.metadata["extraction_method"] == "ocr" for block in mixed.blocks)


def test_short_text_layer_falls_back_to_original_when_ocr_unavailable() -> None:
    provider = UnavailableOCRProvider("disabled for test")

    document = parse_document(
        "short.pdf",
        build_pdf_text_with_blank_page(),
        ocr_provider=provider,
        ocr_min_text_chars=100,
    )

    assert any("EasyShare PDF Golden Page One" in block.text for block in document.blocks)
    assert document.metadata["ocr"]["failed_pages"] == [2]


def test_image_without_ocr_returns_actionable_error() -> None:
    with pytest.raises(DocumentParseError) as exc_info:
        parse_document(
            "scan.jpg",
            b"fake-image",
            ocr_provider=UnavailableOCRProvider("OCR disabled"),
        )

    message = str(exc_info.value)
    assert "OCR disabled" in message
    assert "requirements-ocr.txt" in message


def test_health_manifest_and_query_expose_ocr_source_metadata(tmp_path) -> None:
    provider = FakeOCRProvider()
    storage = FakeStorage({"source/scan.png": b"fake-image"})
    services = make_services(tmp_path, storage, ocr_provider=provider)
    app = create_app(services)

    with TestClient(app) as client:
        health = client.get("/health")
        assert health.status_code == 200
        assert health.json()["ocr"] == {
            "available": True,
            "provider": "fake-ocr",
            "reason": None,
            "formats": ["pdf", "png", "jpg", "jpeg", "bmp", "tif", "tiff"],
        }

        submitted = client.post(
            "/documents/process",
            json={
                "file_id": "scan-1",
                "version_id": "v1",
                "object_key": "source/scan.png",
            },
        )
        completed = _wait_for_terminal(client, submitted.json()["id"])
        assert completed["status"] == "completed"

        manifest = client.get("/documents/scan-1/versions/v1/artifacts")
        assert manifest.status_code == 200
        assert manifest.json()["ocr"]["provider"] == "fake-ocr"
        assert manifest.json()["ocr"]["low_confidence_blocks"] == 1

        query = client.post("/query", json={"question": "EasyShare OCR", "doc_ids": ["scan-1"]})
        assert query.status_code == 200
        context = query.json()["contexts"][0]
        assert context["block_ids"]
        assert context["source_locations"] == [{"page": 1}]
        assert context["extraction_methods"] == ["ocr"]
