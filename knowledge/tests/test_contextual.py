"""Contextual Chunking：文档级摘要构建与切块注入规则。

覆盖三条产品红线：
- LLM 未配置/调用失败必须退回启发式，摘要生成永不阻塞入库
- 启发式摘要只在无标题上下文的段落注入（有标题路径即冗余，词袋口径实测稀释）
- 结构性标题块（段内无正文）不注入前缀
"""
from __future__ import annotations

from app.kb.chunker import chunk_document
from app.kb.contextual import DocContextBuilder, clamp_context
from app.parsing.models import DocumentBlock, ParsedDocument


def _doc(blocks: list[DocumentBlock], filename: str = "demo.md") -> ParsedDocument:
    return ParsedDocument(filename=filename, media_type="text/markdown", blocks=blocks)


def _heading(text: str, level: int) -> DocumentBlock:
    return DocumentBlock(id=f"h-{text}", type="heading", text=text, level=level)


def _para(text: str) -> DocumentBlock:
    return DocumentBlock(id=f"p-{text[:6]}", type="paragraph", text=text)


def test_llm_failure_falls_back_to_heuristic_and_never_raises() -> None:
    class _Boom:
        def chat(self):  # noqa: ANN001 - 测试桩
            raise RuntimeError("网络炸了")

    builder = DocContextBuilder(max_chars=60)
    builder.attach_llm(_Boom(), "test-model")
    document = _doc([_heading("报销制度", 1), _para("住宿上限三百元。")])

    context = builder.build(document, "报销制度.md")

    assert context.provider == "heuristic"
    assert "报销制度" in context.summary


def test_heuristic_context_only_injected_into_sections_without_heading_path() -> None:
    document = _doc([
        _heading("报销制度", 1),
        _para("正文一。"),
        _heading("住宿标准", 2),
        _para("正文二。"),
    ])

    chunks = chunk_document(document, chunk_size=800, overlap=0, doc_summary="报销制度（涵盖：住宿标准）", heuristic_mode=True)

    assert all("[文档]" not in chunk.text for chunk in chunks), "有标题路径的段落应去重跳过"


def test_heuristic_context_injected_into_headingless_documents() -> None:
    document = _doc([_para("会议室预定需提前一天。"), _para("使用后关闭投影仪。")], filename="meeting.txt")

    chunks = chunk_document(document, chunk_size=800, overlap=0, doc_summary="会议室预定规范", heuristic_mode=True)

    assert chunks, "无标题文档应产生切块"
    assert all(chunk.text.startswith("[文档] 会议室预定规范") for chunk in chunks)


def test_llm_context_injected_everywhere_with_heading_path() -> None:
    document = _doc([
        _heading("报销制度", 1),
        _para("正文一。"),
        _heading("住宿标准", 2),
        _para("正文二。"),
    ])

    chunks = chunk_document(document, chunk_size=800, overlap=0, doc_summary="LLM 生成的语义词摘要", heuristic_mode=False)

    assert all("[文档] LLM 生成的语义词摘要" in chunk.text for chunk in chunks)
    assert any("[报销制度 > 住宿标准]" in chunk.text for chunk in chunks), "标题路径前缀应保留"


def test_heading_only_section_never_gets_doc_prefix() -> None:
    document = _doc([
        _heading("封面", 1),
        _para("封面说明。"),
        _heading("目录", 2),
    ])

    chunks = chunk_document(document, chunk_size=800, overlap=0, doc_summary="任意摘要", heuristic_mode=False)

    heading_only = [chunk for chunk in chunks if "目录" in chunk.text and "封面说明" not in chunk.text]
    assert heading_only, "应存在仅含标题的切块"
    assert all("[文档]" not in chunk.text for chunk in heading_only), "结构性标题块不注入摘要"


def test_clamp_context_trims_at_sentence_boundary() -> None:
    text = "第一句话。" * 30

    clamped = clamp_context(text, 50)

    assert len(clamped) <= 50
    assert clamped.endswith("。")


def test_doc_summary_physical_budget_truncates_long_text() -> None:
    document = _doc([_para("正文。" * 200)])

    chunks = chunk_document(document, chunk_size=300, overlap=0, doc_summary="长" * 500, heuristic_mode=True)

    assert chunks
    prefix_line = chunks[0].text.split("\n", 1)[0]
    assert len(prefix_line) <= 300 // 3, "前缀标签行不得超过块预算"
