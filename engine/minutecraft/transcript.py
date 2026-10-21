"""转写文本载入：把各种形态的输入统一成 Transcript。

支持的输入（README 有示例）：
  - JSON：本引擎导出的 `transcript.json`，或 EasyShare 听记记录的 `segments` + `speaker_names`
  - 纯文本：每行一条发言。自动识别
      `张三 [00:12] 内容` / `[00:12] 内容`（单时间戳）
      `[00:00 - 00:03] 内容` / `张三 [00:00 - 00:03] 内容`（起止区间，ASR 分段导出常见）
      `00:00 - 00:03 内容`（区间不带括号）、`[00:00:01,200 --> 00:00:04,500] 内容`
      `张三: 内容` / `[00:00 - 00:03] 张三：内容`（说话人在正文里）
  - SRT / VTT：常见字幕导出（序号、时间轴、正文）
  - TextGrid / 其它：先转成上述任一形态
"""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path

from .models import Transcript, TranscriptTurn

# 一段时间的统一写法：mm:ss / hh:mm:ss / mm:ss.500 / hh:mm:ss,500
_TS = r"\d{1,3}(?::\d{1,2}){1,2}(?:[.,]\d{1,3})?"
# 起止区间的分隔符：ASR 导出里常见 - – — ~ ～ --> -> 至 到
_TIME_SEP = r"(?:-->|->|==>|[-–—~～]|至|到)"
_OPEN = r"\[【(（"
_CLOSE = r"\]】)）"

# 「[00:00 - 00:03] 内容」「张三 [00:00 - 00:03] 内容」
# 说话人必须写在括号前；不写就整个当正文，避免把时间戳的前半截吃成说话人
_RANGE_TS = re.compile(
    r"^\s*(?:(?P<who>[^\[\]:：]{1,24}?)\s*)?[" + _OPEN + r"]\s*"
    r"(?P<start>" + _TS + r")\s*" + _TIME_SEP + r"\s*(?P<end>" + _TS + r")\s*[" + _CLOSE + r"]\s*"
    r"(?P<text>.*)$"
)
# 「00:00 - 00:03 内容」（区间不带括号）
_BARE_RANGE = re.compile(
    r"^\s*(?P<start>" + _TS + r")\s*" + _TIME_SEP + r"\s*(?P<end>" + _TS + r")\s*(?P<text>.*)$"
)
# 「[00:12] 内容」「张三 [00:12] 内容」「【00:12】内容」
_TS_ONLY = re.compile(
    r"^\s*(?:(?P<who>[^\[\]:：]{1,24}?)\s*)?[" + _OPEN + r"]\s*(?P<ts>" + _TS + r")\s*[" + _CLOSE + r"]\s*"
    r"(?P<text>.*)$"
)
# 「张三: 内容」「张三：内容」
_SPEAKER_ONLY = re.compile(r"^\s*(?P<who>[^\s:：][^:：]{0,23})[:：]\s*(?P<text>.+)$")
# 「[说话人 1] 内容」「【张三】内容」
_BRACKETED_WHO = re.compile(
    r"^\s*[" + _OPEN + r"]\s*(?P<who>[^\[\]【】()（）]{1,24})\s*[" + _CLOSE + r"]\s*[:：]?\s*(?P<text>.*)$"
)
# SRT/VTT 时间轴：00:00:01,200 --> 00:00:04,500
_SRT_TIME = re.compile(r"(\d{1,2}):(\d{2}):(\d{2})[,.](\d{1,3})\s*-->\s*(\d{1,2}):(\d{2}):(\d{2})[,.](\d{1,3})")
# 时间轴独占一行（后面没有正文）才是字幕块；单行 `[00:00:01,200 --> 00:00:04,500] 内容`
# 后面紧跟正文，属于普通时间戳行，不能整份交给字幕解析
_SRT_BARE_LINE = re.compile(r"^\s*" + _TS + r"\s*-->\s*" + _TS + r"\s*$")

