"""从 derived/ 产物全量重建向量索引。

两个模式（OKF 架构原则 1：「derived/ 是唯一知识源，向量索引只是可重建缓存」）：

默认 · 重建验证：黄金语料双路对比（完整管线直建 vs 只读 derived 重建，不碰
源文档、不重跑解析清洗、不调 LLM），同一评测集打分断言四项检索指标逐项
持平——证明「索引坏了可以只从 derived 完整重建且质量无损失」。

--apply · 生产替换：把生产 JSON 向量索引（.env 的 VECTOR_STORE_PATH）从
derived 产物全量重建并原子替换，旧索引自动备份为 .bak-<时间戳>；任一文档
重建失败即整体中止、生产索引不动。**必须停服务后执行**（运行中服务的内存
索引会在下次入库写回时覆盖重建结果），完成后重启服务。先跑不带 --yes 的
--apply 看预览（文档清单 / embedder / 目标路径），确认再加 --yes。

用法（在 knowledge/ 目录下运行，读 .env）：
    python scripts/rebuild_from_derived.py               # 重建验证（哈希口径）
    python scripts/rebuild_from_derived.py --output r.json
    python scripts/rebuild_from_derived.py --apply       # 生产替换预览
    python scripts/rebuild_from_derived.py --apply --yes # 真正执行替换
"""
from __future__ import annotations

import argparse
import json
import sys
import tempfile
from pathlib import Path

KNOWLEDGE_ROOT = Path(__file__).resolve().parents[1]
if str(KNOWLEDGE_ROOT) not in sys.path:
    sys.path.insert(0, str(KNOWLEDGE_ROOT))

from app.config import Settings  # noqa: E402
from app.eval.retrieval import evaluate_retrieval  # noqa: E402
from app.kb.rebuild import apply_rebuild, plan_rebuild, rebuild_doc_items  # noqa: E402
from app.rag.retriever import Retriever  # noqa: E402
from app.kb.store import VectorStore  # noqa: E402
from tests.retrieval.corpus import (  # noqa: E402
    EVAL_CHUNK_OVERLAP,
    EVAL_CHUNK_SIZE,
    build_eval_setup,
    index_corpus,
    load_cases,
    load_corpus,
)

# 与基线逐项比对的四项指标键（app/eval/retrieval.py 报告字段）
METRIC_KEYS = ("recall_at_k", "hit_at_1", "mrr", "snippet_hit_rate")


def run_verify(args: argparse.Namespace) -> int:
    """黄金语料双路对比：管线直建基线 vs derived 重建，断言指标不回退。"""
    with tempfile.TemporaryDirectory(prefix="easyshare-rebuild-verify-") as work_dir:
        # 1. 完整入库管线：基线索引 + derived 产物
        setup = build_eval_setup(
            Path(work_dir),
            chunk_size=args.chunk_size,
            chunk_overlap=args.chunk_overlap,
        )
        file_ids = index_corpus(setup)
        if not file_ids:
            print("语料为空，无文档可重建", file=sys.stderr)
            return 2
        baseline = evaluate_retrieval(setup.retriever, load_cases(), top_k=args.top_k)

        # 2. 只读 derived 产物重建到独立的新索引
        rebuilt_store = VectorStore(str(Path(work_dir) / "vectors-rebuilt.json"))
        rebuilt_docs = 0
        rebuilt_chunks = 0
        for document in load_corpus():
            file_id = document["file_id"]
            items = rebuild_doc_items(
                setup.storage,
                file_id,
                "v1",
                setup.embedder,
                args.chunk_size,
                args.chunk_overlap,
            )
            rebuilt_store.replace_doc(file_id, items)
            rebuilt_docs += 1
            rebuilt_chunks += len(items)
        rebuilt = evaluate_retrieval(
            Retriever(setup.embedder, rebuilt_store), load_cases(), top_k=args.top_k
        )

    # 3. 指标对比：确定性口径下应逐项持平
    diffs = {
        key: (baseline[key], rebuilt[key]) for key in METRIC_KEYS if baseline[key] != rebuilt[key]
    }
    print(f"语料文档: {len(file_ids)}  重建文档: {rebuilt_docs}  重建块数: {rebuilt_chunks}")
    print(f"基线   recall@{args.top_k}: {baseline['recall_at_k']:.3f}  hit@1: {baseline['hit_at_1']:.3f}  mrr: {baseline['mrr']:.3f}  snippet: {baseline['snippet_hit_rate']:.3f}")
    print(f"重建后 recall@{args.top_k}: {rebuilt['recall_at_k']:.3f}  hit@1: {rebuilt['hit_at_1']:.3f}  mrr: {rebuilt['mrr']:.3f}  snippet: {rebuilt['snippet_hit_rate']:.3f}")
    if diffs:
        print("指标回退，重建验证失败：", file=sys.stderr)
        for key, (before, after) in diffs.items():
            print(f"  {key}: {before:.3f} -> {after:.3f}", file=sys.stderr)
        return 1
    print("重建验证通过：四指标与管线直建基线逐项持平")

    if args.output:
        report = {
            "verified": True,
            "documents": rebuilt_docs,
            "chunks": rebuilt_chunks,
            "chunk_size": args.chunk_size,
            "chunk_overlap": args.chunk_overlap,
            "top_k": args.top_k,
            "baseline": {key: baseline[key] for key in METRIC_KEYS},
            "rebuilt": {key: rebuilt[key] for key in METRIC_KEYS},
        }
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
        print(f"对比报告已写入: {args.output}")
    return 0


