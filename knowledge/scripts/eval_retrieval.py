"""对检索链路跑标注评测集，输出 recall@k / hit@1 / MRR / 片段命中率报告。

默认使用 HashEmbedder（确定性、无语义，作为 CI 基线口径）；
--real 时按 .env / 环境变量中的 embedding 配置调用真实服务，
用于对比语义检索质量或验证切块参数改动。
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

from app.config import settings  # noqa: E402
from app.eval.retrieval import evaluate_retrieval  # noqa: E402
from app.kb.embedder import build_embedder  # noqa: E402
from tests.retrieval.corpus import (  # noqa: E402
    EVAL_CHUNK_OVERLAP,
    EVAL_CHUNK_SIZE,
    build_eval_setup,
    index_corpus,
    load_cases,
)


def main() -> int:
    parser = argparse.ArgumentParser(description="EasyShare 检索质量评测")
    parser.add_argument("--top-k", type=int, default=5)
    parser.add_argument(
        "--real",
        action="store_true",
        help="使用 .env 配置的真实 embedding 服务（会产生少量 API 调用费用）",
    )
    parser.add_argument("--chunk-size", type=int, default=EVAL_CHUNK_SIZE)
    parser.add_argument("--chunk-overlap", type=int, default=EVAL_CHUNK_OVERLAP)
    parser.add_argument("--output", type=Path, default=None, help="把完整报告写入 JSON 文件")
    args = parser.parse_args()

    embedder = None
    embedder_name = "hash"
    if args.real:
        if not (settings.embedding_api_key and settings.embedding_base_url and settings.embedding_model):
            print("未配置 embedding 服务（embedding_base_url/api_key/model），无法使用 --real", file=sys.stderr)
            return 2
        embedder = build_embedder(settings)
        embedder_name = settings.embedding_model

    with tempfile.TemporaryDirectory(prefix="easyshare-retrieval-eval-") as work_dir:
        setup = build_eval_setup(
            Path(work_dir),
            embedder=embedder,
            chunk_size=args.chunk_size,
            chunk_overlap=args.chunk_overlap,
        )
        file_ids = index_corpus(setup)
        report = evaluate_retrieval(setup.retriever, load_cases(), top_k=args.top_k)

    report["embedder"] = embedder_name
    report["chunk_size"] = args.chunk_size
    report["chunk_overlap"] = args.chunk_overlap
    report["indexed_documents"] = len(file_ids)

    print(f"语料文档: {len(file_ids)}  评测用例: {report['cases']}  embedder: {embedder_name}")
    print(f"chunk_size/overlap: {args.chunk_size}/{args.chunk_overlap}  top_k: {args.top_k}")
    print(f"recall@{args.top_k}: {report['recall_at_k']:.3f}")
    print(f"hit@1:      {report['hit_at_1']:.3f}")
    print(f"mrr:        {report['mrr']:.3f}")
    print(f"snippet:    {report['snippet_hit_rate']:.3f}")
    if report["misses"]:
        print(f"未达标用例 ({len(report['misses'])}):")
        for miss in report["misses"]:
            print(
                f"  {miss['case_id']}: rank={miss['hit_rank']} "
                f"snippet_hit={miss['snippet_hit']} retrieved={miss['retrieved_file_ids']}"
            )

    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(
            json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8"
        )
        print(f"完整报告已写入: {args.output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
