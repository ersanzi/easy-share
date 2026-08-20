"""PDF 快路由：pdf-inspector 分类 + 文本型本地提取，作为字符数启发式之上的增强层。

pdf-inspector 为可选依赖（pip install pdf-inspector），未安装或未启用时返回 None，
管线自动退回现有 ocr_min_text_chars 启发式分流。
"""
from __future__ import annotations

import logging
import time
from dataclasses import dataclass, field
from typing import Any

logger = logging.getLogger(__name__)


@dataclass(slots=True)
class PdfRoute:
    kind: str  # text_based / scanned / image_based / mixed
    confidence: float
    page_count: int
    pages_needing_ocr: list[int] = field(default_factory=list)
    has_encoding_issues: bool = False
    markdown: str | None = None  # text_based 且本地提取有产出时
    duration_ms: int | None = None

    def to_dict(self) -> dict[str, Any]:
        return {
            "kind": self.kind,
            "confidence": round(self.confidence, 3),
            "page_count": self.page_count,
            "pages_needing_ocr": self.pages_needing_ocr,
            "has_encoding_issues": self.has_encoding_issues,
            "duration_ms": self.duration_ms,
        }


class PdfInspectorRouter:
    """包装 pdf-inspector：单次 process_pdf_bytes 同时完成分类与文本型提取。"""

    name = "pdf-inspector"

    def analyze(self, content: bytes) -> PdfRoute:
        import pdf_inspector  # 可选依赖，延迟导入

        started = time.monotonic()
        result = pdf_inspector.process_pdf_bytes(content)
        return PdfRoute(
            kind=result.pdf_type,
            confidence=float(result.confidence),
            page_count=int(result.page_count),
            pages_needing_ocr=list(result.pages_needing_ocr),
            has_encoding_issues=bool(result.has_encoding_issues),
            markdown=result.markdown,
            duration_ms=int((time.monotonic() - started) * 1000),
        )


def build_pdf_router(enabled: bool) -> PdfInspectorRouter | None:
    """未启用或依赖缺失返回 None，调用方退回现有启发式分流。"""
    if not enabled:
        return None
    try:
        import pdf_inspector  # noqa: F401
    except ImportError:
        logger.warning("pdf-inspector 已启用但未安装（pip install pdf-inspector），退回现有启发式分流")
        return None
    return PdfInspectorRouter()
