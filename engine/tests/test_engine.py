"""minutecraft 引擎回归：不调真实 LLM、不碰音频。

覆盖三件事：
  1. 输入归一（纯文本 / 带时间戳 / SRT / JSON 记录）—— Agent 丢进来的各种形态都能吃
  2. 产物形状（纪要/待办/关键词/导图/可视化报告）—— 与 SCHEMA 对账
  3. 导出（Word / Markdown / visual.json）—— 文件真的落盘且可再解析
"""
from __future__ import annotations

import json
import zipfile
from pathlib import Path

import pytest

from minutecraft import MinuteEngine, MinuteError, SCHEMA, Transcript, TranscriptTurn
from minutecraft.docx import build_minutes_docx
from minutecraft.docx.visual import build_visual_report, visual_sections
from minutecraft.engine import LLMSettings, render_markdown
from minutecraft.render import build_report
from minutecraft.templates import BUILTIN_TEMPLATES, find_template
from minutecraft.transcript import TranscriptFormatError, load_transcript, transcript_to_dict

PLAIN = """张三 [00:00] 我们开始今天的周会，主要过一下知识平台部署。
李四 [00:12] 部署这块我来跟进，周五前给排期。
张三 [00:25] 好，另外客户演示也要准备好。
"""

# FakeSummarizer 的哨兵：区分「没传」（用默认关键词）与「显式传 None」（模拟生成失败）
_UNSET: object = object()

SUMMARY = {
    "minutes": "本次周会确认了知识平台部署与客户演示两项工作的负责人与时间节点。",
    "key_points": ["部署由李四跟进，周五前出排期", "客户演示需要提前准备"],
    "chapters": [{"title": "部署排期", "start": 0, "end": 12, "gist": "确认部署负责人"}],
    "todos": [{"task": "给出部署排期", "owner_hint": "李四", "due_hint": "周五"}],
    "insights": ["部署与演示两线并行，需注意资源冲突。"],
    "quote_kp": {"speaker": "张三", "points": ["强调演示准备"], "quotes": ["客户演示也要准备好。"]},
    "visual": {
        "summary": "确认部署与演示两项工作的分工",
        "sections": [
            {
                "title": "部署排期",
                "layout": "cards",
                "items": [{"title": "部署跟进", "tag": "已解决", "bullets": ["李四负责", "周五前给排期"], "note": "排期是关键节点"}],
            },
            {
                "title": "关键共识",
                "layout": "closing",
                "items": [{"title": "", "bullets": [], "tag": "", "note": "演示前一周完成部署验收"}],
            },
        ],
    },
    "mindmap": "# 周会\n## 部署\n- 李四跟进\n## 演示\n- 提前准备",
}


class FakeSummarizer:
    """替身：形状与 MeetingSummarizer 一致，但结果固定（不依赖网络）。"""
    def __init__(self, summary=_UNSET, keywords=_UNSET, fail_mindmap: bool = True) -> None:
        self._summary = json.loads(json.dumps(SUMMARY)) if summary is _UNSET else summary
        # None 表示「关键词生成失败」，区别于空列表（表示确实没有关键词）
        self._keywords = ["知识平台", "部署"] if keywords is _UNSET else keywords
        self._fail_mindmap = fail_mindmap
        self.configured = True

    def summarize(self, transcript, template=None):  # noqa: ANN001
        if self._summary is None:
            return None
        return json.loads(json.dumps(self._summary))

    def extract_keywords(self, transcript):  # noqa: ANN001
        return None if self._keywords is None else list(self._keywords)  # type: ignore[arg-type]

    def ask_json(self, system, user):  # noqa: ANN001
        if self._fail_mindmap:
            return None  # harness 拿不到议题 → 回退总结里的内联 mindmap
        return {"topics": [{"name": "部署", "summary": "排期", "start": 0, "end": 12}]}


def _engine(summarizer: FakeSummarizer) -> MinuteEngine:
    engine = MinuteEngine(LLMSettings(base_url="http://fake", api_key="k", model="m"))
    return engine.use_summarizer(summarizer)  # type: ignore[arg-type]


# ── 1. 输入归一 ──


