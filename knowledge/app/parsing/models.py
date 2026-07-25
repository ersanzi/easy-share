"""统一文档结构：解析器输出结构化块，清洗、渲染和索引都基于该模型。"""
from __future__ import annotations

from dataclasses import asdict, dataclass, field
from typing import Any


@dataclass(slots=True)
class SourceLocation:
    page: int | None = None
    sheet: str | None = None
    slide: int | None = None
    paragraph: int | None = None
    table: int | None = None
    row: int | None = None


@dataclass(slots=True)
class DocumentBlock:
    id: str
    type: str
    text: str = ""
    level: int | None = None
    rows: list[list[str]] = field(default_factory=list)
    source: SourceLocation = field(default_factory=SourceLocation)
    metadata: dict[str, Any] = field(default_factory=dict)

    def to_dict(self) -> dict[str, Any]:
        data = asdict(self)
        data["source"] = {key: value for key, value in data["source"].items() if value is not None}
        if not self.rows:
            data.pop("rows")
        if self.level is None:
            data.pop("level")
        if not self.text:
            data.pop("text")
        if not self.metadata:
            data.pop("metadata")
        return data


@dataclass(slots=True)
class ParsedDocument:
    filename: str
    media_type: str
    blocks: list[DocumentBlock]
    warnings: list[str] = field(default_factory=list)
    metadata: dict[str, Any] = field(default_factory=dict)

    def to_dict(self) -> dict[str, Any]:
        return {
            "schema_version": 1,
            "filename": self.filename,
            "media_type": self.media_type,
            "metadata": self.metadata,
            "warnings": self.warnings,
            "blocks": [block.to_dict() for block in self.blocks],
        }
