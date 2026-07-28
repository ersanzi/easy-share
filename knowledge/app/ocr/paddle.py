"""PaddleOCR 适配器：依赖懒加载，普通开发和 CI 不下载模型。"""
from __future__ import annotations

import importlib.util
import io
import threading
import time
from dataclasses import dataclass, field

from app.ocr.base import (
    OCRCapability,
    OCRPageResult,
    OCRProvider,
    OCRRecognitionError,
    OCRTextBlock,
    OCRUnavailableError,
)


@dataclass(slots=True)
class PaddleOCRProvider:
    """PaddleOCR 2.x 适配器；初始化失败通过 capability 暴露，不污染文本路径。"""

    lang: str = "ch"
    use_angle_cls: bool = True
    name: str = "paddleocr"
    _engine: object | None = field(init=False, default=None, repr=False)
    _reason: str | None = field(init=False, default=None, repr=False)

    _init_lock: threading.Lock = field(init=False, default_factory=threading.Lock, repr=False)

    def __post_init__(self) -> None:
        self._engine = None
        missing = [
            package
            for module, package in (
                ("paddleocr", "paddleocr"),
                ("PIL", "Pillow"),
                ("numpy", "numpy"),
            )
            if importlib.util.find_spec(module) is None
        ]
        self._reason = (
            f"未安装 OCR 依赖：{', '.join(missing)}"
            if missing
            else None
        )

    def capability(self) -> OCRCapability:
        return OCRCapability(
            available=self._reason is None,
            provider=self.name,
            reason=self._reason,
        )

    def _ensure_engine(self) -> object:
        if self._reason is not None:
            raise OCRUnavailableError(self._reason)
        if self._engine is not None:
            return self._engine

        with self._init_lock:
            if self._reason is not None:
                raise OCRUnavailableError(self._reason)
            if self._engine is not None:
                return self._engine
            try:
                from paddleocr import PaddleOCR

                self._engine = PaddleOCR(
                    lang=self.lang,
                    use_angle_cls=self.use_angle_cls,
                    show_log=False,
                )
            except Exception as exc:  # noqa: BLE001 - 第三方初始化差异转为能力错误
                self._reason = f"PaddleOCR 初始化失败：{exc}"
                raise OCRUnavailableError(self._reason) from exc
            return self._engine

    def recognize_image(self, content: bytes, *, filename: str, page: int) -> OCRPageResult:
        engine = self._ensure_engine()
        try:
            from PIL import Image
            import numpy as np

            image = np.asarray(Image.open(io.BytesIO(content)).convert("RGB"))
            started = time.perf_counter()
            raw = engine.ocr(image, cls=self.use_angle_cls)
            blocks = self._parse_legacy_result(raw)
            return OCRPageResult(
                page=page,
                blocks=blocks,
                duration_ms=round((time.perf_counter() - started) * 1000),
            )
        except OCRRecognitionError:
            raise
        except Exception as exc:  # noqa: BLE001 - 转成领域错误，避免泄漏第三方堆栈
            raise OCRRecognitionError(f"{filename} 第 {page} 页 OCR 失败：{exc}") from exc

    @staticmethod
    def _parse_legacy_result(raw: object) -> list[OCRTextBlock]:
        """解析 PaddleOCR 2.x 的 [[box, [text, score]], ...] 结果。"""
        if raw is None:
            return []
        pages = raw
        if isinstance(raw, list) and len(raw) == 1 and isinstance(raw[0], list):
            pages = raw[0]
        if not isinstance(pages, list):
            raise OCRRecognitionError("PaddleOCR 返回了无法识别的结果")

        blocks: list[OCRTextBlock] = []
        for item in pages:
            if not isinstance(item, (list, tuple)) or len(item) < 2:
                continue
            box, detail = item[0], item[1]
            if not isinstance(detail, (list, tuple)) or not detail:
                continue
            text = str(detail[0]).strip()
            if not text:
                continue
            score = None
            if len(detail) > 1:
                try:
                    score = float(detail[1])
                except (TypeError, ValueError):
                    score = None
            bbox = None
            if isinstance(box, list):
                try:
                    bbox = [float(value) for point in box for value in point]
                except (TypeError, ValueError):
                    bbox = None
            blocks.append(OCRTextBlock(text=text, confidence=score, bbox=bbox))
        return blocks


def build_paddle_provider(*, enabled: bool = True, lang: str = "ch") -> OCRProvider:
    if not enabled:
        from app.ocr.base import UnavailableOCRProvider

        return UnavailableOCRProvider("OCR 已通过配置关闭")
    return PaddleOCRProvider(lang=lang)
