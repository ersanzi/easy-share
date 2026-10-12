"""会议总结：转写全文 → LLM 生成 {minutes, key_points, todos}。

复用与 /query 同一套 LLM 配置（OpenAI 兼容）；未配置 LLM 或生成失败时返回 None，
听记仍是完整转写——总结是增值层，不是可用性前提。
"""
from __future__ import annotations

import json
import logging
import re

logger = logging.getLogger(__name__)

# 超过该字符数先分段小结再汇总（map-reduce），保护长会议的上下文
_REDUCE_THRESHOLD = 45000
_REDUCE_CHUNK = 30000

def _template_prompt(template: dict | None) -> str:
    """按模板生成 sections 结构指令；默认模板走旧版固定字段（纪要/要点），避免回归。"""
    if not template or template.get("id") == "meeting-minutes":
        return (
            '{"minutes": "400字以内的会议纪要，按议题归纳，保留数据与结论", '
            '"key_points": ["讨论要点，每条一句话，保留数据/数字/期限"], '
            '"todos": [...], "mindmap": "..."}'
        )
    return (
        '{"sections": [{"title": "小节标题", "content": "本节内容：要么是连贯段落，要么是多行以 `- ` 开头的列表"}], '
        '"todos": [...], "mindmap": "..."}\n'
        f"sections 按以下结构组织（小节标题与顺序遵循）：{template.get('sections')}"
    )


_SYSTEM_PROMPT_HEAD = (
    "你是企业会议纪要助手。根据带说话人标签的会议转写，输出严格的 JSON 对象。JSON 形状为：\n"
)

_SYSTEM_PROMPT_TAIL = (
    '\ntodos 形如 [{"task": "待办事项", "owner_hint": "负责人或空字符串", "due_hint": "期限或空字符串"}]，'
    "任何模板都必须提取待办。\n"
    'mindmap 是会议结构的 markdown 层级大纲：第一行 "# 会议主题"；二级 "## 议题/模块"'
    "（按会议实际议题划分）；三级 \"- 要点\"；四级 \"  - 补充细节\"（可选）。要求：\n"
    "1. 总节点 15-40 个，宁可信息完整也不要干瘪；\n"
    "2. 要点保留具体数据（数量/版本/期限/负责人），负责人用转写中的说话人名；\n"
    "3. 结论与待办单独成支；\n"
    "4. 只依据转写内容，不编造。"
)


def _system_prompt(template: dict | None) -> str:
    return _SYSTEM_PROMPT_HEAD + _template_prompt(template) + _SYSTEM_PROMPT_TAIL



def _extract_last_json(text: str) -> dict | None:
    """从混有思考文本的 LLM 输出中提取最后一个可解析的 JSON 对象。

    推理型模型（如 deepseek-flash 经中转）会在 JSON 前泄漏思维链，贪心正则可能
    抓到错误片段；这里逐 "{" 位置尝试 raw_decode，取最后一个完整对象。
    """
    decoder = json.JSONDecoder()
    last: dict | None = None
    position = text.find("{")
    while position != -1:
        try:
            value, _ = decoder.raw_decode(text[position:])
            if isinstance(value, dict):
                last = value
        except json.JSONDecodeError:
            pass
        position = text.find("{", position + 1)
    return last