# 说话人标签：角色名、2-4 字中文名、英文名。用「像名字」而不是「冒号前的一切」来判定，
# 否则「那我们开始吧，今天主要过三件事：…」会把前半句当成说话人，正文被截掉。
_ROLE_LABEL = re.compile(
    r"(?:说话人|发言人|主持人|嘉宾|讲者|参会人|Speaker|Host|Guest)\s*[A-Za-z0-9]*", re.IGNORECASE
)
_ROLE_WITH_NAME = re.compile(r"(?:主持人|嘉宾|讲者|发言人|参会人)[\s·]*[\u4e00-\u9fff]{2,4}")
_CJK_NAME = re.compile(r"[\u4e00-\u9fff]{2,4}")
_LATIN_NAME = re.compile(r"[A-Za-z][A-Za-z0-9 .'_-]{0,19}")
# 常见句首词不是人名（「结论：」「建议：」这类冒号句在口述里很常见）
_NOT_NAMES = {
    "结论", "建议", "问题", "原因", "方案", "总结", "背景", "目标", "结果", "重点",
    "注意", "其实", "然后", "那么", "所以", "因为", "但是", "如果", "另外", "大家",
    "我们", "今天", "明天", "下周", "先说", "简单", "首先", "其次", "最后",
}


def _looks_like_speaker(who: str) -> bool:
    """冒号/括号前的那段是不是说话人标签。

    只认三类：角色名（说话人 1 / Speaker 2）、2-4 字中文名、英文名。
    判不准就返回 False——宁可把前缀留在正文里，也不要吃掉它。
    """
    base = re.sub(r"[（(][^)）]*[)）]\s*$", "", (who or "").strip()).strip()
    if not base or len(base) > 24 or base in _NOT_NAMES:
        return False
    if _ROLE_LABEL.fullmatch(base) or _ROLE_WITH_NAME.fullmatch(base):
        return True
    if _CJK_NAME.fullmatch(base):
        return True
    return bool(_LATIN_NAME.fullmatch(base))


class TranscriptFormatError(ValueError):
    """输入既不像转写文本、也解析不出任何发言。"""


def _seconds(hours: str, minutes: str, seconds: str, millis: str = "0") -> float:
    return int(hours) * 3600 + int(minutes) * 60 + int(seconds) + int(millis.ljust(3, "0")) / 1000.0


def _mmss_to_seconds(text: str) -> float:
    """`mm:ss` / `hh:mm:ss` / `mm:ss.500` → 秒（小数毫秒也收）。"""
    parts = [part.strip() for part in text.replace(",", ".").split(":")]
    if len(parts) == 3:
        hours, minutes, seconds = parts
    elif len(parts) == 2:
        hours, minutes, seconds = "0", parts[0], parts[1]
    else:
        return float(parts[0] or 0)
    return int(hours or 0) * 3600 + int(minutes or 0) * 60 + float(seconds or 0)


def _speaker_index(who: str, registry: dict[str, str]) -> str:
    """把展示名映射回序号：「说话人 2」→ "1"，未知人名则分配新序号。"""
    match = re.match(r"^说话人\s*(\d+)$", who.strip())
    if match:
        index = str(max(int(match.group(1)) - 1, 0))
        occupant = registry.get(index)
        # 只有该序号还空着、或原本就是「说话人 N」时才复用；
        # 否则（0 号已经是「张三」）另分配，避免把两个不同的人并成一个人
        if occupant is None or re.match(r"^说话人\s*\d+$", occupant):
            registry[index] = who
            return index
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
        end = _seconds(*match.groups()[4:8])
        # 时间轴和正文写在同一行（`[00:00:01,200 --> 00:00:04,500] 内容`）时，
        # 旧实现把整行当时间轴、正文取空，整条就被丢掉了
        tail = stamp[match.end():].strip().strip("]】)）").strip()
        body = " ".join(
            ([tail] if tail else []) + [
                line for line in lines if line is not stamp and not line.strip().isdigit()
            ]
        )
        if body:
            turns.append(TranscriptTurn(start=start, end=max(end, start), text=body, speaker="0"))
    return Transcript(
        turns=turns,
        speaker_names=names or {"0": "说话人 1"},
        duration_seconds=_infer_duration(None, turns),
    )


def _split_inline_speaker(who: str, text: str) -> tuple[str, str]:
    """把正文里的说话人标签拆出来：`[说话人 1] 内容` / `张三：内容`。

    `who` 是从时间轴前面取到的那段，只有它**像名字**才认；正文里的冒号前缀同理。
    判不准就把整段留在正文里——截掉前半句比多一个假说话人严重得多。
    """
    if who and _looks_like_speaker(who):
        return who.strip(), text
    bracketed = _BRACKETED_WHO.match(text)
    if bracketed and _looks_like_speaker(bracketed.group("who")):
        return bracketed.group("who").strip(), bracketed.group("text").strip()
    spoken = _SPEAKER_ONLY.match(text)
    if spoken and _looks_like_speaker(spoken.group("who")):
        return spoken.group("who").strip(), spoken.group("text").strip()
    return "", text


