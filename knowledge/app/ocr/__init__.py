"""OCR 模块导出。"""
from app.ocr.base import (
    OCRCapability,
    OCRPageResult,
    OCRProvider,
    OCRRecognitionError,
    OCRTextBlock,
    OCRUnavailableError,
    UnavailableOCRProvider,
)
from app.ocr.paddle import PaddleOCRProvider, build_paddle_provider

__all__ = [
    "OCRCapability",
    "OCRPageResult",
    "OCRProvider",
    "OCRRecognitionError",
    "OCRTextBlock",
    "OCRUnavailableError",
    "PaddleOCRProvider",
    "UnavailableOCRProvider",
    "build_paddle_provider",
]
