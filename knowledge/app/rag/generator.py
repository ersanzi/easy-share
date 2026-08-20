"""生成：把检索到的片段拼进 prompt，调云端 LLM（OpenAI 兼容）生成可溯源的回答。"""
import logging

from app.config import Settings, settings

logger = logging.getLogger(__name__)

SYSTEM_PROMPT = (
    "你是企业知识库助手。仅根据下面提供的「参考资料」回答用户问题。"
    "如果参考资料中没有相关内容，直接说明无法从现有资料中找到答案，不要编造。"
    "回答末尾用 [n] 标注引用了哪几条资料。"
    "参考资料中标注了每份文档的入库时间（文档时间）：当不同时间的文档内容冲突时，"
    "优先依据较新的文档作答；若所依据的文档可能已被更新版本取代，"
    "须在回答中明确提示该内容的时效，例如「此内容来自 X 日期的文档，可能已更新」。"
)


def context_ingested_at(context: dict) -> str | None:
    """从检索结果中取入库时间；旧数据无该字段时返回 None。"""
    metadata = context.get("metadata") or {}
    return metadata.get("ingested_at")


def build_reference_block(contexts: list[dict]) -> str:
    """拼接参考资料块；带入库时间的片段标注文档时间，供模型判断新旧。"""
    parts = []
    for index, context in enumerate(contexts):
        source = f"来源: {context.get('doc_id', '')}"
        ingested_at = context_ingested_at(context)
        if ingested_at:
            source += f", 文档时间: {ingested_at}"
        parts.append(f"[{index + 1}] ({source}) {context['text']}")
    return "\n\n".join(parts)


def build_messages(question: str, contexts: list[dict]) -> list[dict]:
    ref_block = build_reference_block(contexts)
    return [
        {"role": "system", "content": SYSTEM_PROMPT},
        {"role": "user", "content": f"参考资料：\n{ref_block}\n\n问题：{question}"},
    ]


class Generator:
    def __init__(self, config: Settings = settings) -> None:
        from openai import OpenAI

        self.model = config.llm_model
        self.client = OpenAI(base_url=config.llm_base_url, api_key=config.llm_api_key)

    def generate(self, question: str, contexts: list[dict]) -> dict:
        messages = build_messages(question, contexts)
        resp = self.client.chat.completions.create(model=self.model, messages=messages)
        answer = resp.choices[0].message.content or ""
        sources = [
            {
                "doc_id": c.get("doc_id"),
                "score": c.get("score"),
                "ingested_at": context_ingested_at(c),
            }
            for c in contexts
        ]
        return {"answer": answer, "sources": sources}


def build_generator(config: Settings = settings) -> Generator | None:
    if config.llm_api_key and config.llm_base_url and config.llm_model:
        logger.info("使用 LLM: %s", settings.llm_model)
        return Generator()
    logger.warning("未配置 LLM，/query 将只返回检索片段不做生成")
    return None
