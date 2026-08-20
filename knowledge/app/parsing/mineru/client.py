"""MinerU HTTP 客户端：对接 mineru-api 的 /file_parse 同步端点（multipart 上传）。"""
from __future__ import annotations

import io
import logging
import time

import httpx

from app.parsing.mineru.base import MinerUCapability, MinerUError, MinerUParseResult

logger = logging.getLogger(__name__)


def _pick_entry(payload: dict, filename: str) -> dict:
    """响应按上传文件名分组；找不到时若仅一个条目也接受（服务端可能规范化了文件名）。"""
    if filename in payload and isinstance(payload[filename], dict):
        return payload[filename]
    values = [value for value in payload.values() if isinstance(value, dict)]
    if len(values) == 1:
        return values[0]
    raise KeyError(f"mineru-api 响应中找不到 {filename} 的结果")


class MinerUClient:
    """调用自建 mineru-api；官方 mineru.net API 兼容时仅需替换 base_url 与 token。"""

    name = "mineru"

    def __init__(
        self,
        *,
        base_url: str,
        api_token: str = "",
        backend: str = "pipeline",
        lang: str = "ch",
        timeout_seconds: int = 300,
        max_pages: int = 300,
    ) -> None:
        self.base_url = base_url.rstrip("/")
        self.api_token = api_token
        self.backend = backend
        self.lang = lang
        self.timeout_seconds = timeout_seconds
        self.max_pages = max_pages

    def capability(self) -> MinerUCapability:
        # 不做启动期健康探测：配置即视为可用，运行期失败由管线捕获并回退
        return MinerUCapability(available=True, provider=self.name, reason=None)

    def parse_pdf(self, content: bytes, *, filename: str) -> MinerUParseResult:
        started = time.monotonic()

        # 超长文档防护：MinerU 处理超大 PDF 有内存风险，超页数直接回退本地管线
        try:
            from pypdf import PdfReader

            page_count = len(PdfReader(io.BytesIO(content)).pages)
        except Exception:  # 页数统计失败不阻塞解析请求
            page_count = None
        if page_count is not None and page_count > self.max_pages:
            raise MinerUError(f"页数 {page_count} 超过 MinerU 上限 {self.max_pages}，改走本地管线")

        headers = {"Authorization": f"Bearer {self.api_token}"} if self.api_token else {}
        data = {
            "backend": self.backend,
            "lang_list": self.lang,
            "return_md": "true",
            "return_content_list": "true",
            "return_images": "false",
            "return_middle_json": "false",
        }
        files = {"files": (filename, content, "application/pdf")}
        try:
            response = httpx.post(
                f"{self.base_url}/file_parse",
                data=data,
                files=files,
                headers=headers,
                timeout=self.timeout_seconds,
            )
        except httpx.HTTPError as exc:
            raise MinerUError(f"mineru-api 请求失败: {exc}") from exc

        if response.status_code != 200:
            raise MinerUError(f"mineru-api 返回 {response.status_code}: {response.text[:200]}")
        try:
            payload = response.json()
            entry = _pick_entry(payload, filename)
            markdown = entry.get("md_content") or ""
            content_list = entry.get("content_list") or []
        except (ValueError, KeyError) as exc:
            raise MinerUError(f"mineru-api 响应畸形: {exc}") from exc

        duration_ms = int((time.monotonic() - started) * 1000)
        return MinerUParseResult(
            markdown=markdown,
            content_list=content_list,
            backend=self.backend,
            duration_ms=duration_ms,
        )