def test_load_plain_text_with_timestamps_and_speakers() -> None:
    transcript = load_transcript(_write(PLAIN))
    assert [turn.speaker for turn in transcript.turns] == ["0", "1", "0"]
    assert transcript.speaker_names == {"0": "张三", "1": "李四"}
    assert transcript.turns[1].start == 12
    assert transcript.render().startswith("张三 [00:00] 我们开始今天的周会")


def test_load_plain_text_without_timestamps_keeps_speaker_names() -> None:
    transcript = load_transcript(_write("主持人：大家好\n嘉宾：你好\n"))
    assert transcript.speaker_names == {"0": "主持人", "1": "嘉宾"}
    assert transcript.render(with_timestamps=False) == "主持人: 大家好\n嘉宾: 你好"


def test_load_srt() -> None:
    srt = "1\n00:00:01,200 --> 00:00:04,500\n大家好，开始会议\n\n2\n00:00:05,000 --> 00:00:08,000\n第一项是部署\n"
    transcript = load_transcript(_write(srt))
    assert len(transcript.turns) == 2
    assert transcript.turns[0].start == pytest.approx(1.2)
    assert transcript.turns[1].text == "第一项是部署"


def test_load_record_json_roundtrip() -> None:
    record = {
        "title": "周会",
        "created_at": "2026-10-18T10:00:00",
        "speaker_names": {"0": "张三"},
        "segments": [{"start": 0, "end": 2, "text": "开始", "speaker": "0"}],
    }
    transcript = load_transcript(record)
    assert transcript.title == "周会" and len(transcript.turns) == 1
    # 再导出再读回，形状稳定（Agent 落盘后用 json 文件续跑）
    again = load_transcript(transcript_to_dict(transcript))
    assert again.turns[0].text == "开始" and again.speaker_names == {"0": "张三"}


def test_empty_input_is_rejected() -> None:
    with pytest.raises(TranscriptFormatError):
        load_transcript(_write("   \n\n"))


# ── 2. 产物形状 ──


def test_build_shape_and_schema_alignment() -> None:
    result = _engine(FakeSummarizer()).build(load_transcript(_write(PLAIN)))
    assert result.summary["minutes"].startswith("本次周会")
    assert result.summary["insights"]  # 洞察
    assert result.summary["quote_kp"]["quotes"]  # 核心发言
    assert result.todos[0]["task"] == "给出部署排期"
    assert "todos" not in result.summary and "mindmap" not in result.summary
    assert result.keywords == ["知识平台", "部署"]
    # harness 不可用时回退总结里内联的 mindmap
    assert result.mindmap.startswith("# 周会")
    assert set(result.summary) <= set(SCHEMA["output"])


def test_llm_missing_raises_minute_error() -> None:
    engine = MinuteEngine(LLMSettings(base_url="", api_key="", model=""))
    assert engine.configured is False
    with pytest.raises(MinuteError):
        engine.build(Transcript(turns=[TranscriptTurn(0, 1, "你好")]))


def test_summary_failure_raises_but_optional_blocks_degrade() -> None:
    result = _engine(FakeSummarizer(keywords=None)).build(load_transcript(_write(PLAIN)))
    assert result.keywords == []
    assert any("关键词" in warning for warning in result.warnings)
    with pytest.raises(MinuteError):
        _engine(FakeSummarizer(summary=None)).build(load_transcript(_write(PLAIN)))


# ── 2b. 两步走（Agent 用自己的模型能力，零 LLM 配置）──
#
# 这是 skill 化的核心契约：宿主 Agent 有模型，不该再要 key。
# 两条路径（run 自带客户端 / prepare+finish）必须产出**同一结构**，否则会分叉成两套行为。


def _unconfigured_engine() -> MinuteEngine:
    """完全没有 LLM 配置的引擎——两步走必须在这上面也能工作。"""
    engine = MinuteEngine(LLMSettings(base_url="", api_key="", model=""))
    assert engine.configured is False
    return engine


