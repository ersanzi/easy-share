"""minutecraft 内核：转写文本 → 会议纪要（可被知识服务、CLI、外部 Agent 共同调用）。

一次 `build()` 的产出分三块：
  - summary     ：纪要/要点/待办/章节/关键词/AI洞察/核心发言/可视化报告（LLM 通读产出）
  - mindmap     ：markdown 层级大纲（三阶段 harness，失败回退单次生成）
  - exports     ：写盘的文件（docx / md / json）

设计取舍：
  * **不碰音频**。此处只处理文本；ASR 在采集端（EasyShare 知识服务）或可选的 `minutecraft.asr`。
  * **每一块独立可失败**。LLM 挂了不让整条链路崩：总结失败抛 MinuteError，但调用方可以先拿
    chunks；关键词/导图失败只记进 `errors`，其余产物照常产出（对标听记的区块级状态机）。
  * **零框架依赖**。只用 openai + python-docx，方便在任意 Agent 环境里跑。
"""
from __future__ import annotations

import json
import logging
import os
from dataclasses import dataclass, field
from pathlib import Path

from .docx import build_minutes_docx
from .mindmap_harness import build_mindmap_harness
from .models import Transcript
from .render import build_report
from .summarizer import MeetingSummarizer, chapters_from_topics, mindmap_from_topics
from .templates import find_template
from .transcript import load_transcript, transcript_to_dict

# 请求包版本号：宿主 Agent 可能把 prepare 产物存起来、过一阵再 finish，
# 带上版本便于将来格式变化时报出可读的错，而不是键缺失。
PREPARE_VERSION = "1"

logger = logging.getLogger(__name__)


class MinuteError(RuntimeError):
    """引擎级失败（LLM 未配置 / 总结生成失败）。"""


@dataclass
class MinuteResult:
    """一次生成的全部产物。字段名即 `SCHEMA["output"]` 的键。"""

    transcript: Transcript
    summary: dict = field(default_factory=dict)
    todos: list[dict] = field(default_factory=list)
    keywords: list[str] = field(default_factory=list)
    mindmap: str = ""
    template_id: str = "meeting-minutes"
    warnings: list[str] = field(default_factory=list)
    files: dict[str, str] = field(default_factory=dict)

    def to_dict(self) -> dict:
        return {
            "title": self.transcript.title,
            "created_at": self.transcript.created_at,
            "duration_seconds": self.transcript.duration_seconds,
            "speaker_names": self.transcript.speaker_names,
            "template_id": self.template_id,
            "summary": self.summary,
            "todos": self.todos,
            "keywords": self.keywords,
            "mindmap": self.mindmap,
            "warnings": self.warnings,
            "files": self.files,
        }


def _env(name: str, fallback: str = "") -> str:
    return os.environ.get(name) or (os.environ.get(fallback) if fallback else "")


@dataclass
class LLMSettings:
    """LLM 连接参数；缺省从环境变量读（OpenAI 兼容）。"""

    base_url: str = ""
    api_key: str = ""
    model: str = ""
    trust_env: bool = True
    # 思考档位：auto（默认）= 按转写长短自动选档，也可以钉成 none/low/medium/high。
    # 这是唯一有效的提速开关，实测见 summarizer 顶部注释。
    thinking: str = ""

    @classmethod
    def from_env(cls) -> LLMSettings:
        trust = (_env("MINUTECRAFT_LLM_TRUST_ENV") or "true").strip().lower()
        return cls(
            base_url=_env("MINUTECRAFT_LLM_BASE_URL", "LLM_BASE_URL"),
            api_key=_env("MINUTECRAFT_LLM_API_KEY", "LLM_API_KEY"),
            model=_env("MINUTECRAFT_LLM_MODEL", "LLM_MODEL"),
            trust_env=trust not in ("0", "false", "no"),
            thinking=(_env("MINUTECRAFT_THINKING") or "").strip().lower(),
        )

    @property
    def configured(self) -> bool:
        return bool(self.base_url and self.api_key and self.model)


