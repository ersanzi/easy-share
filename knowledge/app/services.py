"""服务组件容器：集中组装依赖，避免路由模块级单例并便于测试注入。"""
from __future__ import annotations

import logging
from dataclasses import dataclass

from app.ai_config import AiConfigStore, apply_overlay
from app.config import Settings, settings
from app.auth.sso import ControlPlaneSSO
from app.auth.store import UserStore
from app.jobs.runner import JobRunner
from app.jobs.store import JobStore
from app.kb.bm25 import BM25Retriever
from app.kb.contextual import DocContextBuilder, build_doc_context_builder
from app.kb.embedder import Embedder, build_embedder
from app.kb.query_log import QueryLog
from app.kb.reranker import Reranker, NoopReranker, build_reranker
from app.kb.store import VectorStore
from app.meetings.engine import MeetingTranscriber
from app.meetings.service import MeetingService
from app.meetings.store import MeetingStore
from app.meetings.summarizer import MeetingSummarizer
from app.pipeline.service import DocumentPipeline
from app.ocr import OCRProvider, build_paddle_provider
from app.parsing.mineru import build_mineru_provider
from app.parsing.mineru.base import MinerUProvider
from app.parsing.pdf_router import build_pdf_router
from app.rag.generator import Generator, build_generator
from app.rag.multi_hop import MultiHopRetriever, build_multi_hop_retriever
from app.rag.orchestrator import QueryOrchestrator
from app.rag.retriever import Retriever
from app.storage.base import ObjectStorage
from app.storage.rustfs import RustFSStorage
from app.watcher.service import DirectoryWatcher

logger = logging.getLogger(__name__)


@dataclass(slots=True)
class AppServices:
    config: Settings
    storage: ObjectStorage
    embedder: Embedder
    vector_store: VectorStore
    bm25: BM25Retriever
    query_log: QueryLog
    reranker: Reranker | NoopReranker
    retriever: Retriever
    orchestrator: QueryOrchestrator
    generator: Generator | None
    job_store: JobStore
    pipeline: DocumentPipeline
    job_runner: JobRunner
    ocr: OCRProvider | None = None
    mineru: MinerUProvider | None = None
    multi_hop: MultiHopRetriever | None = None
    users: UserStore | None = None
    watcher: DirectoryWatcher | None = None
    sso: ControlPlaneSSO | None = None
    meetings: MeetingService | None = None

    def start(self) -> None:
        self.job_runner.start()
        if self.watcher is not None:
            self.watcher.start()

    def close(self) -> None:
        if self.watcher is not None:
            self.watcher.stop()
        if self.meetings is not None:
            self.meetings.shutdown()
        self.job_runner.shutdown()
        self.job_store.close()

    def reload_ai_components(self, overlay: dict) -> tuple[list[str], list[str]]:
        """管理端下发 AI 配置后的热重建：LLM 消费组件换新实例，ASR 开关热构建/拆除。

        进行中的请求持有旧组件引用，自然跑完不受影响。返回 (已热生效项, 警告)。
        """
        applied: list[str] = []
        warnings: list[str] = []
        new_config = apply_overlay(self.config, overlay)

        llm_changed = (
            new_config.llm_base_url,
            new_config.llm_model,
            new_config.llm_api_key,
        ) != (
            self.config.llm_base_url,
            self.config.llm_model,
            self.config.llm_api_key,
        )
        if llm_changed:
            self.generator = build_generator(new_config)
            self.multi_hop = build_multi_hop_retriever(
                new_config, self.retriever, self.bm25, query_log=self.query_log
            )
            if self.pipeline is not None:
                self.pipeline.context_builder = build_doc_context_builder(new_config)
            applied.append("llm")

        asr_changed = (
            new_config.asr_enabled,
            new_config.asr_speaker_enabled,
        ) != (
            self.config.asr_enabled,
            self.config.asr_speaker_enabled,
        )
        if asr_changed:
            if self.meetings is not None:
                self.meetings.shutdown()
                self.meetings = None
            if new_config.asr_enabled:
                self.meetings = MeetingService(
                    store=MeetingStore(new_config.asr_data_path),
                    transcriber=MeetingTranscriber(speaker_enabled=new_config.asr_speaker_enabled),
                    summarizer=MeetingSummarizer(
                        new_config.llm_base_url,
                        new_config.llm_api_key,
                        new_config.llm_model,
                        trust_env=new_config.llm_trust_env,
                    ),
                )
            applied.append("asr")

        if not llm_changed and not asr_changed:
            warnings.append("配置与当前生效值一致，无需重载")
        self.config = new_config
        return applied, warnings


