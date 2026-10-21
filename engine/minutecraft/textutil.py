"""文本小工具：语气词判定与中文/英文拼接。被 transcript（解析）与 models（材料渲染）共用。

单独成模块是为了避免 `models` 与 `transcript` 互相 import（transcript 依赖 models 的数据结构）。
"""
from __future__ import annotations

import re

# 纯语气词 / 应答词：ASR 按秒切段时会产生大量「嗯,」「行嘞行,」「好的好的,」这类碎句。
# 它们对纪要与议题毫无信息量，却在喂模型时按行计时（每行都有时间戳与说话人）——
# 一场两小时的会光这些就能占掉几千 tokens。只在**组装喂模型的材料**时丢弃，
# 原始 turns 一律保留（转写附页、导出 JSON 都还要用）。
FILLER_PUNCT = "，,。.、！!？?；;：:…—-~～ \t\r\n　\"'“”‘’（）()【】[]{}"
# 语气词按「词」匹配，而不是按单字：按字会把「个人」「好像」这类正常词误判。
# 正则把这些词任意拼接，覆盖「行嘞行」「好的好的」「对对对」「嗯」这类 ASR 碎句。
_FILLER_TOKENS = (
    "这个", "那个", "然后", "就是", "所以", "那么", "好的", "好吧", "行吧", "可以",
    "明白", "收到", "谢谢", "没事", "嗯", "恩", "呃", "额", "啊", "哦", "噢", "喔",
    "唉", "诶", "欸", "呀", "哈", "呵", "嘿", "哎", "唔", "呐", "哪", "吧", "嘛",
    "啦", "咯", "嘞", "的", "了", "行", "好", "对", "是",
)
_FILLER_RE = re.compile(r"^(?:" + "|".join(sorted(_FILLER_TOKENS, key=len, reverse=True)) + r")+$")
_FILLER_WORDS = set(_FILLER_TOKENS)


def is_filler(text: str) -> bool:
    """是不是纯语气词/应答词（「嗯」「行嘞行」「好的好的」「谢谢」…）。"""
    stripped = text.strip().strip(FILLER_PUNCT)
    if not stripped:
        return True
    if len(stripped) > 10:
        return False
    return bool(_FILLER_RE.match(stripped))


def join_text(left: str, right: str) -> str:
    """拼接相邻转写片段：中文直接相连，两侧都是英文/数字时才补空格。

    ASR 会在词中间断句（「…演示脚本我，来准备今，天下班前…」），中文侧加空格会把
    断点变成看得见的噪声；英文侧不加空格又会粘成一个词，所以按字符类型决定。
    """
    if not left:
        return right
    if not right:
        return left
    if left[-1].isascii() and right[0].isascii() and left[-1] not in " ":
        return f"{left} {right}"
    return left + right


# 句末标点。**故意不含 ASCII 句点**：ASR 正文里「3.5」「v1.0」这类版本号会被当成句子结束，
# 切出来的首句就成了「版本 3.」。中文句子绝大多数以「。」收尾，够用了。
_SENTENCE_END_RE = re.compile(r"[。！？；]|[!?;]")


def head_sentence(text: str, limit: int = 60) -> str:
    """取一段话的**开头句**（用于「大纲腿」这类只要锚点与句意的材料）。

    合并后的段落可能有 200 多字，只留首句能再砍一半以上；超过 `limit` 时退到最近的逗号，
    避免硬切在词中间。
    """
    stripped = text.strip()
    match = _SENTENCE_END_RE.search(stripped)
    head = stripped[: match.end()] if match else stripped
    if len(head) > limit:
        cut = head[:limit].rfind("，")
        head = head[: cut + 1] if cut > limit // 3 else head[:limit]
    return head
