"""FastAPI 入口。运行：uvicorn app.main:app --reload（在 knowledge/ 目录下）。"""
import logging

from fastapi import FastAPI

from app.api.routes import router
from app.config import settings

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")

app = FastAPI(title=settings.app_name, description="EasyShare 知识平台计算面（解析/知识库/RAG）")
app.include_router(router)


@app.get("/")
def root() -> dict:
    return {"service": settings.app_name, "docs": "/docs", "health": "/health"}
