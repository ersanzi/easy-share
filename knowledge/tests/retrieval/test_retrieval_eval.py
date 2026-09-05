"""检索质量基线：HashEmbedder 确定性评测，防止切块/索引/检索链路回归。

指标阈值按当前基线校准（见迭代记录）。若有意改动切块或检索策略导致指标
变化，先跑 scripts/eval_retrieval.py 对比新旧报告，再同步更新阈值与记录。

2026-07-29 结构感知切块基线：recall@5=0.933 hit@1=0.900 mrr=0.917 snippet=0.900
标题层级上下文前缀对 HashEmbedder 词袋模型引入噪声词，但真实 embedding 下
为增益（提供主题语境）。阈值按新基线下浮校准，真实语义质量用 --real 验证。

2026-09-05 Contextual Chunking 基线（启发式摘要，4096 维）：
关闭=0.952/0.857/0.894/0.905 开启=0.952/0.857/0.892/0.905（recall@5/hit@1/mrr/snippet）
差异为单个 trigram 哈希碰撞事件的量级（misses 集合不变）；真实 embedding
口径双向 1.000（评测集已饱和）。哈希短文本碰撞噪声分析见
docs/iterations/2026-09-05-contextual-chunking.md。
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
    assert report["recall_at_k"] >= 0.90, summary
    assert report["hit_at_1"] >= 0.85, summary
    assert report["mrr"] >= 0.88, summary
    assert report["snippet_hit_rate"] >= 0.85, summary


def test_doc_scope_never_returns_out_of_scope_documents(eval_setup) -> None:
    records = eval_setup.retriever.retrieve(
        "住宿报销的上限标准是多少？", top_k=5, doc_ids=["vpn-remote"]
    )
    assert records, "限定范围内应返回结果"
    assert {record["file_id"] for record in records} == {"vpn-remote"}
