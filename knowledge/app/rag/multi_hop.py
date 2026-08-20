"""Agent 多跳检索：LLM 判断上下文充分性，不充分则生成补充查询再检索。

预算控制（TDB-AM 对标 P0）：跳数、总上下文条数、总字符三上限，
防止多跳结果雪崩式膨胀；chunk 按唯一 id 去重合并（保留最高分）。
每跳调用 QueryLog 记录（strategy 形如 multi_hop:hop1），可归因分析。
"""
from __future__ import annotations

import json
import logging
import re
from dataclasses import dataclass, field
from typing import Any, Protocol

from app.kb.bm25 import BM25Retriever
from app.kb.fusion import hybrid_fusion

logger = logging.getLogger(__name__)

JUDGE_SYSTEM_PROMPT = (
    "你是检索充分性评估器。根据用户问题与已收集的资料片段，判断资料是否足以回答问题。"
    '只输出 JSON：{"sufficient": true/false, "next_query": "补充检索查询或 null"}。'
    "next_query 针对当前资料缺失的侧面提出一个具体的检索查询；资料已充分时 next_query 为 null。"
    "不要重复已收集的信息。"
)


class HopJudge(Protocol):
    """充分性裁判：输入问题与已收集上下文，输出是否继续与下一跳查询。"""

    def judge(self, question: str, contexts: list[dict]) -> dict[str, Any]: ...


@dataclass(slots=True)
class HopRecord:
    hop: int  # 从 1 起
    query: str
    result_count: int
    top_score: float | None = None
    converged: bool = False  # 该跳后判定充分（或达到跳数上限）

    def to_dict(self) -> dict[str, Any]:
        return {
            "hop": self.hop,
            "query": self.query,
            "result_count": self.result_count,
            "top_score": self.top_score,
            "converged": self.converged,
        }


@dataclass(slots=True)
class MultiHopResult:
    contexts: list[dict] = field(default_factory=list)  # 预算内的最终上下文（按分数降序）
    hops: list[HopRecord] = field(default_factory=list)
    total_candidates: int = 0  # 预算截断前的去重候选总数


class LLMHopJudge:
    """用 OpenAI 兼容 LLM 做充分性裁判；解析失败按"已充分"处理，避免死循环。"""

    def __init__(self, client, model: str, *, judge_char_budget: int = 4000) -> None:
        self.client = client
        self.model = model
        self.judge_char_budget = judge_char_budget

    def judge(self, question: str, contexts: list[dict]) -> dict[str, Any]:
        snippet_parts: list[str] = []
        used = 0
        for index, context in enumerate(contexts, start=1):
            text = context.get("text", "")
            part = f"[{index}] {text}"
            if used + len(part) > self.judge_char_budget:
                break
            snippet_parts.append(part)
            used += len(part)
        messages = [
            {"role": "system", "content": JUDGE_SYSTEM_PROMPT},
            {
                "role": "user",
                "content": f"问题：{question}\n\n已收集资料：\n{chr(10).join(snippet_parts) or '（空）'}",
            },
        ]
        resp = self.client.chat.completions.create(model=self.model, messages=messages)
        raw = (resp.choices[0].message.content or "").strip()
        return self.parse_verdict(raw)

    @staticmethod
    def parse_verdict(raw: str) -> dict[str, Any]:
        match = re.search(r"\{.*\}", raw, re.DOTALL)
        if match:
            try:
                payload = json.loads(match.group(0))
                sufficient = bool(payload.get("sufficient"))
                next_query = payload.get("next_query")
                if not sufficient and isinstance(next_query, str) and next_query.strip():
                    return {"sufficient": False, "next_query": next_query.strip()}
                return {"sufficient": True, "next_query": None}
            except (ValueError, TypeError):
                pass
        logger.warning("多跳裁判输出无法解析，按已充分收敛：%s", raw[:120])
        return {"sufficient": True, "next_query": None}