def test_prepare_works_without_llm() -> None:
    payload = _unconfigured_engine().prepare(load_transcript(_write(PLAIN)))
    assert set(payload) >= {"version", "transcript", "template", "requests"}
    assert "system" in payload["requests"]["summary"] and "user" in payload["requests"]["summary"]
    # 转写原样带回：finish 只靠这个请求包就能还原全部上下文
    assert payload["transcript"]["turns"][0]["text"].startswith("我们开始今天的周会")


def test_finish_works_without_llm_and_matches_run_shape() -> None:
    engine = _unconfigured_engine()
    transcript = load_transcript(_write(PLAIN))
    payload = engine.prepare(transcript)
    answers = {"summary": json.dumps(SUMMARY, ensure_ascii=False), "keywords": '{"keywords": ["知识平台"]}'}

    result = engine.finish(payload, answers)
    # 与 run 路径同一结构口径
    assert result.summary["minutes"].startswith("本次周会")
    assert result.todos[0]["task"] == "给出部署排期"
    assert result.keywords == ["知识平台"]
    assert result.mindmap.startswith("# 周会")  # 取自回答里内联的 mindmap
    assert "todos" not in result.summary and "mindmap" not in result.summary
    assert set(result.summary) <= set(SCHEMA["output"])
    # 只答了 summary + keywords 时唯一该有的警告就是「topics 没答」（见 test_finish_without_topics_warns_and_falls_back）
    assert all("topics" in warning for warning in result.warnings)


def test_finish_tolerates_fenced_and_degrades_gracefully() -> None:
    engine = _unconfigured_engine()
    payload = engine.prepare(load_transcript(_write(PLAIN)))
    fenced = "```json\n" + json.dumps(SUMMARY, ensure_ascii=False) + "\n```"

    # 带 ``` 围栏也能解析；缺 keywords 只警告不失败
    result = engine.finish(payload, {"summary": fenced})
    assert result.summary["minutes"]
    assert result.keywords == []
    assert any("keywords" in warning for warning in result.warnings)


def test_finish_rejects_bad_input_with_actionable_error() -> None:
    engine = _unconfigured_engine()
    payload = engine.prepare(load_transcript(_write(PLAIN)))
    # 1) 压根没给回答
    with pytest.raises(MinuteError, match="缺少 summary"):
        engine.finish(payload, {})
    with pytest.raises(MinuteError, match="缺少 summary"):
        engine.finish(payload, {"summary": "   "})
    # 2) 给了但模型说的是人话、不是 JSON —— 归一层的兜底会把它当纯文本纪要，不算失败；
    #    这里测的是「连兜底都救不回来」的形态：JSON 结构正确但没有可用字段
    with pytest.raises(MinuteError, match="无法解析"):
        engine.finish(payload, {"summary": "[1, 2, 3]"})




def test_every_builtin_template_is_usable() -> None:
    for template in BUILTIN_TEMPLATES:
        assert find_template(template["id"]) is not None
    assert find_template("nope") is None


# ── 3. 导出 ──


def test_visual_report_prefers_structured_and_falls_back() -> None:
    record = {"summary": SUMMARY, "todos": []}
    sections = visual_sections(record, SUMMARY)
    assert sections[0]["title"] == "部署排期"
    report = build_visual_report(record)
    assert report["summary"].startswith("确认部署")

    legacy = {"summary": {"minutes": "老记录纪要", "key_points": ["要点一"]}, "todos": [{"task": "待办一"}]}
    fallback = visual_sections(legacy, legacy["summary"])
    assert [section["title"] for section in fallback] == ["会议纪要", "要点", "待办"]
    assert [section["accent"] for section in fallback] == ["rose", "blue", "teal"]


def test_docx_is_valid_and_carries_minutes_sections(tmp_path: Path) -> None:
    record = {
        "title": "产品周会",
        "created_at": "2026-10-18T10:00:00",
        "speaker_names": {"0": "张三", "1": "李四"},
        "summary": SUMMARY,
        "todos": [{"task": "给出部署排期", "owner_hint": "李四", "due_hint": "周五", "done": False}],
    }
    content = build_minutes_docx(record, with_transcript=False)
    path = tmp_path / "minutes.docx"
    path.write_bytes(content)
    with zipfile.ZipFile(path) as archive:
        names = archive.namelist()
        assert "word/document.xml" in names
        body = archive.read("word/document.xml").decode("utf-8")
    for expected in ("纪要", "会议背景", "待办", "章节", "全局行动项汇总", "AI洞察", "核心发言提炼", "01"):
        assert expected in body, expected