def _answer_text(value) -> str:
    """回答值归一成「原始文本」。

    `answers.json` 里的值本该是模型原始输出（字符串），但 Agent 写起来很容易直接把解析好的
    JSON 对象塞进来（`{"keywords": ["a","b"]}` 或 `{"summary": {...}}`）。原实现直接 `.strip()`，
    对 dict/list 会抛 `AttributeError: 'list' object has no attribute 'strip'`——**一条看不出
    该怎么改的堆栈**。这里两种形态都收：非字符串就序列化回 JSON 文本，再走同一套解析。
    """
    if isinstance(value, str):
        return value
    if value is None:
        return ""
    return json.dumps(value, ensure_ascii=False)


def _answer_list(value) -> str:
    """裸数组（`{"keywords": ["a","b"]}`）是 Agent 最常见的写法。

    它会被解析成「没有 keywords 字段」而静默降级，所以先包成 `{"keywords": [...]}`
    再交给解析器，保证这种写法**真的能生效**，而不是靠 warnings 才发现丢了。
    """
    text = _answer_text(value)
    if isinstance(value, list):
        return json.dumps({"keywords": value}, ensure_ascii=False)
    return text


def _slice_transcript(transcript: Transcript, start: float, end: float, margin: float) -> Transcript:
    """取某议题时间窗附近的发言（前后各留 margin 秒上下文）。

    议题边界是模型切的，首尾句常被切在句子中间；留一点重叠比严格裁剪更能保住上下文。
    时间轴保持原值不变——扩写回答里的时间仍可直接对回整场会议。
    """
    lower = max(0.0, start - margin)
    upper = end + margin
    return Transcript(
        turns=[turn for turn in transcript.turns if turn.end >= lower and turn.start <= upper],
        speaker_names=transcript.speaker_names,
        title=transcript.title,
        created_at=transcript.created_at,
        duration_seconds=transcript.duration_seconds,
    )


