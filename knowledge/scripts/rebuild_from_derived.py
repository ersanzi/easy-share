"""从 derived/ 产物全量重建向量索引，并断言评测集指标不回退。

验证对标文档（docs/agent-knowledge-benchmark.md）架构原则 1 的资产承诺：
「derived/ 是唯一知识源，向量索引只是可重建缓存」——索引坏了应能只从
derived 产物完整重建，且检索质量不损失。

流程（默认 HashEmbedder 确定性口径，与 scripts/eval_retrieval.py 同源）：
1. 黄金语料走完整入库管线（解析→清洗→contextual 摘要→切块→embedding→索引），
   得到基线检索指标与 derived 产物（document.json / manifest.json）。
2. 只读 derived 产物重建整套向量索引：不碰源文档、不重跑解析与清洗，
   contextual 参数（摘要 + provider）从 manifest 恢复，不再调用 LLM。
3. 同一评测集对重建索引重新打分，四指标与基线逐项比对，
   任一下降即非零退出。

用法：
    python scripts/rebuild_from_derived.py            # 重建验证（哈希口径）
    python scripts/rebuild_from_derived.py --output r.json   # 结果留档 JSON
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

from app.eval.retrieval import evaluate_retrieval  # noqa: E402
from app.kb.chunker import chunk_document  # noqa: E402
from app.kb.store import VectorStore  # noqa: E402
from app.parsing.models import DocumentBlock, ParsedDocument, SourceLocation  # noqa: E402
from app.pipeline.service import PIPELINE_VERSION, artifact_keys  # noqa: E402
from app.rag.retriever import Retriever  # noqa: E402
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


def document_from_payload(payload: dict) -> ParsedDocument:
    """把 document.json（ParsedDocument.to_dict 产物）反序列化回文档对象。

    to_dict 会弹出取默认值的字段（level/rows/text/metadata/source 内的 None），
    此处逐字段回填默认值，保证与解析器直出对象等价。
    """
    blocks = [
        DocumentBlock(
            id=raw["id"],
            type=raw["type"],
            text=raw.get("text", ""),
            level=raw.get("level"),
            rows=raw.get("rows", []),
            source=SourceLocation(**raw.get("source", {})),
            metadata=raw.get("metadata", {}),
        )
        for raw in payload.get("blocks", [])
    ]
    return ParsedDocument(
        filename=payload["filename"],
        media_type=payload["media_type"],
        blocks=blocks,
        warnings=payload.get("warnings", []),
        metadata=payload.get("metadata", {}),
    )


def rebuild_doc_items(
    storage,
    file_id: str,
    version_id: str,
    embedder,
    chunk_size: int,
    chunk_overlap: int,
) -> list[dict]:
    """从单个文档的 derived 产物重建向量索引条目（复刻管线 items 结构）。

    与入库管线的唯一信息差：contextual 摘要不重新生成，直接取 manifest
    里留档的 summary/provider——这正是「manifest 是构建参数真相源」的用法。
    """
    keys = artifact_keys(file_id, version_id)
    payload = json.loads(storage.read(keys["document"]))
    manifest = json.loads(storage.read(keys["manifest"]))

    doc_summary = ""
    heuristic_mode = False
    contextual_meta = manifest.get("contextual") or {}
    if contextual_meta.get("enabled"):
        doc_summary = contextual_meta.get("summary") or ""
        heuristic_mode = contextual_meta.get("provider") == "heuristic"

    document = document_from_payload(payload)
    chunks = chunk_document(
        document,
        chunk_size,
        chunk_overlap,
        doc_summary=doc_summary,
        heuristic_mode=heuristic_mode,
    )
    embeddings = embedder.embed([chunk.text for chunk in chunks])
    if len(embeddings) != len(chunks):
        raise ValueError(f"{file_id}: embedding 数量与文本块数量不一致")

    return [
        {
            "id": f"{file_id}:{version_id}:{index}",
            "doc_id": file_id,
            "file_id": file_id,
            "version_id": version_id,
            "text": chunk.text,
            "metadata": {
                "filename": manifest.get("filename"),
                "object_key": manifest.get("object_key"),
                "owner": manifest.get("owner"),
                "visible_depts": manifest.get("visible_depts"),
                "pipeline_version": manifest.get("pipeline_version", PIPELINE_VERSION),
                "ingested_at": manifest.get("processed_at"),
                **chunk.metadata(),
            },
            "embedding": embedding,
        }
        for index, (chunk, embedding) in enumerate(zip(chunks, embeddings))
    ]


def main() -> int:
    parser = argparse.ArgumentParser(description="从 derived/ 产物全量重建向量索引并验证指标不回退")
    parser.add_argument("--top-k", type=int, default=5)
    parser.add_argument("--chunk-size", type=int, default=EVAL_CHUNK_SIZE)
    parser.add_argument("--chunk-overlap", type=int, default=EVAL_CHUNK_OVERLAP)
    parser.add_argument("--output", type=Path, default=None, help="把对比报告写入 JSON 文件")
    args = parser.parse_args()

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


if __name__ == "__main__":
    raise SystemExit(main())