def test_build_writes_all_outputs(tmp_path: Path) -> None:
    result = _engine(FakeSummarizer()).build(load_transcript(_write(PLAIN)), out_dir=tmp_path)
    files = result.files
    # report 是长网页报告的现成数据（2026-10-19 新增）：产物清单只许扩、不许悄悄少
    assert set(files) == {"docx", "md", "json", "visual", "report"}
    for path in files.values():
        assert Path(path).exists() and Path(path).stat().st_size > 0
    payload = json.loads(Path(files["json"]).read_text(encoding="utf-8"))
    assert payload["todos"][0]["task"] == "给出部署排期"
    markdown = Path(files["md"]).read_text(encoding="utf-8")
    assert "## 待办" in markdown and "## AI 洞察" in markdown and "## 思维导图大纲" in markdown
    visual = json.loads(Path(files["visual"]).read_text(encoding="utf-8"))
    assert visual["sections"][0]["layout"] == "cards"


def test_render_markdown_minimal_record() -> None:
    text = render_markdown({"title": "空纪要"})
    assert text.startswith("# 空纪要")


# ── 4. 长网页报告：数据装配（不启动浏览器，纯函数）──


def _report_record() -> dict:
    summary = json.loads(json.dumps(SUMMARY))
    summary["chapters"] = [
        {"title": "部署排期", "start": 0, "end": 12, "gist": "确认部署负责人"},
        {"title": "演示准备", "start": 12, "end": 25, "gist": "演示要提前准备"},
    ]
    return {
        "title": "产品周会",
        "duration_seconds": 300,
        "speaker_names": {"0": "张三", "1": "李四"},
        "summary": summary,
        "todos": [{"task": "给出部署排期", "owner_hint": "李四", "due_hint": "周五"}, {"task": "  "}],
    }


def test_build_report_splits_sections_across_chapters() -> None:
    report = build_report(_report_record())
    assert report["title"] == "产品周会" and report["category"] == "会议纪要"
    assert [topic["title"] for topic in report["topics"]] == ["部署排期", "演示准备"]
    # 两个议题、两节：每节都要落到某个议题下（不能有一节被丢掉）
    landed = [section["title"] for topic in report["topics"] for section in topic["sections"]]
    assert landed == ["部署排期", "关键共识"]
    # 摘要优先取章节 gist
    assert report["topics"][0]["summary"] == "确认部署负责人"
    # 空待办被丢掉，不画空复选框
    assert [todo["task"] for todo in report["todos"]] == ["给出部署排期"]
    assert report["participants"] == ["张三", "李四"]
    assert report["insights"]  # 全场洞察放在文末


def test_build_report_degrades_without_chapters_without_inventing() -> None:
    # 没有 chapters：退化成单议题（标题取会议标题），而不是编造几个时间段
    report = build_report({"title": "只有纪要", "summary": {"minutes": "一句话纪要"}})
    assert len(report["topics"]) == 1
    assert report["topics"][0]["title"] == "只有纪要"
    # 没有的字段就不出现：模板据此不画空框
    for absent in ("focus", "insights", "todos", "participants"):
        assert absent not in report
    assert report["subtitle"] == "一句话纪要"


def test_build_report_uses_given_topics_verbatim() -> None:
    # 手工补的结构样例（将来引擎也可能直接产出这种形状）：议题与分节必须原样保留
    record = {
        "title": "整场",
        "topics": [
            {
                "title": "议题甲",
                "start": 10,
                "end": 20,
                "summary": "摘要甲",
                "focus": ["聚焦甲"],
                "sections": [{"title": "分节甲", "groups": [{"title": "组", "items": ["要点"]}]}],
                "insights": ["洞察甲"],
            },
            {"title": "议题乙", "start": 20, "end": 30},
        ],
        "summary": {"visual": {"summary": "视觉一句话"}},
    }
    report = build_report(record)
    assert [topic["title"] for topic in report["topics"]] == ["议题甲", "议题乙"]
    first = report["topics"][0]
    assert first["focus"] == ["聚焦甲"] and first["insights"] == ["洞察甲"]
    assert first["sections"][0]["title"] == "分节甲"
    # 没给的字段不出现（模板据此不画空框）
    assert "sections" not in report["topics"][1] and "summary" not in report["topics"][1]
    assert report["subtitle"] == "视觉一句话"


