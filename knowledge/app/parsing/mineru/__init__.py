"""MinerU 深度解析 Provider：构建工厂与公共接口。"""
from __future__ import annotations

from app.config import Settings
from app.parsing.mineru.base import (
    MinerUCapability,
    MinerUError,
    MinerUParseResult,
    MinerUProvider,
    MinerUUnavailableError,
    UnavailableMinerUProvider,
)
from app.parsing.mineru.client import MinerUClient
from app.parsing.mineru.adapter import document_from_mineru, parse_table_html


def build_mineru_provider(config: Settings) -> MinerUClient | UnavailableMinerUProvider:
    """按配置构建 MinerU Provider：默认未启用，返回明确失败的占位实现。"""
    if not config.mineru_enabled:
        return UnavailableMinerUProvider()
    return MinerUClient(
        base_url=config.mineru_base_url,
        api_token=config.mineru_api_token,
        backend=config.mineru_backend,
        timeout_seconds=config.mineru_timeout_seconds,
        max_pages=config.mineru_max_pages,
    )


__all__ = [
    "MinerUCapability",
    "MinerUClient",
    "MinerUError",
    "MinerUParseResult",
    "MinerUProvider",
    "MinerUUnavailableError",
    "UnavailableMinerUProvider",
    "build_mineru_provider",
    "document_from_mineru",
    "parse_table_html",
]
