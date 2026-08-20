"""FastAPI 入口。运行：uvicorn app.main:app --reload（在 knowledge/ 目录下）。"""
from __future__ import annotations

import logging
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import FastAPI
from fastapi.responses import JSONResponse
from starlette.concurrency import run_in_threadpool

from app.api.routes import router
from app.auth.routes import router as auth_router
from app.debug.routes import router as debug_router
from app.lab.routes import router as lab_router
from app.services import AppServices, build_services

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")

# AUTH_ENABLED=true 时要求登录的业务前缀；/auth、/health、/docs、/lab、/debug（回环守卫）不受令牌保护
_PROTECTED_PREFIXES = ("/documents", "/query", "/ingest", "/lab/api/uploads")


def create_app(services: AppServices | None = None) -> FastAPI:
    resolved_services = services or build_services()

    @asynccontextmanager
    async def lifespan(application: FastAPI) -> AsyncIterator[None]:
        application.state.services = resolved_services
        resolved_services.start()
        try:
            yield
        finally:
            resolved_services.close()

    application = FastAPI(
        title=resolved_services.config.app_name,
        description="EasyShare 知识平台计算面（解析、清洗、切块、索引与 RAG）",
        lifespan=lifespan,
    )
    application.state.services = resolved_services
    application.include_router(router)
    application.include_router(auth_router)
    application.include_router(lab_router)
    application.include_router(debug_router)

    @application.middleware("http")
    async def auth_middleware(request, call_next):
        # 带合法令牌一律解析出用户（auth 关闭时 /auth/me 等仍可用）；仅 auth_enabled 时强制保护业务前缀
        # 令牌两处可取：Authorization 头，或 GET 请求的 ?token=（供 clean.md 引用链接直开）
        auth_header = request.headers.get("authorization", "")
        token = auth_header[7:].strip() if auth_header.lower().startswith("bearer ") else ""
        if not token and request.method == "GET":
            token = request.query_params.get("token", "")
        if token and resolved_services.users is not None:
            user = await run_in_threadpool(resolved_services.users.resolve_token, token)
            if user:
                request.state.user = user
        if (
            resolved_services.config.auth_enabled
            and request.url.path.startswith(_PROTECTED_PREFIXES)
            and not hasattr(request.state, "user")
        ):
            return JSONResponse({"detail": "未登录或令牌无效"}, status_code=401)
        return await call_next(request)

    @application.get("/")
    def root() -> dict:
        return {
            "service": resolved_services.config.app_name,
            "docs": "/docs",
            "health": "/health",
            "local_lab": "/lab" if resolved_services.config.local_lab_enabled else None,
            "cockpit": "/lab/cockpit" if resolved_services.config.local_lab_enabled else None,
        }

    return application


app = create_app()