def test_render_reports_actionable_error_when_no_browser(tmp_path: Path, monkeypatch) -> None:
    from minutecraft import render as render_mod

    monkeypatch.setattr(render_mod.shutil, "which", lambda _name: None)
    monkeypatch.setattr(render_mod, "BROWSER_CANDIDATES", [str(tmp_path / "missing-chrome")])
    # 先确认模板找得到，失败原因必须是「找不到浏览器」而不是别的
    assert render_mod.resolve_template().exists()
    with pytest.raises(render_mod.ReportRenderError, match="找不到 Chrome/Edge"):
        render_mod.render_report({"title": "x"}, tmp_path / "x.png")


# ── 5. 报告议题（B 案：独立一次模型调用切细议题）──

TOPICS_ANSWER = {
    "topics": [
        {
            "title": "部署排期",
            "start": "00:00",
            "end": "00:12",
            "gist": "李四周五前出排期",
            "focus": ["本段讨论聚焦于部署由谁跟进。", "核心矛盾在于周五这个节点。"],
            "sections": [
                {
                    "title": "排期确认",
                    "groups": [
                        {"title": "责任到人", "items": ["李四负责部署", "周五前给排期"]},
                        {"title": "风险", "items": ["第三方接口未联调"]},
                    ],
                }
            ],
            "insights": ["排期偏紧，接口联调是唯一外部依赖。"],
        },
        {
            "title": "客户演示",
            "start": "00:12",
            "end": "00:25",
            "sections": [{"title": "演示准备", "bullets": ["脚本今天出", "需要预演一次"]}],
        },
    ]
}


def _topics_payload() -> bytes:
    return json.dumps(TOPICS_ANSWER, ensure_ascii=False).encode("utf-8")


def test_topics_from_answer_normalizes_shape() -> None:
    summarizer = minutes_from_transcript()
    topics = summarizer.topics_from_answer(json.dumps(TOPICS_ANSWER, ensure_ascii=False))
    assert [topic["title"] for topic in topics] == ["部署排期", "客户演示"]
    first = topics[0]
    assert first["start"] == 0 and first["end"] == 12
    assert first["sections"][0]["groups"][0] == {"title": "责任到人", "items": ["李四负责部署", "周五前给排期"]}
    # 弱模型只给 bullets 时折成分组，导出侧只认一种形状
    assert topics[1]["sections"][0]["groups"] == [{"title": "", "items": ["脚本今天出", "需要预演一次"]}]


def test_topics_missing_end_is_filled_from_next_topic() -> None:
    topics = minutes_from_transcript().topics_from_answer(
        json.dumps({"topics": [{"title": "甲", "start": "00:10"}, {"title": "乙", "start": "00:30", "end": "00:40"}]},
                   ensure_ascii=False)
    )
    assert topics[0]["end"] == 30  # 用下一个议题的起点补齐，不留空档


def test_topics_from_answer_rejects_garbage() -> None:
    summarizer = minutes_from_transcript()
    assert summarizer.topics_from_answer("不是 JSON") is None
    assert summarizer.topics_from_answer('{"topics": []}') is None
    # 全坏项：没有 title 的议题被丢弃，整体为空 → None（调用方回退旧渲染）
    assert summarizer.topics_from_answer('{"topics": [{"start": "00:00"}]}') is None


