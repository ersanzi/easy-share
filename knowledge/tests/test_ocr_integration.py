from __future__ import annotations

import io
import os

import pytest

from app.ocr import PaddleOCRProvider

pytestmark = pytest.mark.ocr_integration


def test_real_paddleocr_recognizes_synthetic_image() -> None:
    if os.getenv("EASYSHARE_OCR_INTEGRATION") != "1":
        pytest.skip("set EASYSHARE_OCR_INTEGRATION=1 to run PaddleOCR integration test")

    from PIL import Image, ImageDraw, ImageFont

    provider = PaddleOCRProvider(lang="ch")
    capability = provider.capability()
    assert capability.available, capability.reason

    image = Image.new("RGB", (1000, 240), "white")
    draw = ImageDraw.Draw(image)
    font_path = "C:/Windows/Fonts/arial.ttf"
    font = ImageFont.truetype(font_path, 96) if os.path.exists(font_path) else ImageFont.load_default()
    draw.text((30, 60), "EasyShare OCR 2026", fill="black", font=font)
    buffer = io.BytesIO()
    image.save(buffer, format="PNG")

    result = provider.recognize_image(buffer.getvalue(), filename="synthetic.png", page=1)

    assert result.blocks
    recognized = "".join(character for block in result.blocks for character in block.text if character.isalnum())
    assert len(recognized) >= 8
