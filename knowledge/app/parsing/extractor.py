"""文档解析：按扩展名分发，提取纯文本。骨架支持 txt/md/docx/pdf，其余尝试当文本解码。"""
import io
import logging

logger = logging.getLogger(__name__)


def extract_text(filename: str, content: bytes) -> str:
    name = filename.lower()
    try:
        if name.endswith(".docx"):
            return _from_docx(content)
        if name.endswith(".pdf"):
            return _from_pdf(content)
    except Exception as exc:  # 解析失败退回文本解码，尽量不丢内容
        logger.warning("解析 %s 失败，退回纯文本: %s", filename, exc)
    return _from_text(content)


def _from_text(content: bytes) -> str:
    for enc in ("utf-8", "gbk", "latin-1"):
        try:
            return content.decode(enc)
        except UnicodeDecodeError:
            continue
    return content.decode("utf-8", errors="ignore")


def _from_docx(content: bytes) -> str:
    from docx import Document

    doc = Document(io.BytesIO(content))
    parts = [p.text for p in doc.paragraphs if p.text.strip()]
    for table in doc.tables:
        for row in table.rows:
            cells = [c.text.strip() for c in row.cells if c.text.strip()]
            if cells:
                parts.append(" | ".join(cells))
    return "\n".join(parts)


def _from_pdf(content: bytes) -> str:
    from pypdf import PdfReader

    reader = PdfReader(io.BytesIO(content))
    parts = []
    for page in reader.pages:
        text = page.extract_text() or ""
        if text.strip():
            parts.append(text)
    return "\n".join(parts)
