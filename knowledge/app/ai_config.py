"""AI 能力运行时配置：控制面（RuoYi sys_config）下发的覆盖层。

优先级：runtime overlay（data/runtime_config.json，控制面下发）> .env 默认值。
知识服务重启后 overlay 仍在（数据目录持久化）；控制面保存时同步推送到
PUT /admin/ai-config（X-Service-Secret 头），推送失败由控制面提示重存。

覆盖字段（缺省=沿用 .env）：llmBaseUrl / llmModel / llmApiKey /
asrEnabled / asrSpeakerEnabled。embedding/rerank 刻意不开放——改 dim 会
损坏既有向量索引，属部署期配置。
"""
from __future__ import annotations

import json
import logging
import threading
from pathlib import Path

from app.config import Settings

logger = logging.getLogger(__name__)

# 允许通过管理端覆盖的字段白名单（Settings 字段名）
OVERRIDABLE_FIELDS = (
    "llm_base_url",
    "llm_model",
    "llm_api_key",
    "asr_enabled",
    "asr_speaker_enabled",
)


class AiConfigStore:
    """runtime overlay 的读写；原子落盘。"""

    def __init__(self, path: str | Path) -> None:
        self._path = Path(path)
        self._lock = threading.Lock()

    def load(self) -> dict:
        try:
            data = json.loads(self._path.read_text(encoding="utf-8"))
        except FileNotFoundError:
            return {}
        except (json.JSONDecodeError, OSError):
            logger.warning("运行时 AI 配置损坏，忽略: %s", self._path)
            return {}
        return {key: data[key] for key in OVERRIDABLE_FIELDS if key in data}

    def save(self, overlay: dict) -> dict:
        clean = {key: overlay[key] for key in OVERRIDABLE_FIELDS if key in overlay}
        with self._lock:
            self._path.parent.mkdir(parents=True, exist_ok=True)
            temporary = self._path.with_suffix(".json.tmp")
            temporary.write_text(json.dumps(clean, ensure_ascii=False, indent=2), encoding="utf-8")
            temporary.replace(self._path)
        return clean


def apply_overlay(settings: Settings, overlay: dict) -> Settings:
    """把 overlay 合并进 Settings，返回新实例（原实例不动）。

    空串/None 字段视为「清除覆盖」回退 .env 值——由 store.load 时不写入
    空值保证；这里的 update 只接受白名单内的非空值与布尔值。
    """
    updates = {
        key: value
        for key, value in overlay.items()
        if key in OVERRIDABLE_FIELDS and value is not None and value != ""
    }
    if not updates:
        return settings
    return settings.model_copy(update=updates)


def masked_key(key: str) -> str:
    """密钥掩码展示：只露末 4 位。"""
    if not key:
        return ""
    return "****" + key[-4:] if len(key) > 8 else "****"


def normalize_overlay(raw: dict) -> tuple[dict, list[str]]:
    """校验控制面下发的配置体，返回 (干净 overlay, 错误列表)。

    语义：字符串字段空串=清除覆盖（回退 .env）；llm_api_key 空串=保持现有
    覆盖不变（密钥不走清空语义，防误触丢钥），由调用方在合并旧值后传入。
    """
    errors: list[str] = []
    clean: dict = {}
    for field in ("llm_base_url", "llm_model"):
        value = (raw.get(field) or "").strip()
        if value and not value.startswith(("http://", "https://")) and field == "llm_base_url":
            errors.append("llmBaseUrl 必须以 http:// 或 https:// 开头")
            continue
        clean[field] = value
    key = (raw.get("llm_api_key") or "").strip()
    if key:
        clean["llm_api_key"] = key
    for field in ("asr_enabled", "asr_speaker_enabled"):
        if field in raw and raw[field] is not None:
            clean[field] = bool(raw[field])
    return clean, errors