class MeetingSummarizer:
    def __init__(self, base_url: str, api_key: str, model: str, trust_env: bool = True) -> None:
        self._base_url = base_url
        self._api_key = api_key
        self._model = model
        self._trust_env = trust_env

    def _client(self):
        from openai import OpenAI

        kwargs: dict = {}
        if not self._trust_env:
            # 直连（不走系统代理）：内网/中转端点走代理常 TLS 握手超时
            import httpx

            kwargs["http_client"] = httpx.Client(trust_env=False)
        return OpenAI(base_url=self._base_url, api_key=self._api_key, **kwargs)

    @property
    def configured(self) -> bool:
        return bool(self._base_url and self._api_key and self._model)

    def ask_json(self, system: str, user: str) -> dict | None:
        """通用 LLM JSON 询问器（供导图 harness 等多阶段管线复用）。失败返回 None。"""
        if not self.configured:
            return None
        try:
            client = self._client()
            response = client.chat.completions.create(
                model=self._model,
                messages=[
                    {"role": "system", "content": system},
                    {"role": "user", "content": user},
                ],
                temperature=0.2,
                # 推理型模型答案前大量思考，默认补全上限会被中途截断
                max_tokens=8192,
            )
            raw = (response.choices[0].message.content or "").strip()
        except Exception:  # noqa: BLE001
            logger.warning("LLM 调用失败", exc_info=True)
            return None
        if raw.startswith("```"):
            raw = re.sub(r"^```[a-zA-Z]*\n?", "", raw).rstrip("`").strip()
        try:
            return json.loads(raw)
        except json.JSONDecodeError:
            return _extract_last_json(raw)

    def ask_text(self, system: str, messages: list[dict]) -> str | None:
        """多轮纯文本问答（听记 AI 问答用）。messages 含历史与最新提问。失败返回 None。"""
        if not self.configured:
            return None
        try:
            client = self._client()
            response = client.chat.completions.create(
                model=self._model,
                messages=[{"role": "system", "content": system}, *messages],
                temperature=0.3,
                max_tokens=4096,
            )
            return (response.choices[0].message.content or "").strip()
        except Exception:  # noqa: BLE001
            logger.warning("听记问答 LLM 调用失败", exc_info=True)
            return None

    def summarize(self, transcript: str, template: dict | None = None) -> dict | None:
        if not self.configured:
            return None
        try:
            return self._summarize_guarded(transcript, template)
        except Exception:  # noqa: BLE001 - 总结失败不影响转写可用
            logger.warning("会议总结生成失败", exc_info=True)
            return None

    def _summarize_guarded(self, transcript: str, template: dict | None = None) -> dict | None:
        client = self._client()
        if len(transcript) > _REDUCE_THRESHOLD:
            partials: list[str] = []
            for start in range(0, len(transcript), _REDUCE_CHUNK):
                piece = transcript[start : start + _REDUCE_CHUNK]
                partials.append(self._ask(client, "以下是长会议转写的一段，输出该段的要点小结（纯文本，300字内）：\n" + piece))
            material = "各段小结：\n" + "\n".join(partials)
        else:
            material = transcript
        raw = self._ask(client, material, template)
        return self._parse(raw, template)

    def _ask(self, client, material: str, template: dict | None = None) -> str:
        response = client.chat.completions.create(
            model=self._model,
            messages=[
                {"role": "system", "content": _system_prompt(template)},
                {"role": "user", "content": "会议转写如下：\n" + material},
            ],
            temperature=0.2,
            # 推理型模型答案前大量思考，默认补全上限会被中途截断
            max_tokens=8192,
        )
        return response.choices[0].message.content or ""

    @staticmethod
    def _parse(raw: str, template: dict | None = None) -> dict | None:
        text = raw.strip()
        # 防御性解析：剥掉可能的 markdown 代码栅栏；解析失败回退把全文当纪要
        if text.startswith("```"):
            text = re.sub(r"^```[a-zA-Z]*\n?", "", text).rstrip("`").strip()
        try:
            parsed = json.loads(text)
        except json.JSONDecodeError:
            parsed = _extract_last_json(raw)
            if parsed is None:
                return {"minutes": raw.strip(), "key_points": [], "todos": []} if raw.strip() else None
        todos = [
            {
                "task": str(item.get("task") or ""),
                "owner_hint": str(item.get("owner_hint") or ""),
                "due_hint": str(item.get("due_hint") or ""),
            }
            for item in parsed.get("todos") or []
            if isinstance(item, dict) and item.get("task")
        ]
        result: dict = {"todos": todos, "mindmap": str(parsed.get("mindmap") or "")}
        sections = parsed.get("sections")
        is_default = not template or template.get("id") == "meeting-minutes"
        if isinstance(sections, list) and sections:
            # 模板 sections 结构（title + 段落或列表）
            result["sections"] = [
                {
                    "title": str(section.get("title") or "").strip(),
                    "text": str(section.get("content") or "").strip(),
                }
                for section in sections
                if isinstance(section, dict) and str(section.get("title") or "").strip()
            ]
        elif not is_default and (parsed.get("minutes") or parsed.get("key_points")):
            # 弱模型不守 sections 形状时兜底：把旧形状转换成 sections，保证模板结构可见
            sections_backfill = []
            if parsed.get("minutes"):
                sections_backfill.append({"title": "总结", "text": str(parsed.get("minutes"))})
            if parsed.get("key_points"):
                sections_backfill.append({
                    "title": "要点",
                    "content": chr(10).join("- " + str(point) for point in parsed["key_points"]),
                })
            result["sections"] = sections_backfill
        if is_default:
            # 默认模板：兼容旧固定字段（纪要/要点）
            result["minutes"] = str(parsed.get("minutes") or "")
            result["key_points"] = [str(item) for item in parsed.get("key_points") or []]
        return result
