"""文档级上下文注入（Contextual Chunking，对标 Anthropic Contextual Retrieval）。

制造业语料里的型号编码/工艺参数/表格列名脱离文档语境即失义：为每个切块注入
文档级定位摘要（文档是什么、覆盖哪些主题），同时服务向量与 BM25 两路索引。
Anthropic 原法是逐块调 LLM（整文档入 prompt）；私有部署语料可达 100MB 且
入库走本地进程，故收敛为**每文档一次** LLM 摘要 + 既有标题路径前缀的简化版。
失败链路：LLM 未配置或调用失败一律退回启发式摘要（标题大纲 + 开篇要点），
摘要生成永不阻塞入库。
"""
from __future__ import annotations

import logging
import re
from dataclasses import dataclass
from typing import Any

from app.config import Settings
from app.parsing.models import ParsedDocument
from app.parsing.renderer import render_block

logger = logging.getLogger(__name__)


@dataclass(slots=True)
class DocContext:
    """文档级上下文注入产物：摘要文本 + 文档标题（供切块器去重判断）。"""

    summary: str
    title: str
    provider: str  # llm | heuristic

SUMMARY_SYSTEM_PROMPT = (
    "你是检索索引预处理助手。根据给定的文档大纲与开头内容，"
    "用不超过 100 字写一段该文档的定位摘要：说明这是什么文档、覆盖哪些主题。"
    "只输出摘要本身，不要任何前后缀、引号或解释。"
)

_OUTLINE_HEAD_LIMIT = 1200  # 喂给 LLM 的正文开头字符数
_LABEL_LEN = len("[文档] \n")  # 注入时前缀标签的物理开销


def clamp_context(text: str, max_chars: int) -> str:
    """压缩空白并截断到上限；尽量在句读边界收尾，避免摘要以半个词告终。"""
    text = re.sub(r"\s+", " ", text).strip()
    if len(text) <= max_chars:
        return text
    window = text[:max_chars]
    for i in range(len(window) - 1, int(max_chars * 0.6) - 1, -1):
        if window[i - 1] in "。！？；，、":
            return window[:i].rstrip("，、")
    return window.rstrip("，、;；")


class DocContextBuilder:
    """构建文档级定位摘要：LLM 优先，未配置或失败退回启发式。"""

    def __init__(self, max_chars: int = 120) -> None:
        self.max_chars = max(20, max_chars)
        self._llm: Any = None
        self._model = ""
        self.provider = "heuristic"

    def attach_llm(self, client: Any, model: str) -> None:
        """接入 OpenAI 兼容聊天客户端；不接入则永远走启发式。"""
        self._llm = client
        self._model = model
        self.provider = "llm"

    def build(self, document: ParsedDocument, filename: str) -> DocContext:
        title = self._title(document, filename)
        summary = None
        if self._llm is not None:
            summary = self._llm_summary(document, filename)
        provider = self.provider
        if not summary:
            summary = self._heuristic(document, filename)
            provider = "heuristic"
        return DocContext(
            summary=clamp_context(summary, self.max_chars), title=title, provider=provider
        )

    def _title(self, document: ParsedDocument, filename: str) -> str:
        """文档标题：H1 → 正文首行伪标题（无标题文档，如 txt 导出）→ 文件名主干。"""
        heading = next(
            (block.text.strip() for block in document.blocks
             if block.type == "heading" and (block.level or 2) == 1),
            "",
        )
        if heading:
            return heading
        first_line = next(
            (render_block(block).strip().splitlines()[0][:40]
             for block in document.blocks
             if block.type != "heading" and render_block(block).strip()),
            "",
        )
        if first_line:
            return first_line
        return re.sub(r"\.[A-Za-z0-9]+$", "", filename).strip() or filename

    # -- LLM 摘要 -----------------------------------------------------------

    def _llm_summary(self, document: ParsedDocument, filename: str) -> str | None:
        try:
            user = (
                f"文件名：{filename}\n\n文档大纲：\n{self._outline(document) or '（无标题结构）'}"
                f"\n\n开头内容：\n{self._head_text(document)}"
            )
            resp = self._llm.chat.completions.create(
                model=self._model,
                messages=[
                    {"role": "system", "content": SUMMARY_SYSTEM_PROMPT},
                    {"role": "user", "content": user},
                ],
            )
            text = (resp.choices[0].message.content or "").strip().strip("\"“”'‘")
            if text:
                return text
            logger.warning("LLM 文档摘要返回为空，退回启发式摘要：%s", filename)
            return None
        except Exception as exc:  # 摘要失败绝不阻塞入库
            logger.warning("LLM 文档摘要生成失败，退回启发式摘要（%s）：%s", filename, exc)
            return None

    # -- 启发式摘要（无 LLM 时的确定性回退） ----------------------------------
    # 设计约束：HashEmbedder 词袋口径下每个字都是向量质量——摘要必须高密度、
    # 零样板词（"本文档主题/开篇要点"类措辞是纯噪声，2026-09-05 评测实测致
    # recall@5 回退 0.952→0.905，故收敛为标题+大纲关键词短形式）

    def _heuristic(self, document: ParsedDocument, filename: str) -> str:
        title = self._title(document, filename)
        outline = self._outline(document, exclude={title}, cap=6)
        if not outline:
            return title
        return f"{title}（涵盖：{outline}）"

    # -- 公共取材 ------------------------------------------------------------

    def _outline(self, document: ParsedDocument, exclude: set[str] | None = None, cap: int = 8) -> str:
        """标题大纲：前 cap 个 level≤3 的标题去重串接，可排除已用作标题的词。"""
        exclude = exclude or set()
        titles: list[str] = []
        for block in document.blocks:
            if block.type != "heading" or (block.level or 2) > 3:
                continue
            text = block.text.strip()
            if text and text not in titles and text not in exclude:
                titles.append(text)
            if len(titles) >= cap:
                break
        return "、".join(titles)

    def _head_text(self, document: ParsedDocument, max_chars: int = _OUTLINE_HEAD_LIMIT) -> str:
        """正文开头：跳过标题块，拼接到字符上限。"""
        buf = ""
        for block in document.blocks:
            if block.type == "heading":
                continue
            buf = f"{buf} {render_block(block).strip()}".strip()
            if len(buf) >= max_chars:
                break
        return buf[:max_chars]


def build_doc_context_builder(config: Settings) -> DocContextBuilder | None:
    """按配置装配摘要构建器：功能关闭返回 None；LLM 就绪则接入，否则纯启发式。"""
    if not config.contextual_chunking:
        return None
    builder = DocContextBuilder(max_chars=config.contextual_max_chars)
    if config.llm_api_key and config.llm_base_url and config.llm_model:
        from openai import OpenAI

        builder.attach_llm(
            OpenAI(base_url=config.llm_base_url, api_key=config.llm_api_key),
            config.llm_model,
        )
        logger.info("Contextual Chunking：LLM 文档摘要已启用（%s）", config.llm_model)
    else:
        logger.info("Contextual Chunking：未配置 LLM，使用启发式文档摘要")
    return builder
