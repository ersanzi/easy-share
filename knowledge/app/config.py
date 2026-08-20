"""服务配置：全部来自环境变量 / .env，敏感凭证不写死在代码里。"""
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", env_file_encoding="utf-8", extra="ignore")

    # 服务
    app_name: str = "EasyShare Knowledge Service"
    host: str = "127.0.0.1"
    port: int = 8000
    local_lab_enabled: bool = True

    # RustFS（S3 兼容）
    rustfs_endpoint: str = "http://127.0.0.1:9000"
    rustfs_access_key: str = ""
    rustfs_secret_key: str = ""
    rustfs_bucket: str = "easyshare"

    # LLM（OpenAI 兼容）
    llm_base_url: str = ""
    llm_api_key: str = ""
    llm_model: str = ""

    # Embedding（OpenAI 兼容；未配置时退回 HashEmbedder 仅跑通管线）
    embedding_base_url: str = ""
    embedding_api_key: str = ""
    embedding_model: str = ""
    embedding_dim: int = 1024

    # Reranker（OpenAI 兼容 rerank API；未配置时保持原始排序）
    rerank_base_url: str = ""
    rerank_api_key: str = ""
    rerank_model: str = ""

    # OCR 为可选运行时，重量级依赖单独安装。
    ocr_enabled: bool = True
    ocr_lang: str = "ch"
    ocr_min_text_chars: int = 20

    # MinerU 深度解析（可选远程服务；失败自动回退本地管线，默认关闭）
    # backend 可选：pipeline（无 GPU、多语言）/ vlm-engine / vlm-http-client / hybrid-*（需配套 vllm 服务）
    mineru_enabled: bool = False
    mineru_base_url: str = "http://127.0.0.1:8779"
    mineru_api_token: str = ""
    mineru_backend: str = "pipeline"
    mineru_timeout_seconds: int = 300
    mineru_max_pages: int = 300

    # pdf-inspector 快路由（可选本地依赖 pip install pdf-inspector；未安装自动退回启发式，默认关闭）
    pdf_inspector_enabled: bool = False

    # 切块与检索
    chunk_size: int = 800
    chunk_overlap: int = 120
    retrieval_top_k: int = 5

    # Agent 多跳检索（需配置 LLM 做充分性裁判；预算控制防多跳上下文雪崩）
    multi_hop_max_hops: int = 3
    multi_hop_hop_top_k: int = 5
    multi_hop_max_contexts: int = 10
    multi_hop_max_chars: int = 12000

    # 清洗规则引擎：JSON 规则集路径（不存在则用内置默认；里程碑 2 起由 Java 下发同一 schema）
    cleaning_rules_path: str = "./data/cleaning_rules.json"

    # 本地持久化与任务执行
    vector_store_path: str = "./data/vector_store.json"
    job_store_path: str = "./data/jobs.db"
    job_workers: int = 2
    max_source_bytes: int = 100 * 1024 * 1024

    # Milvus 向量库（可选；milvus_uri 为空时退回 JSON 文件存储）
    # 本地 Standalone 默认 http://127.0.0.1:19530
    milvus_uri: str = ""
    milvus_collection: str = "easyshare_chunks"

    # 查询日志（支撑健康度仪表盘使用率与盲区分析）
    query_log_path: str = "./data/query_log.db"


settings = Settings()