def test_finish_attaches_topics_and_render_uses_them() -> None:
    engine = _unconfigured_engine()
    payload = engine.prepare(load_transcript(_write(PLAIN)))
    # topics 是独立请求，必须出现在请求包里（B 案：Agent 要多答一段）
    assert "topics" in payload["requests"] and "system" in payload["requests"]["topics"]
    result = engine.finish(payload, {"summary": json.dumps(SUMMARY, ensure_ascii=False),
                                     "topics": json.dumps(TOPICS_ANSWER, ensure_ascii=False)})
    assert [topic["title"] for topic in result.summary["topics"]] == ["部署排期", "客户演示"]
    # 渲染优先用新议题结构（而不是把 chapters 与 visual 硬拼）
    report = build_report(result.to_dict())
    assert [topic["title"] for topic in report["topics"]] == ["部署排期", "客户演示"]
    assert report["topics"][0]["sections"][0]["title"] == "排期确认"


def test_finish_without_topics_warns_and_falls_back() -> None:
    """不会再静默：宿主跳过 topics 必须留一条能照着做的警告。

    实测踩过——Agent 只答了 summary + keywords，产物里 15 个议题 10 个是空壳，
    而 warnings 里一个字都没有，谁也不知道长图被降级了。
    """
    engine = _unconfigured_engine()
    payload = engine.prepare(load_transcript(_write(PLAIN)))
    result = engine.finish(payload, {"summary": json.dumps(SUMMARY, ensure_ascii=False),
                                     "keywords": '{"keywords": ["知识平台"]}'})
    assert "topics" not in result.summary
    assert any("topics" in warning for warning in result.warnings)
    # 警告要指出补救动作，而不是只说「缺了」
    assert any("answers.topics" in warning for warning in result.warnings)
    # 长图仍然出得来：回退到 chapters 均分 visual
    report = build_report(result.to_dict())
    assert len(report["topics"]) == len(SUMMARY["chapters"])


def test_finish_with_broken_topics_warns_but_keeps_minutes() -> None:
    engine = _unconfigured_engine()
    payload = engine.prepare(load_transcript(_write(PLAIN)))
    result = engine.finish(payload, {"summary": json.dumps(SUMMARY, ensure_ascii=False), "topics": "不是 JSON"})
    assert result.summary["minutes"]  # 纪要照常
    assert any("topics" in warning for warning in result.warnings)


def test_build_writes_report_json(tmp_path: Path) -> None:
    result = _engine(FakeSummarizer()).build(load_transcript(_write(PLAIN)), out_dir=tmp_path)
    report_path = Path(result.files["report"])
    assert report_path.exists()
    data = json.loads(report_path.read_text(encoding="utf-8"))
    # 现成数据可以直接再喂回 build_report（幂等），也能直接 `minutecraft render` 出图
    assert [topic["title"] for topic in data["topics"]] == [topic["title"] for topic in build_report(data)["topics"]]


def test_duration_inferred_from_last_turn_when_missing() -> None:
    """`duration_seconds` 常是 null（ASR 没回填）：从最后一句时间戳推，别显示 0 分钟。

    实测：一份 1 小时 22 分钟的会议，Agent 组装的 payload 里 duration_seconds 是 None，
    报告头部成了「0分钟」、每个议题时间段也都是零长度。
    """
    transcript = load_transcript({
        "title": "长会",
        "duration_seconds": None,
        "speaker_names": {"0": "田老师"},
        "turns": [
            {"start": 0, "end": 3, "text": "开场"},
            {"start": 120, "end": 4890, "text": "最后一段"},
        ],
    })
    assert transcript.duration_seconds == 4890
    # 显式给的时长优先，不被推出来的覆盖
    given = load_transcript({"duration_seconds": 100, "turns": [{"start": 0, "end": 9, "text": "x"}]})
    assert given.duration_seconds == 100


def test_chapter_without_end_borrows_next_start() -> None:
    """只给 start 的章节不能留成零长度（导航条会显示「20:32 – 20:32」）。"""
    from minutecraft.summarizer import _parse_chapters

    chapters = _parse_chapters([
        {"title": "甲", "start": "00:10", "gist": "一"},
        {"title": "乙", "start": "02:30", "gist": "二"},
        {"title": "丙", "start": "05:00", "gist": "三"},
    ])
    assert [chapter["end"] for chapter in chapters] == [150, 300, 300]


