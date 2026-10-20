"""转写文本载入：把各种形态的输入统一成 Transcript。

支持的输入（README 有示例）：
  - JSON：本引擎导出的 `transcript.json`，或 EasyShare 听记记录的 `segments` + `speaker_names`
  - 纯文本：每行一条发言。自动识别 `张三 [00:12] 内容` / `张三: 内容` / `[00:12] 内容`
  - SRT / VTT：常见字幕导出（序号、时间轴、正文）
  - TextGrid / 其它：先转成上述任一形态
"""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path

from .models import Transcript, TranscriptTurn

# 「张三 [00:12] 内容」「张三 [00:01:02] 内容」
_SPEAKER_TS = re.compile(r"^\s*(?P<who>[^\[\]:：]{1,24}?)\s*[\[【]\s*(?P<ts>\d{1,2}:\d{2}(?::\d{2})?)\s*[\]】]\s*(?P<text>.*)$")
# 「张三: 内容」「张三：内容」
_SPEAKER_ONLY = re.compile(r"^\s*(?P<who>[^\s:：][^:：]{0,23})[:：]\s*(?P<text>.+)$")
# SRT/VTT 时间轴：00:00:01,200 --> 00:00:04,500
_SRT_TIME = re.compile(r"(\d{1,2}):(\d{2}):(\d{2})[,.](\d{1,3})\s*-->\s*(\d{1,2}):(\d{2}):(\d{2})[,.](\d{1,3})")


class TranscriptFormatError(ValueError):
    """输入既不像转写文本、也解析不出任何发言。"""


def _seconds(hours: str, minutes: str, seconds: str, millis: str = "0") -> float:
    return int(hours) * 3600 + int(minutes) * 60 + int(seconds) + int(millis.ljust(3, "0")) / 1000.0


def _mmss_to_seconds(text: str) -> float:
    parts = [int(part) for part in text.split(":")]
    if len(parts) == 2:
        return parts[0] * 60 + parts[1]
    return parts[0] * 3600 + parts[1] * 60 + parts[2]


def _speaker_index(who: str, registry: dict[str, str]) -> str:
    """把展示名映射回序号：「说话人 2」→ "1"，未知人名则分配新序号。"""
    match = re.match(r"^说话人\s*(\d+)$", who.strip())
    if match:
        return str(max(int(match.group(1)) - 1, 0))
    for index, name in registry.items():
        if name == who:
            return index
    index = str(len(registry))
    registry[index] = who
    return index


def _from_record(payload: dict) -> Transcript:
    """EasyShare 听记记录 / 引擎导出的 transcript.json 两种形状都吃。"""
    names = dict(payload.get("speaker_names") or {})
    raw_turns = payload.get("turns") or payload.get("segments") or []
    turns = [
        TranscriptTurn(
            start=float(item.get("start") or 0),
            end=float(item.get("end") or 0),
            text=str(item.get("text") or "").strip(),
            speaker=str(item.get("speaker", "0")),
        )
        for item in raw_turns
        if str(item.get("text") or "").strip()
    ]
    return Transcript(
        turns=turns,
        speaker_names=names,
        title=str(payload.get("title") or "会议纪要"),
        created_at=str(payload.get("created_at") or ""),
        duration_seconds=_infer_duration(payload.get("duration_seconds"), turns),
    )


def _infer_duration(given, turns: list[TranscriptTurn]) -> float | None:
    """时长：给的用给的，没给就从最后一句的时间戳推。

    实测量：Agent 组装的 request.json 里 `duration_seconds` 常是 `null`（ASR 没回填），
    于是一份 1 小时 22 分钟的会议在报告头部显示 0 分钟、每个议题的时间段也都是零长度。
    最后一句的 `end`（缺失时用 `start`）就是转写覆盖到的时刻，比缺省值可信得多。
    """
    try:
        value = float(given) if given is not None else 0.0
    except (TypeError, ValueError):
        value = 0.0
    if value > 0:
        return value
    if not turns:
        return given
    last = max(turns, key=lambda turn: max(turn.end or 0, turn.start))
    return float(max(last.end or 0, last.start)) or None


def _from_srt(text: str) -> Transcript:
    turns: list[TranscriptTurn] = []
    names: dict[str, str] = {}
    blocks = re.split(r"\r?\n\s*\r?\n", text.strip())
    for block in blocks:
        lines = [line for line in block.splitlines() if line.strip()]
        if not lines:
            continue
        stamp = next((line for line in lines if "-->" in line), None)
        if not stamp:
            continue
        match = _SRT_TIME.search(stamp)
        if not match:
            continue
        start = _seconds(*match.groups()[:4])
        body = " ".join(line for line in lines if line is not stamp and not line.strip().isdigit())
        if body:
            turns.append(TranscriptTurn(start=start, end=start, text=body, speaker="0"))
    return Transcript(turns=turns, speaker_names=names or {"0": "说话人 1"})


def _from_plain(text: str) -> Transcript:
    turns: list[TranscriptTurn] = []
    names: dict[str, str] = {}
    last_start = 0.0
    for raw in text.splitlines():
        line = raw.strip()
        if not line:
            continue
        stamped = _SPEAKER_TS.match(line)
        if stamped:
            start = _mmss_to_seconds(stamped.group("ts"))
            who = stamped.group("who").strip() or "说话人 1"
            text_part = stamped.group("text").strip()
        else:
            spoken = _SPEAKER_ONLY.match(line)
            if spoken and not re.match(r"^\d{1,2}:\d{2}$", line):
                who, text_part, start = spoken.group("who").strip(), spoken.group("text").strip(), last_start
            else:
                who, text_part, start = "说话人 1", line, last_start
        if not text_part:
            continue
        last_start = start
        turns.append(TranscriptTurn(start=start, end=start, text=text_part, speaker=_speaker_index(who, names)))
    if not turns:
        raise TranscriptFormatError("没有解析出任何发言：请提供 JSON（turns/segments）或带说话人/时间戳的文本")
    return Transcript(turns=turns, speaker_names=names)


def load_transcript(source: str | Path | dict) -> Transcript:
    """按输入形态载入转写；dict 直接当记录处理。

    另外支持 `-`：从标准输入读取（Agent 管道 `cat a.txt | minutecraft run -`）。
    """
    if isinstance(source, dict):
        return _from_record(source)
    if str(source) == "-":
        raw = sys.stdin.read()
    else:
        raw = Path(source).read_text(encoding="utf-8-sig")
    stripped = raw.lstrip()
    if stripped.startswith("{") or stripped.startswith("["):
        return _from_record(json.loads(raw))
    if "-->" in raw:
        return _from_srt(raw)
    return _from_plain(raw)


def transcript_to_dict(transcript: Transcript) -> dict:
    """引擎导出用的稳定形状（可再喂回 load_transcript）。"""
    return {
        "title": transcript.title,
        "created_at": transcript.created_at,
        "duration_seconds": transcript.duration_seconds,
        "speaker_names": transcript.speaker_names,
        "turns": [
            {"start": turn.start, "end": turn.end, "text": turn.text, "speaker": turn.speaker}
            for turn in transcript.turns
        ],
    }
