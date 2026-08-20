from __future__ import annotations

from app.config import Settings
from app.rag.multi_hop import (
    LLMHopJudge,
    MultiHopRetriever,
    apply_context_budget,
    build_multi_hop_retriever,
)


def _record(rid: str, text: str, score: float, file_id: str = "f1") -> dict:
    return {"id": rid, "text": text, "score": score, "file_id": file_id, "version_id": "v1", "metadata": {"filename": f"{rid}.md"}}


class FakeRetriever:
    def __init__(self, by_query: dict) -> None:
        self.by_query = by_query
        self.calls: list[str] = []

    def retrieve(self, query: str, top_k: int = 5, doc_ids: list[str] | None = None) -> list[dict]:
        self.calls.append(query)
        return [dict(record) for record in self.by_query.get(query, [])]


class FakeBM25:
    def __init__(self, by_query: dict) -> None:
        self.by_query = by_query

    def query(self, query: str, top_k: int, doc_ids: list[str] | None = None) -> list[dict]:
        return [dict(record) for record in self.by_query.get(query, [])]


class ScriptedJudge:
    """按脚本顺序返回裁判结论。"""

    def __init__(self, verdicts: list[dict]) -> None:
        self.verdicts = list(verdicts)
        self.calls = 0

    def judge(self, question: str, contexts: list[dict]) -> dict:
        self.calls += 1
        return self.verdicts.pop(0)


class FakeQueryLog:
    def __init__(self) -> None:
        self.entries: list[dict] = []

    def log_retrieval(self, **kwargs) -> None:
        self.entries.append(kwargs)


def _multi_hop(retriever, bm25, judge, log=None, **overrides) -> MultiHopRetriever:
    params = dict(max_hops=3, hop_top_k=5, max_contexts=10, max_chars=12000)
    params.update(overrides)
    return MultiHopRetriever(retriever=retriever, bm25=bm25, judge=judge, query_log=log, **params)


# ---------- 预算控制 ----------


def test_context_budget_caps_count_and_chars() -> None:
    records = [
        _record("a", "x" * 100, 0.9),
        _record("b", "y" * 100, 0.8),
        _record("c", "z" * 100, 0.7),
    ]
    assert [r["id"] for r in apply_context_budget(records, max_contexts=2, max_chars=10000)] == ["a", "b"]
    assert [r["id"] for r in apply_context_budget(records, max_contexts=10, max_chars=150)] == ["a"]


# ---------- 裁判输出解析 ----------


def test_parse_verdict_json_variants() -> None:
    assert LLMHopJudge.parse_verdict('{"sufficient": false, "next_query": "旧版报销标准"}') == {
        "sufficient": False, "next_query": "旧版报销标准",
    }
    assert LLMHopJudge.parse_verdict('```json\n{"sufficient": true, "next_query": null}\n```') == {
        "sufficient": True, "next_query": None,
    }
    # 无法解析时按已充分收敛，避免死循环
    assert LLMHopJudge.parse_verdict("我认为资料已经足够了。") == {"sufficient": True, "next_query": None}


# ---------- 多跳主流程 ----------


def test_two_hops_with_follow_up_query() -> None:
    retriever = FakeRetriever({
        "新旧报销制度差异": [_record("c1", "新版制度内容", 0.9)],
        "旧版报销标准": [_record("c2", "旧版制度内容", 0.8)],
    })
    bm25 = FakeBM25({
        "新旧报销制度差异": [],
        "旧版报销标准": [],
    })
    judge = ScriptedJudge([
        {"sufficient": False, "next_query": "旧版报销标准"},
        {"sufficient": True, "next_query": None},
    ])
    result = _multi_hop(retriever, bm25, judge).retrieve("新旧报销制度差异")

    assert [hop.query for hop in result.hops] == ["新旧报销制度差异", "旧版报销标准"]
    assert result.hops[0].converged is False
    assert result.hops[1].converged is True
    assert {record["id"] for record in result.contexts} == {"c1", "c2"}
    assert judge.calls == 2  # 第 2 跳裁判判定充分后收敛（仅达跳数上限时才跳过裁判）


def test_immediately_sufficient_runs_single_hop() -> None:
    retriever = FakeRetriever({"问题": [_record("c1", "内容", 0.9)]})
    judge = ScriptedJudge([{"sufficient": True, "next_query": None}])
    result = _multi_hop(retriever, FakeBM25({}), judge).retrieve("问题")

    assert len(result.hops) == 1
    assert result.hops[0].converged is True


def test_max_hops_budget_stops_loop() -> None:
    retriever = FakeRetriever({"问题": [_record("c1", "内容", 0.9)]})
    judge = ScriptedJudge([{"sufficient": False, "next_query": "追问"} for _ in range(5)])
    result = _multi_hop(retriever, FakeBM25({}), judge, max_hops=2).retrieve("问题")

    assert len(result.hops) == 2
    assert result.hops[-1].converged is True  # 跳数上限兜底
    assert retriever.calls == ["问题", "追问"]


def test_duplicate_chunks_dedup_across_hops() -> None:
    retriever = FakeRetriever({
        "第一问": [_record("c1", "共享内容", 0.9)],
        "第二问": [_record("c1", "共享内容", 0.9), _record("c2", "新内容", 0.7)],
    })
    judge = ScriptedJudge([
        {"sufficient": False, "next_query": "第二问"},
        {"sufficient": True, "next_query": None},
    ])
    result = _multi_hop(retriever, FakeBM25({}), judge).retrieve("第一问")

    assert result.total_candidates == 2
    assert sum(1 for record in result.contexts if record["id"] == "c1") == 1


def test_hop_logging_records_strategy_names() -> None:
    retriever = FakeRetriever({
        "问题": [_record("c1", "内容", 0.9)],
        "追问": [_record("c2", "内容2", 0.8)],
    })
    log = FakeQueryLog()
    judge = ScriptedJudge([
        {"sufficient": False, "next_query": "追问"},
        {"sufficient": True, "next_query": None},
    ])
    _multi_hop(retriever, FakeBM25({}), judge, log=log).retrieve("问题")

    assert [entry["strategy"] for entry in log.entries] == ["multi_hop:hop1", "multi_hop:hop2"]
    assert log.entries[0]["question"] == "问题"
    assert log.entries[1]["question"] == "追问"


# ---------- 构建降级 ----------


def test_build_multi_hop_requires_llm_config() -> None:
    config = Settings(_env_file=None, llm_api_key="", llm_base_url="", llm_model="")
    assert build_multi_hop_retriever(config, retriever=None, bm25=None) is None
