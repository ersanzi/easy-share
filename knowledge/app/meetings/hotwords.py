"""个性化热词（对标钉钉听记 hotwords）：员工自己的词表，转写时优先识别。

口径取自钉钉客户端文案（逆向证据 E18）：仅支持中英文日数字与空格，
含中文/日词的条目不超过 10 字，纯英文不超过 5 个单词，每人有总量上限。
"""
from __future__ import annotations

import json
import logging
import re
import threading
from pathlib import Path

logger = logging.getLogger(__name__)

MAX_WORDS_PER_USER = 100
MAX_CJK_CHARS = 10
MAX_ENGLISH_WORDS = 5
MAX_ENGLISH_CHARS = 40

# 允许的字符：中日韩、英文字母、数字、空格（其余直接拒收，避免把符号喂进模型）
_ALLOWED = re.compile(r"^[一-鿿぀-ヿ\w\s]+$" , re.UNICODE)
_HAS_CJK = re.compile(r"[一-鿿぀-ヿ]")


class HotwordError(ValueError):
    """校验不通过，消息即用户可见文案。"""


def validate(word: str) -> str:
    text = " ".join((word or "").split())
    if not text:
        raise HotwordError("热词不能为空")
    if not _ALLOWED.match(text):
        raise HotwordError("热词仅支持中文、英文、日文、数字和空格")
    if _HAS_CJK.search(text):
        if len(text) > MAX_CJK_CHARS:
            raise HotwordError(f"含中文或日文的热词不能超过 {MAX_CJK_CHARS} 个字符：{text}")
    else:
        if len(text.split()) > MAX_ENGLISH_WORDS:
            raise HotwordError(f"纯英文热词最多 {MAX_ENGLISH_WORDS} 个单词：{text}")
        if len(text) > MAX_ENGLISH_CHARS:
            raise HotwordError(f"纯英文热词总长度不能超过 {MAX_ENGLISH_CHARS} 个字符：{text}")
    return text


def normalize_many(words: object) -> list[str]:
    if isinstance(words, str):
        raw = [words]
    elif isinstance(words, (list, tuple, set)):
        raw = list(words)
    else:
        raise HotwordError("热词格式不正确")
    result: list[str] = []
    for item in raw:
        text = validate(str(item))
        if text not in result:
            result.append(text)
    return result


class HotwordStore:
    """按用户存放词表；文件型存储与听记记录一致（无数据库）。"""

    def __init__(self, path: str | Path) -> None:
        self._path = Path(path)
        self._lock = threading.Lock()
        if self._path.parent != Path(""):
            self._path.parent.mkdir(parents=True, exist_ok=True)

    def _read(self) -> dict:
        if not self._path.is_file():
            return {}
        try:
            data = json.loads(self._path.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError):
            logger.warning("热词文件损坏，按空表处理: %s", self._path)
            return {}
        return data if isinstance(data, dict) else {}

    def _write(self, data: dict) -> None:
        temporary = self._path.with_suffix(".json.tmp")
        temporary.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
        temporary.replace(self._path)

    def list(self, username: str | None) -> list[str]:
        return list(self._read().get(username or "shared") or [])

    def add(self, username: str | None, words: object) -> list[str]:
        incoming = normalize_many(words)
        key = username or "shared"
        with self._lock:
            data = self._read()
            merged = list(data.get(key) or [])
            for word in incoming:
                if word not in merged:
                    merged.append(word)
            if len(merged) > MAX_WORDS_PER_USER:
                raise HotwordError(f"热词最多 {MAX_WORDS_PER_USER} 个，当前已有 {len(merged) - len(incoming)} 个")
            data[key] = merged
            self._write(data)
            return merged

    def remove(self, username: str | None, word: str) -> list[str]:
        key = username or "shared"
        target = " ".join((word or "").split())
        with self._lock:
            data = self._read()
            current = list(data.get(key) or [])
            remaining = [item for item in current if item != target]
            data[key] = remaining
            self._write(data)
            return remaining

    def asr_phrase(self, username: str | None) -> str:
        """喂给 seaco 模型的热词串：本机 FunASR 实测只接受空格分隔字符串（传 list 会 TypeError）。"""
        return " ".join(self.list(username))
