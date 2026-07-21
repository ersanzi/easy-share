"""文本切块：先按段落聚合到目标大小，超长段落硬切，相邻块带 overlap 保留上下文。"""


def chunk_text(text: str, chunk_size: int = 800, overlap: int = 120) -> list[str]:
    text = text.strip()
    if not text:
        return []

    paragraphs = [p.strip() for p in text.split("\n") if p.strip()]
    chunks: list[str] = []
    buf = ""
    for para in paragraphs:
        if len(buf) + len(para) + 1 <= chunk_size:
            buf = f"{buf}\n{para}" if buf else para
            continue
        if buf:
            chunks.append(buf)
        if len(para) > chunk_size:
            step = max(1, chunk_size - overlap)
            for i in range(0, len(para), step):
                chunks.append(para[i : i + chunk_size])
            buf = ""
        else:
            buf = para
    if buf:
        chunks.append(buf)

    if overlap > 0 and len(chunks) > 1:
        merged = [chunks[0]]
        for i in range(1, len(chunks)):
            merged.append(chunks[i - 1][-overlap:] + chunks[i])
        chunks = merged

    return [c for c in chunks if c.strip()]
