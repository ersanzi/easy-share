"""仅供本地开发与测试的文档处理可视化实验台。"""
from __future__ import annotations

import mimetypes
import re
import uuid
from pathlib import Path, PurePosixPath
from typing import Annotated

from fastapi import APIRouter, File, Form, HTTPException, Query, Request, UploadFile, status
from fastapi.responses import FileResponse
from starlette.concurrency import run_in_threadpool

from app.api.schemas import ProcessingJobResponse, SAFE_ID_PATTERN
from app.parsing.extractor import SUPPORTED_EXTENSIONS
from app.services import AppServices

router = APIRouter(prefix="/lab", include_in_schema=False)
ASSET_DIR = Path(__file__).resolve().parent
LOCAL_CLIENTS = {"127.0.0.1", "::1", "testclient"}
SAFE_ID_RE = re.compile(SAFE_ID_PATTERN)


def _services(request: Request) -> AppServices:
    return request.app.state.services


def _guard_local_lab(request: Request) -> AppServices:
    """实验台无认证，只允许显式开启后由本机回环地址访问。"""
    services = _services(request)
    if not services.config.local_lab_enabled:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "本地实验台未启用")
    client_host = request.client.host if request.client else ""
    if client_host not in LOCAL_CLIENTS:
        raise HTTPException(status.HTTP_403_FORBIDDEN, "本地实验台仅允许从回环地址访问")
    return services


def _job_response(job) -> ProcessingJobResponse:
    return ProcessingJobResponse.model_validate(job.to_dict())


def _validate_id(value: str, field_name: str) -> str:
    if not SAFE_ID_RE.fullmatch(value):
        raise HTTPException(
            status.HTTP_422_UNPROCESSABLE_CONTENT,
            f"{field_name} 只能包含字母、数字、点、下划线、冒号和短横线，且最长 128 字符",
        )
    return value


def _safe_filename(raw_filename: str | None) -> str:
    candidate = PurePosixPath((raw_filename or "upload").replace("\\", "/")).name
    candidate = "".join("_" if ord(char) < 32 else char for char in candidate).strip(" .")
    candidate = re.sub(r'[<>:"/\\|?*]+', "_", candidate)
    if not candidate:
        candidate = "upload"
    if len(candidate) > 180:
        suffix = Path(candidate).suffix[:20]
        candidate = f"{Path(candidate).stem[: max(1, 180 - len(suffix))]}{suffix}"
    return candidate


@router.get("")
def lab_page(request: Request) -> FileResponse:
    _guard_local_lab(request)
    return FileResponse(ASSET_DIR / "index.html", media_type="text/html; charset=utf-8")


@router.get("/assets/lab.css")
def lab_css(request: Request) -> FileResponse:
    _guard_local_lab(request)
    return FileResponse(ASSET_DIR / "lab.css", media_type="text/css; charset=utf-8")


@router.get("/assets/lab.js")
def lab_js(request: Request) -> FileResponse:
    _guard_local_lab(request)
    return FileResponse(ASSET_DIR / "lab.js", media_type="text/javascript; charset=utf-8")


# ---------------------------------------------------------------------------
# 知识质量驾驶舱（/lab/cockpit）
# ---------------------------------------------------------------------------

COCKPIT_DIR = Path(__file__).resolve().parent.parent / "debug"


@router.get("/cockpit")
def cockpit_page(request: Request) -> FileResponse:
    _guard_local_lab(request)
    return FileResponse(COCKPIT_DIR / "cockpit.html", media_type="text/html; charset=utf-8")


@router.get("/cockpit/cockpit.css")
def cockpit_css(request: Request) -> FileResponse:
    _guard_local_lab(request)
    return FileResponse(COCKPIT_DIR / "cockpit.css", media_type="text/css; charset=utf-8")


@router.get("/cockpit/cockpit.js")
def cockpit_js(request: Request) -> FileResponse:
    _guard_local_lab(request)
    return FileResponse(COCKPIT_DIR / "cockpit.js", media_type="text/javascript; charset=utf-8")


@router.get("/api/jobs", response_model=list[ProcessingJobResponse])
def list_jobs(
    request: Request,
    limit: Annotated[int, Query(ge=1, le=100)] = 20,
) -> list[ProcessingJobResponse]:
    services = _guard_local_lab(request)
    return [_job_response(job) for job in services.job_store.list_recent(limit=limit)]


@router.post(
    "/api/uploads",
    response_model=ProcessingJobResponse,
    status_code=status.HTTP_202_ACCEPTED,
)
async def upload_document(
    request: Request,
    file: Annotated[UploadFile, File(description="待处理的 Office、PDF、图片或文本文件")],
    file_id: Annotated[str | None, Form()] = None,
    version_id: Annotated[str, Form()] = "v1",
    force: Annotated[bool, Form()] = False,
) -> ProcessingJobResponse:
    services = _guard_local_lab(request)
    resolved_filename = _safe_filename(file.filename)
    extension = Path(resolved_filename).suffix.lower()
    if extension not in SUPPORTED_EXTENSIONS:
        supported = "、".join(sorted(SUPPORTED_EXTENSIONS))
        raise HTTPException(
            status.HTTP_415_UNSUPPORTED_MEDIA_TYPE,
            f"暂不支持 {extension or '无扩展名文件'}，当前支持：{supported}",
        )

    resolved_file_id = _validate_id(file_id or f"lab-{uuid.uuid4().hex}", "file_id")
    resolved_version_id = _validate_id(version_id, "version_id")

    if not force:
        existing = services.job_store.find_latest(
            file_id=resolved_file_id,
            version_id=resolved_version_id,
        )
        if existing is not None:
            return _job_response(existing)

    try:
        content = await file.read(services.config.max_source_bytes + 1)
    finally:
        await file.close()
    if len(content) > services.config.max_source_bytes:
        raise HTTPException(
            status.HTTP_413_CONTENT_TOO_LARGE,
            f"文件超过本地实验台上限 {services.config.max_source_bytes} 字节",
        )
    if not content:
        raise HTTPException(status.HTTP_422_UNPROCESSABLE_ENTITY, "不能上传空文件")

    object_key = f"lab/uploads/{resolved_file_id}/{resolved_version_id}/{resolved_filename}"
    content_type = file.content_type or mimetypes.guess_type(resolved_filename)[0]
    content_type = content_type or "application/octet-stream"
    try:
        await run_in_threadpool(
            services.storage.write,
            object_key,
            content,
            content_type=content_type,
        )
    except Exception as exc:
        raise HTTPException(status.HTTP_502_BAD_GATEWAY, f"写入 RustFS 失败：{exc}") from exc

    job, created = services.job_store.create_or_get(
        file_id=resolved_file_id,
        version_id=resolved_version_id,
        object_key=object_key,
        filename=resolved_filename,
        # 2b 文件归属：lab 上传要求登录（auth 开启时），谁传的归谁；未登录（本地
        # auth 关闭场景）落 None = 共享文档
        owner=(getattr(request.state, "user", None) or {}).get("username"),
        force=force,
    )
    if created or job.status == "queued":
        services.job_runner.submit(job.id)
    return _job_response(job)
