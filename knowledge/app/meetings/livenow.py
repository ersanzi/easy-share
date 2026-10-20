"""实时字幕的文本后缀判定：流式预览与会中要点共用。

单独成模块而不是写在 stream.py 里，是因为 **livenow 热词快检**（`liveNow` 接口，
见 routes.py）也要用同一套「什么算半句/整句」的判定——两处规则一旦分叉，
上屏断句和「快检提示」就会自相矛盾。
"""
from __future__ import annotations

# 句末标点：出现即可安全断行
SENTENCE_ENDS = ("。", "？", "！", "?", "!")
# 逗号/顿号：已经形成一小块语义，落成一行（钉钉逐字稿也是按语义块断行，
# 不会把 40 字连成一行等句号）
CLAUSE_ENDS = ("，", "、", "；", ";", ",", "：", ":")

# 孤立语气字：流式模型常把「嗯/啊/对」切成单独一片，攒着不落行
_TAIL_FILLERS = ("嗯", "啊", "哦", "呃", "额", "唉", "诶")

# 行尾停在这些字上时，合成整行做快检的收益最大（半句话信息不足，模型会瞎猜）
_INCOMPLETE_TAILS = ("我", "你", "他", "她", "它", "们", "的", "了", "在", "是", "把", "被",
                     "和", "跟", "给", "对", "就", "都", "也", "还", "要", "会", "能", "这", "那")


def should_break(text: str) -> bool:
    """当前累积的这一句够不够长，可以落成一行。"""
    if not text:
        return False
    if text[-1] in SENTENCE_ENDS or text[-1] in CLAUSE_ENDS:
        return len(text) >= 2
    if text[-1] in _TAIL_FILLERS and len(text) <= 2:
        return False
    return len(text) >= 24


def strip_line(text: str) -> str:
    """落行的清理：去掉句末标点与空白（与离线转写保持同一展示口径）。"""
    return text.strip().strip("，。、；;：:！？!? ")


def is_incomplete_tail(text: str) -> bool:
    """行尾是不是「话还没说完」——虚词/代词收尾说明后面的词被截在了下一条。"""
    text = (text or "").strip()
    if not text:
        return True
    if text[-1] in SENTENCE_ENDS:
        return False
    return text[-1] in _INCOMPLETE_TAILS or text[-1] in CLAUSE_ENDS