def test_no_topics_skips_the_slowest_call() -> None:
    """议题是最慢的一次调用（实测 250-300s）；只要纪要/Word 时得能明确跳过它。

    跳过不是静默的：产物里记一条警告，说明长图会退化成章节均分。
    """
    engine = _unconfigured_engine()
    payload = engine.prepare(load_transcript(_write(PLAIN)), with_topics=False)
    assert "topics" not in payload["requests"]
    assert "summary" in payload["requests"]


def test_run_with_no_topics_does_not_call_extract_topics() -> None:
    calls: list[str] = []

    class TrackingSummarizer(FakeSummarizer):
        def extract_topics(self, transcript: str):  # noqa: D102 - 测试替身
            calls.append(transcript)
            return [{"title": "不该被调用", "start": 0, "end": 1}]

    result = _engine(TrackingSummarizer()).build(load_transcript(_write(PLAIN)), with_topics=False)
    assert not calls, "with_topics=False 时不应发起议题调用"
    assert any("--no-topics" in warning for warning in result.warnings)


def test_answers_accept_objects_and_bare_arrays() -> None:
    """Agent 写 answers.json 的三种常见形态都要能用。

    踩过的两个坑：
    ① 值是解析好的 JSON 对象（不是原始文本）→ 原实现直接 `.strip()` 抛 AttributeError，
       而且堆栈看不出该怎么改；
    ② keywords 给裸数组 → 解析成「没有 keywords 字段」，**静默降级、exit code 仍是 0**，
       只能翻 warnings 才发现关键词丢了。
    """
    engine = _unconfigured_engine()
    payload = engine.prepare(load_transcript(_write(PLAIN)))

    # ① 值直接是对象
    result = engine.finish(payload, {
        "summary": SUMMARY,
        "keywords": {"keywords": ["知识平台"]},
        "topics": {"topics": [{"title": "部署排期", "start": "00:10", "end": "02:00"}]},
    })
    assert result.summary["minutes"].startswith("本次周会")
    assert result.keywords == ["知识平台"]
    assert result.summary["topics"][0]["title"] == "部署排期"

    # ② keywords 是裸数组（最容易写成这样）
    bare = engine.finish(payload, {"summary": json.dumps(SUMMARY, ensure_ascii=False), "keywords": ["知识平台", "上线"]})
    assert bare.keywords == ["知识平台", "上线"], "裸数组不能静默降级成没有关键词"


def test_cli_returns_partial_code_when_anything_degraded(tmp_path: Path) -> None:
    """有降级必须换退出码，不能一律 exit 0。

    踩过的坑：keywords 给裸数组被静默降级、**exit code 仍是 0**，得翻 warnings 才发现
    关键词丢了。现在：0 = 齐全，4 = 产物已出但有降级（可重跑补齐，不是失败）。
    """
    from minutecraft.cli import EXIT_PARTIAL, _finish_code

    assert _finish_code([]) == 0, "没有警告就是齐全"
    assert _finish_code(["未提供 topics 回答"]) == EXIT_PARTIAL == 4


def test_topics_prompt_asks_for_dense_sections() -> None:
    """篇幅口径是实测钉钉听记的：长议题要 2-4 分节、每分节 2-4 组。

    提示词回退成「1 个分节」会直接退回「只有标题没有正文」的粗版式，所以在这里钉住。
    """
    engine = minutes_from_transcript()
    system = engine.topics_request("x")["system"]
    assert "2-4 个" in system
    assert "2-3 条" in system
    assert "≥ 10 分钟" in system
    assert "不编造" in system
    """篇幅口径是实测钉钉听记的：长议题要 2-4 分节、每分节 2-4 组。

    提示词回退成「1 个分节」会直接退回「只有标题没有正文」的粗版式，所以在这里钉住。
    """
    engine = minutes_from_transcript()
    system = engine.topics_request("x")["system"]
    assert "2-4 个" in system
    assert "2-3 条" in system
    assert "≥ 10 分钟" in system
    assert "不编造" in system


