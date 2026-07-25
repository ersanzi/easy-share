"""对象存储协议，生产使用 RustFS，测试使用内存实现。"""
from __future__ import annotations

from typing import Protocol


class ObjectStorage(Protocol):
    def read(self, key: str, *, max_bytes: int | None = None) -> bytes:
        ...

    def write(self, key: str, content: bytes, *, content_type: str) -> None:
        ...
