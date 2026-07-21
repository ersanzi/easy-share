"""RustFS（S3 兼容）对象存储读取。"""
import logging

import boto3
from botocore.config import Config

from app.config import settings

logger = logging.getLogger(__name__)


class RustFSStorage:
    def __init__(self) -> None:
        self.client = boto3.client(
            "s3",
            endpoint_url=settings.rustfs_endpoint,
            aws_access_key_id=settings.rustfs_access_key,
            aws_secret_access_key=settings.rustfs_secret_key,
            config=Config(signature_version="s3v4"),
            region_name="us-east-1",
        )
        self.bucket = settings.rustfs_bucket

    def read(self, key: str) -> bytes:
        obj = self.client.get_object(Bucket=self.bucket, Key=key)
        return obj["Body"].read()

    def list_keys(self, prefix: str = "") -> list[str]:
        keys: list[str] = []
        paginator = self.client.get_paginator("list_objects_v2")
        for page in paginator.paginate(Bucket=self.bucket, Prefix=prefix):
            for item in page.get("Contents", []):
                keys.append(item["Key"])
        return keys