def test_thinking_level_translates_to_reasoning_effort() -> None:
    """思考档位是唯一有效的提速开关，必须真的传到请求里。

    实测：同一份 82 分钟转写，topics 在 high 下 246s、none 下 32s（思考 token 归零）。
    压 max_tokens 没用——只会让它在思考阶段撞墙、内容为空。
    """
    from minutecraft.summarizer import MeetingSummarizer, _reasoning_param

    assert _reasoning_param("none") == {"reasoning_effort": "none"}
    assert _reasoning_param("low") == {"reasoning_effort": "low"}

    # 非法值退化成 auto（自适应），不能让一个手滑的环境变量把整条链路搞挂
    assert MeetingSummarizer("u", "k", "m", thinking="说快点").thinking == "auto"
    assert MeetingSummarizer("u", "k", "m", thinking="high").thinking == "high"


def test_thinking_adapts_to_transcript_length() -> None:
    """内容越多档位越低：长会本就慢，再叠高档只会更慢。

    实测同一份 82 分钟转写：none 76s / low 143s，长报告密度相当（10 议题 52 要点 vs 8 议题 74 要点）。
    """
    from minutecraft.summarizer import MeetingSummarizer

    sm = MeetingSummarizer("u", "k", "m", thinking="auto")
    assert sm.thinking_for("字" * 500) == "medium"       # 3 分钟短会：想清楚，质优
    assert sm.thinking_for("字" * 5000) == "low"         # 半小时会
    assert sm.thinking_for("字" * 13000) == "none"       # 80 分钟长会（实测 13365 字 → none，76s）

    # 钉死具体档位后不再自适应（命令行/环境变量显式要求优先）
    pinned = MeetingSummarizer("u", "k", "m", thinking="high")
    assert pinned.thinking_for("字" * 20000) == "high"


def test_hollow_minutes_is_detected() -> None:
    """「合法 JSON 但正文全空」要判成失败重试，不能静默交一份 0 字纪要。

    实测踩到：low 档回了一个 `minutes: ""`、`key_points: []` 的对象，解析一路绿灯，
    最后产出 371 字节的 minutes.md 和一份空白 Word。
    """
    from minutecraft.summarizer import _is_hollow_minutes

    assert _is_hollow_minutes('{"minutes": "", "key_points": [], "chapters": []}') is True
    assert _is_hollow_minutes("") is True
    assert _is_hollow_minutes('{"minutes": "有内容", "key_points": []}') is False
    assert _is_hollow_minutes('{"chapters": [{"title": "甲", "start": "00:01"}]}') is False
    # 解析不了的纯文本交给 _parse 的回退分支，这里不抢判断
    assert _is_hollow_minutes("这不是 JSON，但也是一份纪要") is False


def test_reasoning_effort_dropped_when_endpoint_rejects_it() -> None:
    """有些方言不认 reasoning_effort：要剥掉它重试，而不是让整份纪要失败。"""
    from minutecraft.summarizer import _create_with_retry, _rejects_reasoning

    class Unsupported(Exception):
        pass

    calls: list[dict] = []

    class FakeCompletions:
        @staticmethod
        def create(**kwargs):
            calls.append(dict(kwargs))
            if "reasoning_effort" in kwargs:
                raise Unsupported("Unsupported parameter: reasoning_effort")
            return "ok"

    class FakeChat:
        completions = FakeCompletions

    class FakeClient:
        chat = FakeChat

    assert _create_with_retry(FakeClient(), model="m", reasoning_effort="none") == "ok"
    assert len(calls) == 2, "第一次带 reasoning_effort，被拒后剥掉重试"
    assert "reasoning_effort" in calls[0] and "reasoning_effort" not in calls[1]
    assert _rejects_reasoning(Unsupported("Unsupported parameter: reasoning_effort"))


def minutes_from_transcript():
    """议题归一用例只需要一个未配置 LLM 的 summarizer（topics_from_answer 是纯解析）。"""
    from minutecraft.summarizer import MeetingSummarizer

    return MeetingSummarizer("", "", "")


def _write(content: str) -> Path:
    """把样例写到临时文件（load_transcript 走文件路径，贴近 CLI 实际用法）。"""
    import tempfile

    handle = tempfile.NamedTemporaryFile("w", suffix=".txt", delete=False, encoding="utf-8")
    handle.write(content)
    handle.close()
    return Path(handle.name)