class MinuteEngine:
    """引擎门面。构造一次可反复调用（LLM 客户端按需创建）。"""

    def __init__(self, settings: LLMSettings | None = None) -> None:
        self.settings = settings or LLMSettings.from_env()
        self._summarizer = MeetingSummarizer(
            self.settings.base_url, self.settings.api_key, self.settings.model,
            trust_env=self.settings.trust_env, thinking=self.settings.thinking or None,
        )

    @property
    def configured(self) -> bool:
        return self.settings.configured

    # ── 采集端（知识服务）复用的 LLM 询问器：把已有 summarizer 注进来，避免重复读环境 ──
    def use_summarizer(self, summarizer: MeetingSummarizer) -> MinuteEngine:
        self._summarizer = summarizer
        return self

    @property
    def summarizer(self) -> MeetingSummarizer:
        return self._summarizer

    def build(
        self,
        transcript: Transcript,
        *,
        template_id: str = "meeting-minutes",
        with_keywords: bool = True,
        with_mindmap: bool = True,
        with_docx: bool = True,
        with_transcript_page: bool = False,
        out_dir: str | Path | None = None,
        with_topics: bool = True,
        with_compact_material: bool = True,
    ) -> MinuteResult:
        """自带 LLM 客户端的完整流程（无人值守/批量场景用）。

        **Agent 场景请用 prepare() + finish() 两步走**：Agent 用自己的模型能力回答，
        不必再申请一把 key。本方法内部就是这两步的组合，产物完全一致。
        """
        if not self.configured:
            raise MinuteError(
                "LLM 未配置：请设置 MINUTECRAFT_LLM_BASE_URL / MINUTECRAFT_LLM_API_KEY / MINUTECRAFT_LLM_MODEL；"
                "或者改用两步走（prepare → 你的 agent 回答 → finish），服务型 Agent 无需任何配置"
            )
        template = find_template(template_id) or find_template("meeting-minutes")
        material = transcript.render(compact=with_compact_material)
        if not material.strip():
            raise MinuteError("转写为空，没有可生成的会议内容")

        result = MinuteResult(transcript=transcript, template_id=str(template["id"]))

        summary = self._summarizer.summarize(material, template)
        if not summary:
            raise MinuteError("总结生成失败（LLM 调用失败或返回不可解析），可稍后重试")
        # 待办与导图在产物里是一等实体，摘出去单独放（与听记的存储口径一致）
        result.todos = summary.pop("todos", None) or []
        inline_mindmap = summary.pop("mindmap", None) or ""
        inline_keywords = summary.pop("keywords", None) or []
        result.summary = summary

        if with_keywords:
            # 总结回答里自带关键词就不用再单独问一次（省一次完整转写的调用）
            if inline_keywords:
                result.keywords = inline_keywords
            else:
                keywords = self._summarizer.extract_keywords(material)
                if keywords is None:
                    result.warnings.append("关键词生成失败（不影响纪要）")
                else:
                    result.keywords = keywords

        # 报告议题：独立一次调用（失败只丢长图正文，纪要照常）。
        # 用 getattr 探测：外部注入的 summarizer（知识服务可以塞自己的实现）未必有这个方法，
        # 缺了就当「没生成」降级，绝不能因为一份长图把一个已经成功的 build 打成异常。
        extract_topics = getattr(self._summarizer, "extract_topics", None)
        if not with_topics:
            result.warnings.append("已跳过报告议题（--no-topics）：长图将按章节均分")
        elif callable(extract_topics):
            topics = extract_topics(material)
            if topics:
                result.summary["topics"] = topics
            else:
                result.warnings.append("报告议题生成失败（长图将按章节均分，不影响纪要）")

        if with_mindmap:
            result.mindmap = self._make_mindmap(transcript, inline_mindmap, result)

        if out_dir is not None:
            result.files = self.write_outputs(result, out_dir, with_docx=with_docx,
                                              with_transcript_page=with_transcript_page)
        return result

    # ── 两步走：prepare（纯工程）→ 调用方回答 → finish（纯工程）──
    #
    # 为什么拆：作为 skill 使用时，模型能力由宿主 Agent 提供，再要一把 key 是重复且更危险
    # （多一处密钥落点）。拆开后本工程只做确定性工作，模型调用归宿主。

    def prepare(
        self,
        transcript: Transcript,
        *,
        template_id: str = "meeting-minutes",
        with_keywords: bool = True,
        with_topics: bool = True,
        with_compact_material: bool = True,
        split_topics: bool = False,
    ) -> dict:
        """生成待回答的请求包。**不需要任何 LLM 配置。**

        返回结构（可 json.dump 落盘）：
            {"version", "transcript"（原样带回，finish 需要）, "template",
             "material": {"chars", "turns", "lines", "compact"},
             "plan": {"calls", "parallel"},
             "requests": {"summary": {...}, "topics": {...}}}

        速度取舍（默认值就是快的那条路）：
        - **关键词合并进 summary**：不再单独发一次关键词请求。少一次调用 = 少一个 Agent 来回。
        - **章节/导图不重复写**：开启 topics 时 summary 只写纪要与汇报卡，`chapters` /
          `mindmap` 由 finish 从议题派生（实测这两项占 summary 回答约 1/3 的 output token）。
        - **材料压缩**（`with_compact_material`）：合并同一说话人的连续短句、丢掉「嗯/好的」
          这类语气词碎句，只在段首留时间戳。转写 token 通常降 30%-50%，模型也少受噪音干扰。
        - `plan.parallel=True` 表示请求之间互不依赖，应当**并发发出**。

        `split_topics=True` 时只产出议题大纲请求（`topics_outline`），长会的正文扩写交给
        `expand()` 拆成每议题一个小请求——支持并发的宿主能把墙钟时间从「一次长调用」降到
        「大纲 + 最慢的一个议题」。

        `topics` 是长网页报告的正文骨架（议题 → 分节 → 要点），可选；宿主不回答它时
        finish 会回退到「按章节均分 visual」的旧渲染路径，不会失败。
        """
        template = find_template(template_id) or find_template("meeting-minutes")
        material = transcript.render(compact=with_compact_material)
        if not material.strip():
            raise MinuteError("转写为空，没有可生成的会议内容")
        requests: dict[str, dict] = {
            "summary": self._summarizer.build_request(
                material, template, with_keywords=with_keywords, derive_from_topics=with_topics
            )
        }
        # with_topics=False 用于「只要 docx/md，不要长图」：议题是最慢的一次调用（实测 250-300s）
        if with_topics:
            if split_topics:
                requests["topics_outline"] = self._summarizer.topics_outline_request(material)
            else:
                requests["topics"] = self._summarizer.topics_request(material)
        note = (
            "summary 与议题大纲互不依赖，请并发发出；答完大纲后执行 minutecraft expand 拆出各议题扩写请求"
            if split_topics
            else "summary 与 topics 互不依赖，请并发发出；topics 最慢（长会约 4 分钟），不要串行等它"
        )
        return {
            "version": PREPARE_VERSION,
            "transcript": transcript_to_dict(transcript),
            "template": template["id"],
            # finish 据此判断「summary 里没有 chapters/mindmap」是正常的派生路径，
            # 而不是宿主漏答；漏答 topics 时要给出明确的降级警告。
            "derive_from_topics": with_topics,
            "topics_mode": "outline" if (with_topics and split_topics) else ("single" if with_topics else "none"),
            "material": {
                "chars": len(material),
                "lines": material.count("\n") + 1,
                "turns": len(transcript.turns),
                "compact": with_compact_material,
            },
            "plan": {
                "calls": list(requests),
                "parallel": len(requests) > 1,
                "derive_from_topics": with_topics,
                "note": note,
            },
            "requests": requests,
        }

    def expand(self, payload: dict, outline_answer: str, *, margin: float = 30.0) -> dict:
        """把「议题大纲回答」拆成每议题一个扩写请求（两阶段议题的第二阶段）。

        **不需要 LLM 配置，也不调模型。** 每个请求只带该议题时间窗（前后各留 `margin` 秒
        上下文）的转写，输出只需正文三件套，比整份 topics 短得多；请求之间互不依赖，
        支持并发的宿主应全部并行发出。

        返回的新请求包仍可直接交给 `finish`：`topic_outline` 保存大纲，
        `requests` 的键是 `topics#1`…`topics#N`，宿主按同名键写进 answers.json。
        """
        transcript = load_transcript(payload.get("transcript") or {})
        outline = self._summarizer.topics_from_answer(outline_answer)
        if not outline:
            raise MinuteError(
                "议题大纲无法解析：需要 {\"topics\": [{\"title\", \"start\", \"end\", \"gist\"}]}"
            )
        requests: dict[str, dict] = {}
        for index, topic in enumerate(outline, 1):
            start = int(topic.get("start") or 0)
            end = int(topic.get("end") or start)
            if end <= start:
                if index < len(outline):
                    end = int(outline[index].get("start") or start)
                else:
                    end = int(transcript.duration_seconds or start)
            piece = _slice_transcript(transcript, start, end, margin)
            requests[f"topics#{index}"] = self._summarizer.topic_expand_request(
                topic, piece.render(), index, len(outline)
            )
        expanded = dict(payload)
        expanded["topics_mode"] = "split"
        expanded["topic_outline"] = outline
        expanded["requests"] = requests
        expanded["plan"] = {
            "calls": list(requests),
            "parallel": True,
            "note": "各议题扩写互不依赖，请并发发出；每个请求只带该议题时间窗，输出比整份 topics 短得多",
        }
        return expanded

    def finish(
        self,
        payload: dict,
        answers: dict[str, str],
        *,
        with_keywords: bool = True,
        with_docx: bool = True,
        with_transcript_page: bool = False,
        out_dir: str | Path | None = None,
    ) -> MinuteResult:
        """用调用方（Agent）给出的模型原始回答生成全部产物。**不需要 LLM 配置。**

        `answers` 形如 {"summary": "<模型原始输出>", "keywords": "<可选>", "topics": "<可选>"}；
        解析归一兜底都在这里做，因此无论哪个模型回答，结构都一致。
        """
        transcript = load_transcript(payload.get("transcript") or {})
        template_id = str(payload.get("template") or "meeting-minutes")
        template = find_template(template_id) or find_template("meeting-minutes")
        raw_summary = _answer_text((answers or {}).get("summary"))
        if not raw_summary.strip():
            raise MinuteError("缺少 summary 回答：请把模型对 summary 请求的原始输出原样传回")

        summary = self._summarizer.summarize_from_answer(raw_summary, template)
        if not summary:
            raise MinuteError("summary 回答无法解析为纪要结构（需要 JSON，可带 ```json 围栏）")

        result = MinuteResult(transcript=transcript, template_id=str(template["id"]))
        result.todos = summary.pop("todos", None) or []
        inline_mindmap = summary.pop("mindmap", None) or ""
        inline_keywords = summary.pop("keywords", None) or []
        result.summary = summary

        # 报告议题：单次回答（topics）或两阶段（topics#1…N + topic_outline）两种形态。
        # **但要留痕**：topics 是最容易被宿主跳过的一项（它长、要另写一份 JSON），
        # 实测 Agent 只答了 summary + keywords 却不出任何声，产物里 15 个议题里 10 个是空壳，
        # 而宿主完全不知道。这里给一条能定位到具体动作的警告，比默默降级有用得多。
        topics: list[dict] = []
        if str(payload.get("topics_mode") or "single") == "split":
            topics = self._collect_split_topics(payload, answers, result)
            if topics:
                result.summary["topics"] = topics
        else:
            raw_topics = _answer_text((answers or {}).get("topics"))
            parse_topics = getattr(self._summarizer, "topics_from_answer", None)
            if not raw_topics.strip():
                # prepare 在开启 topics 时已经把 chapters/mindmap 从 summary 请求里去掉了，
                # 这时跳过 topics 不只是长图变粗，导航条与导图也一起没有来源——必须说清楚。
                missing_derived = (
                    bool(payload.get("derive_from_topics"))
                    and not result.summary.get("chapters")
                    and not inline_mindmap.strip()
                )
                result.warnings.append(
                    "未提供 topics 回答：长图会退回「按章节均分」的粗版式（议题只有标题和时间，没有分节正文）"
                    + ("，chapters 与 mindmap 也无法派生" if missing_derived else "")
                    + "。要完整产物，请把 requests.topics 的模型原始输出作为 answers.topics 传回"
                )
            elif callable(parse_topics):
                topics = parse_topics(raw_topics) or []
                if topics:
                    result.summary["topics"] = topics
                else:
                    result.warnings.append("topics 回答无法解析，长图将按章节均分（纪要不受影响）")

        if with_keywords:
            # 关键词默认已在 summary 回答里（prepare 合并了请求）；单独补答的 answers.keywords
            # 优先，方便「summary 只回了纪要、关键词要补跑」的场景。
            raw_keywords = _answer_list((answers or {}).get("keywords"))
            if raw_keywords.strip():
                keywords = self._summarizer.keywords_from_answer(raw_keywords)
                if keywords is None:
                    result.warnings.append("keywords 回答无法解析，已跳过")
                else:
                    result.keywords = keywords
            elif inline_keywords:
                result.keywords = inline_keywords
            else:
                result.warnings.append(
                    "未提供关键词：summary 回答里没有 keywords 字段，也没有单独回答 answers.keywords"
                    "（不影响纪要与待办）"
                )

        # 章节与导图能从议题派生就不必让模型再写一遍：议题的 title/start/end/gist 与章节同构，
        # 分节里的分组要点也比模型二次压缩更具体。summary 里若仍带了（旧请求包/模型主动给了），
        # 一律以模型原文优先。
        if not result.summary.get("chapters") and topics:
            chapters = chapters_from_topics(topics)
            if chapters:
                result.summary["chapters"] = chapters
        result.mindmap = inline_mindmap.strip()
        if not result.mindmap and topics:
            result.mindmap = mindmap_from_topics(
                transcript.title, topics, result.todos, result.summary.get("insights")
            )
        if not result.mindmap:
            result.warnings.append("未生成导图（summary 回答里没有 mindmap 字段，也没有可派生的议题）")

        if out_dir is not None:
            result.files = self.write_outputs(result, out_dir, with_docx=with_docx,
                                              with_transcript_page=with_transcript_page)
        return result


    def _collect_split_topics(self, payload: dict, answers: dict, result: MinuteResult) -> list[dict]:
        """合并两阶段议题：大纲给标题/时间/gist，`topics#N` 给分节正文。

        缺哪个议题的正文就退回该议题的大纲（长图仍有标题与摘要），并记一条点名到
        `topics#N` 的警告——补答某一个议题不必重跑整场会。
        """
        outline = payload.get("topic_outline") or []
        if not outline:
            result.warnings.append(
                "请求包缺少 topic_outline：无法合并议题正文（请用 prepare --split-topics 重新生成，"
                "或改用单次 topics 模式）"
            )
            return []
        parse_body = getattr(self._summarizer, "topic_body_from_answer", None)
        merged_topics: list[dict] = []
        missing: list[int] = []
        for index, topic in enumerate(outline, 1):
            raw = _answer_text((answers or {}).get(f"topics#{index}"))
            body = None
            if raw.strip() and callable(parse_body):
                body = parse_body(
                    raw,
                    title=str(topic.get("title") or ""),
                    start=int(topic.get("start") or 0),
                    end=int(topic.get("end") or 0),
                )
            if not body:
                missing.append(index)
                merged_topics.append(dict(topic))
                continue
            # 标题与起止以大纲为准（扩写只负责正文），避免十个请求各写一套时间轴
            body["title"] = topic.get("title") or body.get("title")
            body["start"] = int(topic.get("start") or 0)
            body["end"] = int(topic.get("end") or body.get("end") or 0)
            merged_topics.append(body)
        if missing:
            result.warnings.append(
                "未提供或无法解析 " + "、".join(f"topics#{index}" for index in missing)
                + " 的扩写回答：这些议题只有大纲（标题/时间/gist），长报告里没有分节正文；"
                "补答对应键再 finish 即可"
            )
        return merged_topics

    def _make_mindmap(self, transcript: Transcript, inline_mindmap: str, result: MinuteResult) -> str:
        """导图：三阶段 harness 优先，失败回退总结里内联的大纲。"""
        try:
            outline, log_lines = build_mindmap_harness(
                self._summarizer.ask_json,
                [{"start": t.start, "end": t.end, "text": t.text, "speaker": t.speaker} for t in transcript.turns],
                transcript.speaker_names,
                meeting_title=transcript.title,
            )
            for line in log_lines:
                logger.info("导图 harness: %s", line)
            if outline.strip():
                return outline
            result.warnings.append("导图 harness 未产出结构，已回退单次生成")
        except Exception as exc:  # noqa: BLE001 - 导图失败不影响纪要
            logger.warning("导图 harness 失败", exc_info=True)
            result.warnings.append(f"导图 harness 失败：{exc}")
        if inline_mindmap.strip():
            return inline_mindmap
        result.warnings.append("导图未生成（不影响纪要与其他导出）")
        return ""

    # ── 导出 ──

    def write_outputs(
        self,
        result: MinuteResult,
        out_dir: str | Path,
        *,
        with_docx: bool = True,
        with_transcript_page: bool = False,
    ) -> dict[str, str]:
        """落盘：`<out>/纪要.docx / minutes.md / minutes.json / visual.json`。返回相对键→路径。"""
        target = Path(out_dir)
        target.mkdir(parents=True, exist_ok=True)
        meta = {
            "title": result.transcript.title,
            "created_at": result.transcript.created_at,
            "duration_seconds": result.transcript.duration_seconds,
            "speaker_names": result.transcript.speaker_names,
            "summary": result.summary,
            "todos": result.todos,
            "keywords": result.keywords,
            "mindmap": result.mindmap,
        }
        if with_transcript_page:
            # 附页要逐条转写。之前 meta 里根本没有 segments，`--with-transcript` 一直是空开关
            meta["segments"] = [
                {"start": turn.start, "end": turn.end, "text": turn.text, "speaker": turn.speaker}
                for turn in result.transcript.turns
            ]
        files: dict[str, str] = {}

        if with_docx:
            safe = _safe_name(result.transcript.title)
            docx_path = target / f"{safe} 纪要.docx"
            docx_path.write_bytes(build_minutes_docx(meta, with_transcript=with_transcript_page))
            files["docx"] = str(docx_path)

        md_path = target / "minutes.md"
        md_path.write_text(render_markdown(meta), encoding="utf-8")
        files["md"] = str(md_path)

        payload = result.to_dict()
        json_path = target / "minutes.json"
        json_path.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
        files["json"] = str(json_path)

        visual = (result.summary or {}).get("visual")
        if visual:
            visual_path = target / "visual.json"
            visual_path.write_text(json.dumps(visual, ensure_ascii=False, indent=2), encoding="utf-8")
            files["visual"] = str(visual_path)

        # 长网页报告的现成数据（含新议题结构）：`minutecraft render report.json` 直接出图，
        # 不必再走一遍装配，也让 Agent 能先看数据再决定要不要出图。
        report_path = target / "report.json"
        report_path.write_text(json.dumps(build_report(payload), ensure_ascii=False, indent=2), encoding="utf-8")
        files["report"] = str(report_path)
        return files


