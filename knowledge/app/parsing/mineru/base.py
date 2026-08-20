"""MinerU 深度解析抽象：默认未启用，失败回退本地管线，绝不阻塞文档处理。"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Protocol


class MinerUError(RuntimeError):
    """MinerU 解析失败（网络/服务/超页/结果畸形），触发本地管线回退。"""


class MinerUUnavailableError(MinerUError):
    """MinerU 未启用或未配置。"""


@dataclass(slots=True)
class MinerUCapability:
    available: bool
    provider: str
    reason: str | None = None

    def to_dict(self) -> dict[str, Any]:
        return {"available": self.available, "provider": self.provider, "reason": self.reason}


@dataclass(slots=True)
class MinerUParseResult:
    """MinerU 解析产物：content_list 为主（含类型/层级/页码），markdown 兜底。"""

    markdown: str
    content_list: list[dict[str, Any]] = field(default_factory=list)
    backend: str = "pipeline"
    duration_ms: int | None = None


class MinerUProvider(Protocol):
    name: str

    def capability(self) -> MinerUCapability: ...

    def parse_pdf(self, content: bytes, *, filename: str) -> MinerUParseResult: ...


@dataclass(slots=True)
class UnavailableMinerUProvider:
    """未启用 MinerU 时的明确失败实现，保证其他格式解析不受影响。"""

    reason: str = "MinerU 未启用；设置 MINERU_ENABLED=true 并配置 MINERU_BASE_URL 后生效"
    name: str = "unavailable"

    def capability(self) -> MinerUCapability:
        return MinerUCapability(available=False, provider=self.name, reason=self.reason)

    def parse_pdf(self, content: bytes, *, filename: str) -> MinerUParseResult:
        raise MinerUUnavailableError(self.reason)
