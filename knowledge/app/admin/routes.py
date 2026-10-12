"""/admin：服务间管理端点（非用户令牌鉴权）。

目前仅 AI 配置下发：控制面保存 sys_config 后同步推送到这里，
X-Service-Secret 头与知识服务 .env 的 CONTROL_PLANE_SERVICE_SECRET 比对。
端点刻意不进 main.py 的用户令牌保护清单——它有独立的密钥鉴权。
"""
from __future__ import annotations

import hmac
import logging

from fastapi import APIRouter, HTTPException, Request, status
from pydantic import BaseModel, ConfigDict, Field

from app.ai_config import AiConfigStore, masked_key, normalize_overlay

logger = logging.getLogger(__name__)
router = APIRouter()


class AiConfigBody(BaseModel):
    """控制面下发的 AI 配置；空串字段=清除该覆盖项回退 .env（llmApiKey 除外）。

    别名收 camelCase（控制面/管理端的字段命名与 sys_config JSON 一致），
    populate_by_name 允许内部与测试用 snake_case 调用。
    """

    model_config = ConfigDict(populate_by_name=True)

    llm_base_url: str = Field("", alias="llmBaseUrl")
    llm_model: str = Field("", alias="llmModel")
    llm_api_key: str = Field("", alias="llmApiKey")
    asr_enabled: bool | None = Field(None, alias="asrEnabled")
    asr_speaker_enabled: bool | None = Field(None, alias="asrSpeakerEnabled")


@router.put("/admin/ai-config")
def update_ai_config(body: AiConfigBody, request: Request) -> dict:
    services = request.app.state.services
    expected = services.config.control_plane_service_secret
    provided = request.headers.get("x-service-secret", "")
    if not expected or not provided or not hmac.compare_digest(provided, expected):
        raise HTTPException(status.HTTP_403_FORBIDDEN, "服务密钥缺失或不匹配")

    raw = body.model_dump()
    if not raw["llm_api_key"]:
        # 密钥空串不走「清除」语义（防误触丢钥）：沿用现有覆盖值
        raw["llm_api_key"] = AiConfigStore(services.config.runtime_config_path).load().get("llm_api_key", "")
    overlay, errors = normalize_overlay(raw)
    if errors:
        raise HTTPException(status.HTTP_422_UNPROCESSABLE_ENTITY, "；".join(errors))

    saved = AiConfigStore(services.config.runtime_config_path).save(overlay)
    applied, warnings = services.reload_ai_components(saved)
    logger.info("AI 配置已下发并热重载: applied=%s warnings=%s", applied, warnings)
    return {
        "applied": applied,
        "warnings": warnings,
        "masked": {"llmApiKey": masked_key(services.config.llm_api_key)},
    }
