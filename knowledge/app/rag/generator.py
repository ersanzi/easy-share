"""生成：把检索到的片段拼进 prompt，调云端 LLM（OpenAI 兼容）生成可溯源的回答。"""
import logging

from app.config import Settings, settings

logger = logging.getLogger(__name__)

SYSTEM_PROMPT = (
    "你是企业知识库助手。仅根据下面提供的「参考资料」回答用户问题。"
    "如果参考资料中没有相关内容，直接说明无法从现有资料中找到答案，不要编造。"
    "回答末尾用 [n] 标注引用了哪几条资料。"
)


class Generator:
    def __init__(self, config: Settings = settings) -> None:
        from openai import OpenAI

        self.model = config.llm_model
        self.client = OpenAI(base_url=config.llm_base_url, api_key=config.llm_api_key)

    def generate(self, question: str, contexts: list[dict]) -> dict:
        ref_block = "\n\n".join(
            f"[{i + 1}] (来源: {c.get('doc_id', '')}) {c['text']}" for i, c in enumerate(contexts)
        )
        messages = [
            {"role": "system", "content": SYSTEM_PROMPT},
            {"role": "user", "content": f"参考资料：\n{ref_block}\n\n问题：{question}"},
        ]
        resp = self.client.chat.completions.create(model=self.model, messages=messages)
        answer = resp.choices[0].message.content or ""
        sources = [{"doc_id": c.get("doc_id"), "score": c.get("score")} for c in contexts]
        return {"answer": answer, "sources": sources}


def build_generator(config: Settings = settings) -> Generator | None:
    if config.llm_api_key and config.llm_base_url and config.llm_model:
        logger.info("使用 LLM: %s", settings.llm_model)
        return Generator()
    logger.warning("未配置 LLM，/query 将只返回检索片段不做生成")
    return None
