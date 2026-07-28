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

    # OCR 为可选运行时，重量级依赖单独安装。
    ocr_enabled: bool = True
    ocr_lang: str = "ch"
    ocr_min_text_chars: int = 20

    # 切块与检索
    chunk_size: int = 800
    chunk_overlap: int = 120
    retrieval_top_k: int = 5

    # 清洗规则引擎：JSON 规则集路径（不存在则用内置默认；里程碑 2 起由 Java 下发同一 schema）
    cleaning_rules_path: str = "./data/cleaning_rules.json"

    # 本地持久化与任务执行
    vector_store_path: str = "./data/vector_store.json"
    job_store_path: str = "./data/jobs.db"
    job_workers: int = 2
    max_source_bytes: int = 100 * 1024 * 1024


settings = Settings()
