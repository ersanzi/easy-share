"""从 derived/ 产物重建向量索引（OKF 架构原则 1：索引是可重建缓存）。

重建核心供两个入口共用（口径定稿见 docs/iterations/2026-10-02-okf-rebuild-verify.md）：
- scripts/rebuild_from_derived.py 默认模式：黄金语料双路对比验证（切片一）；
- 同脚本 --apply 模式：生产 JSON 索引的重建与原子替换（切片二，2026-10-11）。

口径要点：
- 构建参数的真相源是 manifest：contextual 摘要取留档的 summary/provider，
  不重新调用 LLM；
- embedding 用当前配置的 embedder 重建（与线上查询同一 build_embedder 工厂），
  保证重建索引与查询口径一致——未配置 embedding 服务时退回 HashEmbedder，
  与生产行为同构，不会出现「重建用 A 模型、查询用 B 模型」的维度错配。
"""
from __future__ import annotations

import json
import shutil
from datetime import datetime
from pathlib import Path

from app.config import Settings
from app.jobs.store import JobStore
from app.kb.chunker import chunk_document
from app.kb.embedder import Embedder, build_embedder
from app.kb.store import VectorStore
from app.parsing.models import DocumentBlock, ParsedDocument, SourceLocation
from app.pipeline.service import PIPELINE_VERSION, artifact_keys
from app.storage.rustfs import RustFSStorage


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
    embedder: Embedder,
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


def collect_rebuild_targets(vector_store: VectorStore, job_store: JobStore) -> list[tuple[str, str]]:
    """枚举待重建文档（file_id, version_id）。

    权威来源是任务库：每个 file_id 取最新一次 completed 的版本——这样才能
    恢复「索引丢块/丢文档」的灾难场景（丢掉的记录无法从索引自身反查）；
    当前索引里有、任务库里没有的文档（历史年代数据）从索引反查 version_id
    兜底合并，保证不漏。
    """
    targets: dict[str, str] = {}
    for file_id, version_id in job_store.latest_completed_docs():
        targets[file_id] = version_id
    for record in vector_store.snapshot_records():
        doc_id = record.get("doc_id")
        if not doc_id or doc_id in targets:
            continue
        version_id = record.get("version_id") or (record.get("metadata") or {}).get("version_id")
        if version_id:
            targets[doc_id] = str(version_id)
    return sorted(targets.items())


def _refuse_milvus(config: Settings) -> None:
    if config.milvus_uri:
        raise RuntimeError(
            "Milvus 后端不支持本重建链路（当前仅支持 JSON 文件索引）；"
            "如已切 Milvus，请先扩展重建验证覆盖 MilvusVectorStore 再使用"
        )


def plan_rebuild(config: Settings) -> dict:
    """--apply 预览：只枚举目标与检查前置，不读 derived、不写任何文件。"""
    _refuse_milvus(config)
    store_path = Path(config.vector_store_path)
    current = VectorStore(str(store_path))
    job_store = JobStore(config.job_store_path)
    try:
        targets = collect_rebuild_targets(current, job_store)
    finally:
        job_store.close()
    return {
        "store": str(store_path),
        "store_exists": store_path.exists(),
        "documents": len(targets),
        "targets": targets,
        "embedder": type(build_embedder(config)).__name__,
    }


def apply_rebuild(config: Settings, *, storage=None, embedder=None) -> dict:
    """从 derived 产物全量重建生产向量索引并原子替换（--apply 核心）。

    安全语义：
    - 任一文档重建失败即整体中止，生产索引文件保持原样（防静默丢文档）；
    - 替换前旧索引备份为 vector_store.json.bak-<时间戳>；
    - 新索引先写同目录临时文件再 os.replace，替换是原子的。

    注意：请停服务后执行——运行中的服务持有内存索引，下次入库写回会覆盖
    重建结果；apply 完成后重启服务（BM25 等派生索引会按 count 一致性自动
    懒重建）。storage/embedder 可注入（测试用），默认按 config 构建。
    """
    _refuse_milvus(config)
    resolved_storage = storage or RustFSStorage(
        endpoint=config.rustfs_endpoint,
        access_key=config.rustfs_access_key,
        secret_key=config.rustfs_secret_key,
        bucket=config.rustfs_bucket,
    )
    resolved_embedder = embedder or build_embedder(config)

    store_path = Path(config.vector_store_path)
    current = VectorStore(str(store_path))
    job_store = JobStore(config.job_store_path)
    try:
        targets = collect_rebuild_targets(current, job_store)
    finally:
        job_store.close()
    if not targets:
        return {
            "applied": False,
            "reason": "没有可重建的文档（任务库无 completed 记录且当前索引为空）",
            "store": str(store_path),
        }

    tmp_path = store_path.with_name(store_path.name + ".rebuild-tmp")
    tmp_path.unlink(missing_ok=True)  # 清掉上次中断可能留下的残留，防旧数据混入
    rebuilt = VectorStore(str(tmp_path))
    failures: list[str] = []
    documents = 0
    chunks = 0
    for file_id, version_id in targets:
        try:
            items = rebuild_doc_items(
                resolved_storage,
                file_id,
                version_id,
                resolved_embedder,
                config.chunk_size,
                config.chunk_overlap,
            )
        except Exception as exc:  # noqa: BLE001——任何单文档失败都要中止整体替换
            failures.append(f"{file_id}@{version_id}: {exc}")
            continue
        rebuilt.replace_doc(file_id, items)
        documents += 1
        chunks += len(items)

    if failures:
        tmp_path.unlink(missing_ok=True)
        raise RuntimeError(
            f"重建失败，生产索引未改动（{len(failures)} 个文档出错）: " + "; ".join(failures)
        )

    backup: str | None = None
    if store_path.exists():
        backup = f"{store_path}.bak-{datetime.now():%Y%m%d%H%M%S}"
        shutil.copy2(store_path, backup)
    tmp_path.replace(store_path)
    return {
        "applied": True,
        "store": str(store_path),
        "documents": documents,
        "chunks": chunks,
        "backup": backup,
        "embedder": type(resolved_embedder).__name__,
    }
