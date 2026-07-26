"""请求/响应模型。"""
from __future__ import annotations

from typing import Any, Literal

from pydantic import BaseModel, Field

SAFE_ID_PATTERN = r"^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$"


class IngestRequest(BaseModel):
    source: Literal["rustfs", "text"] = "rustfs"
    key: str | None = None
    filename: str | None = None
    content: str | None = None
    doc_id: str | None = None


class IngestResponse(BaseModel):
    doc_id: str
    filename: str
    chunks: int
    chars: int


class QueryRequest(BaseModel):
    question: str = Field(min_length=1)
    top_k: int = Field(default=5, ge=1, le=100)
    doc_ids: list[str] | None = None


class SourceRef(BaseModel):
    doc_id: str | None = None
    score: float | None = None


class RetrievedChunk(BaseModel):
    doc_id: str | None = None
    file_id: str | None = None
    version_id: str | None = None
    filename: str | None = None
    score: float | None = None
    text: str


class QueryResponse(BaseModel):
    answer: str
    sources: list[SourceRef] = Field(default_factory=list)
    contexts: list[RetrievedChunk] = Field(default_factory=list)


class ProcessDocumentRequest(BaseModel):
    file_id: str = Field(pattern=SAFE_ID_PATTERN)
    version_id: str = Field(pattern=SAFE_ID_PATTERN)
    object_key: str = Field(min_length=1, max_length=1024)
    filename: str | None = Field(default=None, min_length=1, max_length=255)
    force: bool = False


class ProcessingJobResponse(BaseModel):
    id: str
    file_id: str
    version_id: str
    object_key: str
    filename: str
    status: Literal["queued", "processing", "completed", "failed"]
    stage: str
    progress: int
    retry_count: int
    error_code: str | None = None
    error_message: str | None = None
    result: dict[str, Any] | None = None
    created_at: str
    updated_at: str
    started_at: str | None = None
    finished_at: str | None = None


class ArtifactManifestResponse(BaseModel):
    schema_version: int
    pipeline_version: str
    status: str
    file_id: str
    version_id: str
    filename: str
    object_key: str
    source_sha256: str
    source_bytes: int
    media_type: str
    blocks: int
    characters: int
    chunks: int
    warnings: list[str] = Field(default_factory=list)
    artifacts: dict[str, str]
    processed_at: str