def run_apply(args: argparse.Namespace) -> int:
    """生产索引替换：--yes 前为只读预览，--yes 后重建并原子替换。"""
    config = Settings()  # 读 knowledge/.env（须在 knowledge/ 目录运行）
    try:
        if not args.yes:
            plan = plan_rebuild(config)
            print(f"目标索引: {plan['store']}（{'已存在，将备份后替换' if plan['store_exists'] else '不存在，将新建'}）")
            print(f"Embedder: {plan['embedder']}（与线上查询同一配置工厂）")
            print(f"待重建文档: {plan['documents']}")
            for file_id, version_id in plan["targets"]:
                print(f"  - {file_id} @ {version_id}")
            if plan["documents"]:
                print("\n预览模式，未做任何改动。确认清单无误后加 --yes 执行；")
                print("执行前请先停知识服务（运行中服务的内存索引会覆盖重建结果）。")
            else:
                print("\n没有可重建的文档，无需执行。")
            return 0 if plan["documents"] else 2

        report = apply_rebuild(config)
    except RuntimeError as exc:
        print(f"--apply 失败：{exc}", file=sys.stderr)
        return 1

    if not report.get("applied"):
        print(f"无需重建：{report.get('reason')}")
        return 2
    print(
        f"重建完成: {report['documents']} 文档 / {report['chunks']} 块 → {report['store']}"
        f"（Embedder: {report['embedder']}）"
    )
    print(f"旧索引备份: {report['backup'] or '（原索引不存在，未备份）'}")
    print("请重启知识服务以加载新索引（BM25 派生索引会自动懒重建）。")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description="从 derived/ 产物全量重建向量索引（验证 / 生产替换）")
    parser.add_argument("--apply", action="store_true", help="生产替换模式（默认为黄金语料重建验证）")
    parser.add_argument("--yes", action="store_true", help="配合 --apply：跳过预览真正执行替换")
    parser.add_argument("--top-k", type=int, default=5)
    parser.add_argument("--chunk-size", type=int, default=EVAL_CHUNK_SIZE)
    parser.add_argument("--chunk-overlap", type=int, default=EVAL_CHUNK_OVERLAP)
    parser.add_argument("--output", type=Path, default=None, help="把对比报告写入 JSON 文件（仅验证模式）")
    args = parser.parse_args()
    if args.apply:
        return run_apply(args)
    return run_verify(args)


if __name__ == "__main__":
    raise SystemExit(main())
