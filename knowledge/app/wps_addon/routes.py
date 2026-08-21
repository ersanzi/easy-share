"""WPS 加载项静态入口：知识服务自己托管加载项页面，与 /auth、/query 同源，天然免跨域。

WPS 启动时按 jsaddons 登记项拉取 {server}/wps/ribbon.xml 并加载 index.html（回调上下文），
任务窗格页面为 taskpane.html。安装方式见 scripts/install_wps_addon.ps1。
"""
from __future__ import annotations

from pathlib import Path

from fastapi import APIRouter
from fastapi.responses import FileResponse

router = APIRouter(prefix="/wps", include_in_schema=False)
ASSET_DIR = Path(__file__).resolve().parent


@router.get("/ribbon.xml")
def ribbon() -> FileResponse:
    # ribbon.xml 需以 XML 类型响应，WPS 据此解析功能区
    return FileResponse(ASSET_DIR / "ribbon.xml", media_type="text/xml; charset=utf-8")


@router.get("")
@router.get("/")
def addon_index() -> FileResponse:
    return FileResponse(ASSET_DIR / "index.html", media_type="text/html; charset=utf-8")


@router.get("/index.html")
def addon_index_html() -> FileResponse:
    return FileResponse(ASSET_DIR / "index.html", media_type="text/html; charset=utf-8")


@router.get("/taskpane.html")
def taskpane() -> FileResponse:
    return FileResponse(ASSET_DIR / "taskpane.html", media_type="text/html; charset=utf-8")