def _safe_name(title: str) -> str:
    """文件名里去掉 Windows 不允许的字符（纪要标题常带 / : 等）。"""
    cleaned = "".join(char for char in title if char not in '\\/:*?"<>|').strip()
    return (cleaned or "会议纪要")[:60]


def render_markdown(meta: dict) -> str:
    """全量 Markdown（与听记「导出 MD」同口径，补上关键词/章节/洞察）。"""
    lines: list[str] = [f"# {meta.get('title') or '会议纪要'}", ""]
    if meta.get("created_at"):
        lines.append(f"- 时间：{str(meta['created_at']).replace('T', ' ')}")
    duration = meta.get("duration_seconds")
    if duration:
        lines.append(f"- 时长：{int(duration) // 60:02d}:{int(duration) % 60:02d}")
    names = [name for name in (meta.get("speaker_names") or {}).values() if name]
    if names:
        lines.append(f"- 参与人：{'、'.join(dict.fromkeys(names))}")
    lines.append("")

    summary = meta.get("summary") or {}
    visual_summary = str((summary.get("visual") or {}).get("summary") or "").strip()
    if visual_summary:
        lines += [f"> {visual_summary}", ""]
    if summary.get("minutes"):
        lines += ["## 纪要", str(summary["minutes"]), ""]
    if summary.get("key_points"):
        lines += ["## 要点", *[f"- {point}" for point in summary["key_points"]], ""]
    if summary.get("sections"):
        for section in summary["sections"]:
            lines += [f"## {section.get('title') or ''}", str(section.get("text") or ""), ""]
    if meta.get("keywords"):
        lines += ["## 关键词", "、".join(meta["keywords"]), ""]
    if summary.get("chapters"):
        lines.append("## 章节")
        for chapter in summary["chapters"]:
            start = int(chapter.get("start") or 0)
            lines.append(f"- `{start // 60:02d}:{start % 60:02d}` {chapter.get('title') or ''}")
            if chapter.get("gist"):
                lines.append(f"  - {chapter['gist']}")
        lines.append("")
    if summary.get("insights"):
        lines += ["## AI 洞察", *[f"- {point}" for point in summary["insights"]], ""]
    kp = summary.get("quote_kp") or {}
    if kp.get("points") or kp.get("quotes"):
        lines += [f"## 核心发言提炼 · {kp.get('speaker') or '发言人'}", ""]
        lines += [f"- {point}" for point in kp.get("points") or []]
        lines += [f"> {quote}" for quote in kp.get("quotes") or []]
        lines.append("")
    if meta.get("todos"):
        lines.append("## 待办")
        for todo in meta["todos"]:
            mark = "x" if todo.get("done") else " "
            suffix = [value for value in (todo.get("owner_hint"), todo.get("due_hint")) if value]
            lines.append(f"- [{mark}] {todo.get('task') or ''}" + (f"（{'，'.join(suffix)}）" if suffix else ""))
        lines.append("")
    if meta.get("mindmap"):
        lines += ["## 思维导图大纲", "```", str(meta["mindmap"]), "```", ""]
    return "\n".join(lines)
