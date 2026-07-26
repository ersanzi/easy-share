"""检索质量基线：HashEmbedder 确定性评测，防止切块/索引/检索链路回归。

指标阈值按当前基线校准（见迭代记录）。若有意改动切块或检索策略导致指标
变化，先跑 scripts/eval_retrieval.py 对比新旧报告，再同步更新阈值与记录。
"""
from __future__ import annotations

import pytest

from app.eval.retrieval import evaluate_retrieval
from tests.retrieval.corpus import build_eval_setup, index_corpus, load_cases, load_corpus


@pytest.fixture(scope="module")
def eval_setup(tmp_path_factory):
    setup = build_eval_setup(tmp_path_factory.mktemp("retrieval-eval"))
    index_corpus(setup)
    return setup


def test_cases_reference_existing_corpus_documents() -> None:
    corpus_ids = {document["file_id"] for document in load_corpus()}
    cases = load_cases()
    assert len(cases) >= 30, "评测集应保持至少 30 条标注"
    for case in cases:
        assert case.expected_file_id in corpus_ids, f"{case.id} 指向不存在的文档"
        for doc_id in case.doc_ids or []:
            assert doc_id in corpus_ids, f"{case.id} 的权限范围含不存在的文档"


def test_retrieval_quality_baseline(eval_setup) -> None:
    report = evaluate_retrieval(eval_setup.retriever, load_cases(), top_k=5)
    detail = "\n".join(
        f"  {miss['case_id']}: rank={miss['hit_rank']} snippet_hit={miss['snippet_hit']} "
        f"retrieved={miss['retrieved_file_ids']}"
        for miss in report["misses"]
    )
    summary = (
        f"recall@5={report['recall_at_k']:.3f} hit@1={report['hit_at_1']:.3f} "
        f"mrr={report['mrr']:.3f} snippet={report['snippet_hit_rate']:.3f}\n未达标用例:\n{detail}"
    )
    assert report["recall_at_k"] >= 0.95, summary
    assert report["hit_at_1"] >= 0.80, summary
    assert report["mrr"] >= 0.85, summary
    assert report["snippet_hit_rate"] >= 0.95, summary


def test_doc_scope_never_returns_out_of_scope_documents(eval_setup) -> None:
    records = eval_setup.retriever.retrieve(
        "住宿报销的上限标准是多少？", top_k=5, doc_ids=["vpn-remote"]
    )
    assert records, "限定范围内应返回结果"
    assert {record["file_id"] for record in records} == {"vpn-remote"}