def _parse_plain_line(line: str, last_start: float) -> tuple[str, float, float, str]:
    """解析一行纯文本，返回（说话人, 开始秒, 结束秒, 正文）。

    优先级：带括号的区间 → 不带括号的区间 → 单时间戳 → 说话人冒号 → 裸文本。
    区间必须排在单时间戳前，否则 `[00:00 - 00:03]` 会被当成「说话人 + 单时间戳」。
    """
    ranged = _RANGE_TS.match(line) or _BARE_RANGE.match(line)
    if ranged:
        start = _mmss_to_seconds(ranged.group("start"))
        end = max(_mmss_to_seconds(ranged.group("end")), start)
        who, content = _split_inline_speaker(
            ranged.groupdict().get("who") or "", ranged.group("text").strip()
        )
        return (who or "说话人 1"), start, end, content
    stamped = _TS_ONLY.match(line)
    if stamped:
        start = _mmss_to_seconds(stamped.group("ts"))
        who, content = _split_inline_speaker(stamped.group("who") or "", stamped.group("text").strip())
        return (who or "说话人 1"), start, start, content
    spoken = _SPEAKER_ONLY.match(line)
    if spoken and _looks_like_speaker(spoken.group("who")):
        return spoken.group("who").strip(), last_start, last_start, spoken.group("text").strip()
    return "说话人 1", last_start, last_start, line


def _from_plain(text: str) -> Transcript:
    turns: list[TranscriptTurn] = []
    names: dict[str, str] = {}
    last_start = 0.0
    for raw in text.splitlines():
        line = raw.strip()
        if not line:
            continue
        who, start, end, content = _parse_plain_line(line, last_start)
        if not content:
            continue
        last_start = start
        turns.append(
            TranscriptTurn(start=start, end=end, text=content, speaker=_speaker_index(who, names))
        )
    if not turns:
        raise TranscriptFormatError(
            "没有解析出任何发言：请提供 JSON（turns/segments）或带说话人/时间戳的文本"
        )
    return Transcript(turns=turns, speaker_names=names, duration_seconds=_infer_duration(None, turns))


def _looks_like_json(raw: str) -> bool:
    """是不是 JSON 输入。

    **不能只看首字符**：ASR 导出的转写常以 `[00:00 - 00:03]` 开头，首字符也是 `[`，
    旧实现直接当 JSON 解析，报一句 `Expecting ',' delimiter` —— 看着像文件坏了，
    实际是纯文本。这里先按 JSON 试解析，失败就老老实实走文本解析。
    """
    stripped = raw.lstrip().lstrip("\ufeff")
    if stripped.startswith("{"):
        return True
    if not stripped.startswith("["):
        return False
    try:
        json.loads(raw)
    except json.JSONDecodeError:
        return False
    return True


def load_transcript(source: str | Path | dict) -> Transcript:
    """按输入形态载入转写；dict 直接当记录处理。

    另外支持 `-`：从标准输入读取（Agent 管道 `cat a.txt | minutecraft run -`）。
    """
    if isinstance(source, dict):
        return _from_record(source)
    if isinstance(source, list):
        # 裸数组 = turns 列表（某些工具直接导出一个数组），补个壳再走同一条路径
        return _from_record({"turns": source})
    if str(source) == "-":
        raw = sys.stdin.read()
    else:
        raw = Path(source).read_text(encoding="utf-8-sig")
    if _looks_like_json(raw):
        data = json.loads(raw)
        if isinstance(data, list):
            return _from_record({"turns": data})
        if isinstance(data, dict):
            return _from_record(data)
        raise TranscriptFormatError("JSON 输入必须是对象或发言数组")
    if "-->" in raw and any(_SRT_BARE_LINE.match(line) for line in raw.splitlines()):
        # 时间轴独占一行 = 标准字幕块，交给 SRT 解析（正文在下一行）；
        # 只有单行 `[... --> ...] 正文` 时按普通文本解析，SRT 取不到那种写法的正文
        srt = _from_srt(raw)
        if srt.turns:
            return srt
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
