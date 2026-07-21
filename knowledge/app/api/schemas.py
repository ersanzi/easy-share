"""请求/响应模型。"""
from pydantic import BaseModel


class IngestRequest(BaseModel):
    source: str = "rustfs"  # rustfs | text
    key: str | None = None  # source=rustfs 时的对象键
    filename: str | None = None
    content: str | None = None  # source=text 时直接传文本
    doc_id: str | None = None  # 不传则自动生成


class IngestResponse(BaseModel):
    doc_id: str
    filename: str
    chunks: int
    chars: int


class QueryRequest(BaseModel):
    question: str
    top_k: int = 5
    doc_ids: list[str] | None = None  # 权限范围过滤（由 Java 控制面传入）


class SourceRef(BaseModel):
    doc_id: str | None = None
    score: float | None = None


class RetrievedChunk(BaseModel):
    doc_id: str | None = None
    filename: str | None = None
    score: float | None = None
    text: str


class QueryResponse(BaseModel):
    answer: str
    sources: list[SourceRef] = []
    contexts: list[RetrievedChunk] = []
