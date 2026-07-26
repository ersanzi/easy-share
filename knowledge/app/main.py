"""FastAPI 入口。运行：uvicorn app.main:app --reload（在 knowledge/ 目录下）。"""
from __future__ import annotations

import logging
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import FastAPI

from app.api.routes import router
from app.lab.routes import router as lab_router
from app.services import AppServices, build_services

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")


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
    application.include_router(lab_router)

    @application.get("/")
    def root() -> dict:
        return {
            "service": resolved_services.config.app_name,
            "docs": "/docs",
            "health": "/health",
            "local_lab": "/lab" if resolved_services.config.local_lab_enabled else None,
        }

    return application


app = create_app()
