"""服务配置：全部来自环境变量 / .env，敏感凭证不写死在代码里。"""
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", env_file_encoding="utf-8", extra="ignore")

    # 服务
    app_name: str = "EasyShare Knowledge Service"
    host: str = "127.0.0.1"
    port: int = 8000

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

    # 切块
    chunk_size: int = 800
    chunk_overlap: int = 120

    # 检索
    retrieval_top_k: int = 5

    # 向量库持久化
    vector_store_path: str = "./data/vector_store.json"


settings = Settings()