def apply_context_budget(
    records: list[dict],
    *,
    max_contexts: int,
    max_chars: int,
) -> list[dict]:
    """按分数降序截断：先限条数，再限总字符（逐条累加，超预算即止）。"""
    ordered = sorted(records, key=lambda r: r.get("score", 0.0), reverse=True)[:max_contexts]
    results: list[dict] = []
    used = 0
    for record in ordered:
        length = len(record.get("text", ""))
        if results and used + length > max_chars:
            break
        results.append(record)
        used += length
    return results


class MultiHopRetriever:
    """分轮混合检索 + LLM 充分性裁判 + 预算控制。"""

    def __init__(
        self,
        *,
        retriever,
        bm25: BM25Retriever,
        judge: HopJudge,
        query_log=None,
        max_hops: int = 3,
        hop_top_k: int = 5,
        max_contexts: int = 10,
        max_chars: int = 12000,
    ) -> None:
        self.retriever = retriever
        self.bm25 = bm25
        self.judge = judge
        self.query_log = query_log
        self.max_hops = max(1, max_hops)
        self.hop_top_k = hop_top_k
        self.max_contexts = max_contexts
        self.max_chars = max_chars

    def retrieve(self, question: str, doc_ids: list[str] | None = None) -> MultiHopResult:
        collected: dict[str, dict] = {}
        hops: list[HopRecord] = []
        current_query = question

        for hop in range(1, self.max_hops + 1):
            vector_results = self.retriever.retrieve(current_query, top_k=self.hop_top_k, doc_ids=doc_ids)
            bm25_results = self.bm25.query(current_query, self.hop_top_k, doc_ids)
            fused = hybrid_fusion(vector_results, bm25_results, self.hop_top_k)

            for record in fused:
                rid = record.get("id", "")
                if rid not in collected or record.get("score", 0.0) > collected[rid].get("score", 0.0):
                    collected[rid] = record

            top_score = fused[0].get("score") if fused else None
            hops.append(HopRecord(hop=hop, query=current_query, result_count=len(fused), top_score=top_score))
            self._log_hop(hop, current_query, fused)

            if hop == self.max_hops:
                hops[-1].converged = True  # 达到跳数上限，视作收敛（预算兜底）
                break
            verdict = self.judge.judge(question, list(collected.values()))
            if verdict.get("sufficient") or not verdict.get("next_query"):
                hops[-1].converged = True
                break
            current_query = verdict["next_query"]

        contexts = apply_context_budget(
            list(collected.values()),
            max_contexts=self.max_contexts,
            max_chars=self.max_chars,
        )
        return MultiHopResult(contexts=contexts, hops=hops, total_candidates=len(collected))

    def _log_hop(self, hop: int, query: str, fused: list[dict]) -> None:
        if self.query_log is None:
            return
        try:
            self.query_log.log_retrieval(
                question=query,
                strategy=f"multi_hop:hop{hop}",
                top_k=self.hop_top_k,
                result_count=len(fused),
                top_score=fused[0].get("score", 0.0) if fused else 0.0,
                file_ids_hit=[r.get("file_id", "") for r in fused],
            )
        except Exception:  # 日志失败不影响检索主流程
            logger.warning("多跳 hop 日志写入失败", exc_info=True)


def build_multi_hop_retriever(config, retriever, bm25, query_log=None):
    """LLM 未配置时返回 None，调用方按"策略不可用"降级展示。"""
    if not (config.llm_api_key and config.llm_base_url and config.llm_model):
        return None
    from openai import OpenAI

    client = OpenAI(base_url=config.llm_base_url, api_key=config.llm_api_key)
    judge = LLMHopJudge(client, config.llm_model)
    return MultiHopRetriever(
        retriever=retriever,
        bm25=bm25,
        judge=judge,
        query_log=query_log,
        max_hops=config.multi_hop_max_hops,
        hop_top_k=config.multi_hop_hop_top_k,
        max_contexts=config.multi_hop_max_contexts,
        max_chars=config.multi_hop_max_chars,
    )