def build_vector_store(config: Settings) -> VectorStore:
    """根据配置选择向量库实现：Milvus（生产）或 JSON 文件（开发/测试）。"""
    if config.milvus_uri:
        from app.kb.milvus_store import MilvusVectorStore

        logger.info("向量库：Milvus（%s, collection=%s）", config.milvus_uri, config.milvus_collection)
        return MilvusVectorStore(
            uri=config.milvus_uri,
            dim=config.embedding_dim,
            collection_name=config.milvus_collection,
        )
    logger.info("向量库：JSON 文件（%s）", config.vector_store_path)
    return VectorStore(config.vector_store_path)


def build_services(config: Settings = settings) -> AppServices:
    # 管理端下发的 AI 配置覆盖层（控制面推送落地的 runtime_config.json）优先于 .env；
    # 覆盖后局部变量指向生效配置，后续组件装配与 .env 直启完全同构。
    config = apply_overlay(config, AiConfigStore(config.runtime_config_path).load())
    storage = RustFSStorage(
        endpoint=config.rustfs_endpoint,
        access_key=config.rustfs_access_key,
        secret_key=config.rustfs_secret_key,
        bucket=config.rustfs_bucket,
    )
    embedder = build_embedder(config)
    ocr = build_paddle_provider(enabled=config.ocr_enabled, lang=config.ocr_lang)
    mineru = build_mineru_provider(config)
    pdf_router = build_pdf_router(config.pdf_inspector_enabled)
    vector_store = build_vector_store(config)
    bm25 = BM25Retriever()
    query_log = QueryLog(config.query_log_path)
    reranker = build_reranker(config)
    retriever = Retriever(embedder, vector_store, bm25=bm25)
    generator = build_generator(config)
    multi_hop = build_multi_hop_retriever(config, retriever, bm25, query_log=query_log)
    orchestrator = QueryOrchestrator(
        retriever=retriever,
        store=vector_store,
        bm25=bm25,
        reranker=reranker,
        multi_hop=multi_hop,
        query_log=query_log,
        strategy=config.query_strategy,
    )
    job_store = JobStore(config.job_store_path)
    users = UserStore(config.auth_db_path, token_expiry_hours=config.auth_token_expiry_hours)
    sso = (
        ControlPlaneSSO(
            config.control_plane_url,
            client_id=config.control_plane_client_id,
            users=users,
        )
        if config.control_plane_url
        else None
    )
    pipeline = DocumentPipeline(
        storage=storage,
        embedder=embedder,
        vector_store=vector_store,
        chunk_size=config.chunk_size,
        chunk_overlap=config.chunk_overlap,
        max_source_bytes=config.max_source_bytes,
        cleaning_rules_path=config.cleaning_rules_path,
        ocr_provider=ocr,
        ocr_min_text_chars=config.ocr_min_text_chars,
        mineru_provider=mineru,
        pdf_router=pdf_router,
        context_builder=build_doc_context_builder(config),
    )
    job_runner = JobRunner(job_store, pipeline.process, workers=config.job_workers)
    app_services = AppServices(
        config=config,
        storage=storage,
        embedder=embedder,
        vector_store=vector_store,
        bm25=bm25,
        query_log=query_log,
        reranker=reranker,
        retriever=retriever,
        orchestrator=orchestrator,
        generator=generator,
        job_store=job_store,
        pipeline=pipeline,
        job_runner=job_runner,
        ocr=ocr,
        mineru=mineru,
        multi_hop=multi_hop,
        users=users,
        sso=sso,
    )
    if config.watch_dirs:
        # 监听器持 services 引用，须在容器组装完成后创建
        app_services.watcher = DirectoryWatcher(
            app_services,
            interval_seconds=config.watch_interval_seconds,
            stable_seconds=config.watch_stable_seconds,
        )
    if config.asr_enabled:
        # 会议听记：引擎在首个任务时才拉起 FunASR 模型，这里只建轻量容器
        app_services.meetings = MeetingService(
            store=MeetingStore(config.asr_data_path),
            transcriber=MeetingTranscriber(speaker_enabled=config.asr_speaker_enabled),
            summarizer=MeetingSummarizer(
                config.llm_base_url,
                config.llm_api_key,
                config.llm_model,
                trust_env=config.llm_trust_env,
            ),
        )
        logger.info("会议听记已启用（数据目录 %s）", config.asr_data_path)
    return app_services
