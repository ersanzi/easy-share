"""RustFS（S3 兼容）对象存储读写。"""
from __future__ import annotations

import threading
from typing import Any

import boto3
from botocore.config import Config

from app.config import settings


class ObjectTooLargeError(ValueError):
    pass


class RustFSStorage:
    def __init__(
        self,
        *,
        endpoint: str | None = None,
        access_key: str | None = None,
        secret_key: str | None = None,
        bucket: str | None = None,
    ) -> None:
        self.endpoint = endpoint or settings.rustfs_endpoint
        self.access_key = access_key if access_key is not None else settings.rustfs_access_key
        self.secret_key = secret_key if secret_key is not None else settings.rustfs_secret_key
        self.bucket = bucket or settings.rustfs_bucket
        self._client_instance: Any | None = None
        self._client_lock = threading.Lock()

    @property
    def client(self) -> Any:
        """延迟创建客户端，启动健康检查不应依赖 RustFS 已就绪。"""
        if self._client_instance is None:
            with self._client_lock:
                if self._client_instance is None:
                    self._client_instance = boto3.client(
                        "s3",
                        endpoint_url=self.endpoint,
                        aws_access_key_id=self.access_key or None,
                        aws_secret_access_key=self.secret_key or None,
                        config=Config(signature_version="s3v4"),
                        region_name="us-east-1",
                    )
        return self._client_instance

    def read(self, key: str, *, max_bytes: int | None = None) -> bytes:
        obj = self.client.get_object(Bucket=self.bucket, Key=key)
        body = obj["Body"]
        try:
            content_length = int(obj.get("ContentLength") or 0)
            if max_bytes is not None and content_length > max_bytes:
                raise ObjectTooLargeError(f"对象大小 {content_length} 字节，超过限制 {max_bytes} 字节")
            content = body.read() if max_bytes is None else body.read(max_bytes + 1)
            if max_bytes is not None and len(content) > max_bytes:
                raise ObjectTooLargeError(f"对象超过限制 {max_bytes} 字节")
            return content
        finally:
            body.close()

    def write(self, key: str, content: bytes, *, content_type: str) -> None:
        self.client.put_object(
            Bucket=self.bucket,
            Key=key,
            Body=content,
            ContentType=content_type,
        )

    def list_keys(self, prefix: str = "") -> list[str]:
        keys: list[str] = []
        paginator = self.client.get_paginator("list_objects_v2")
        for page in paginator.paginate(Bucket=self.bucket, Prefix=prefix):
            for item in page.get("Contents", []):
                keys.append(item["Key"])
        return keys
