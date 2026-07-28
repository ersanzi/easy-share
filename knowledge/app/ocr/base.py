"""OCR 能力抽象：默认不强制安装重量级 OCR 依赖。"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Protocol


class OCRUnavailableError(RuntimeError):
    """OCR 依赖或模型不可用。"""


class OCRRecognitionError(RuntimeError):
    """单页 OCR 失败。"""


@dataclass(slots=True)
class OCRTextBlock:
    text: str
    confidence: float | None = None
    bbox: list[float] | None = None


@dataclass(slots=True)
class OCRPageResult:
    page: int
    blocks: list[OCRTextBlock] = field(default_factory=list)
    duration_ms: int | None = None

    @property
    def text(self) -> str:
        return "\n".join(block.text.strip() for block in self.blocks if block.text.strip())

    @property
    def low_confidence_blocks(self) -> int:
        return sum(
            1 for block in self.blocks
            if block.confidence is not None and block.confidence < 0.6
        )


@dataclass(slots=True)
class OCRCapability:
    available: bool
    provider: str
    reason: str | None = None
    formats: list[str] = field(default_factory=lambda: ["pdf", "png", "jpg", "jpeg", "bmp", "tif", "tiff"])

    def to_dict(self) -> dict[str, Any]:
        return {
            "available": self.available,
            "provider": self.provider,
            "reason": self.reason,
            "formats": self.formats,
        }


class OCRProvider(Protocol):
    name: str

    def capability(self) -> OCRCapability:
        ...

    def recognize_image(self, content: bytes, *, filename: str, page: int) -> OCRPageResult:
        ...


@dataclass(slots=True)
class UnavailableOCRProvider:
    """未安装 OCR 时的明确失败实现，保证文本型文档仍可正常处理。"""

    reason: str = "未安装 OCR 依赖；扫描件需要安装 PaddleOCR 可选依赖"
    name: str = "unavailable"

    def capability(self) -> OCRCapability:
        return OCRCapability(available=False, provider=self.name, reason=self.reason)

    def recognize_image(self, content: bytes, *, filename: str, page: int) -> OCRPageResult:
        raise OCRUnavailableError(self.reason)
