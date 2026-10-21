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
    # SRT 的结束时间以前被丢掉了（end=start），时长推不准
    assert transcript.turns[0].end == pytest.approx(4.5)


RANGE_TEXT = """[00:00 - 00:00] 嗯,
[00:00 - 00:01] 行嘞行,
[00:01 - 00:02] 好的好的,
[00:02 - 00:03] 那我往下拉了。
[00:03 - 00:05] 先过一下这个季度的目标，销售额要冲到三千万。
[00:05 - 00:07] 嗯,
[00:05 - 00:07] 张三: 这个我来负责，月底前给方案。
张三 [00:07 - 00:09] 好，那就这么定了。
"""


def test_load_range_timestamps_does_not_mistake_text_for_json() -> None:
    """`[00:00 - 00:03]` 开头的转写以前会被当成 JSON 数组，报 `Expecting ',' delimiter`。

    这是 ASR 导出最常见的形态之一：首字符 `[` + 每行一个起止区间。
    """
    transcript = load_transcript(_write(RANGE_TEXT))
    assert len(transcript.turns) == 8
    assert transcript.turns[3].text == "那我往下拉了。"
    assert transcript.turns[3].start == 2 and transcript.turns[3].end == 3
    # 区间里的说话人（写在正文里）也要认出来
    assert transcript.turns[6].speaker == "1"
    assert transcript.speaker_names == {"0": "说话人 1", "1": "张三"}
    # 时长从最后一句的 end 推出来
    assert transcript.duration_seconds == 9


def test_range_timestamp_keeps_sentences_with_colons() -> None:
    """正文里的中文冒号不是说话人分隔符。

    真踩过：`[00:03 - 00:06] 那我们开始吧，今天主要过三件事：部署进度…`
    被拆成「说话人=那我们开始吧，今天主要过三件事」+「部署进度…」，
    前半句直接消失，纪要里永远看不到。这里锁住「整句保留、不新增假说话人」。
    """
    transcript = load_transcript(_write(
        "[00:00 - 00:00] 嗯,\n"
        "[00:03 - 00:06] 那我们开始吧，今天主要过三件事：部署进度、客户演示、还有风险项。\n"
        "[00:06 - 00:09] 那我就往下拉了。风险有两个：一是表格识别，二是网络。\n"
    ))
    assert transcript.speaker_names == {"0": "说话人 1"}
    assert transcript.turns[1].text == "那我们开始吧，今天主要过三件事：部署进度、客户演示、还有风险项。"
    assert transcript.turns[2].text == "那我就往下拉了。风险有两个：一是表格识别，二是网络。"


def test_more_timestamp_shapes() -> None:
    """`[00:12]` 单时间戳、`[说话人 1] 内容`、无括号区间、单行 SRT 都要能解析。"""
    transcript = load_transcript(_write(
        "[00:12] 我们开始今天的周会。\n"
        "[00:20] [说话人 1] 部署这块我来跟进。\n"
        "[00:30] 【张三】客户演示也要准备好。\n"
        "00:40 - 00:50 好，那就这么定了。\n"
        "[00:00:51,200 --> 00:00:55,500] 最后确认一下时间。\n"
    ))
    assert [turn.start for turn in transcript.turns] == [12, 20, 30, 40, 51.2]
    assert transcript.turns[0].text == "我们开始今天的周会。"
    assert transcript.turns[1].speaker == "0"
    assert transcript.turns[2].speaker == "1" and transcript.speaker_names["1"] == "张三"
    assert transcript.turns[3].end == 50
    assert transcript.turns[4].text == "最后确认一下时间。"


def test_compact_material_merges_segments_and_drops_filler() -> None:
    """喂模型的材料要压缩：合并同说话人连续短句、丢掉「嗯/好的」这类碎句。

    原始 turns 不动（转写附页与 JSON 还要用）；只影响 render() 的输出。
    """
    transcript = load_transcript(_write(RANGE_TEXT))
    compact = transcript.render()
    full = transcript.render(compact=False)
    assert len(compact) < len(full) * 0.75, "压缩后应显著变短"
    assert "行嘞行" not in compact and "好的好的" not in compact
    assert "那我往下拉了" in compact
    # 时间戳保留在段首，且段数远少于行数
    assert "[00:02]" in compact and "[00:05]" in compact
    assert len(compact.splitlines()) < len(transcript.turns)
    # 原始数据一条不少
    assert len(transcript.turns) == 8
    # 整份都是语气词时不能删空
    noisy = load_transcript(_write("[00:00 - 00:01] 嗯,\n[00:01 - 00:02] 好的好的,\n"))
    assert noisy.render().strip()


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
    # 关键词已并入 summary：请求从 3 个降到 2 个，Agent 少一个来回
    assert set(payload["requests"]) == {"summary", "topics"}
    assert "keywords" in payload["requests"]["summary"]["system"]
    assert payload["plan"]["parallel"] is True
    assert payload["material"]["chars"] > 0 and payload["material"]["compact"] is True


def test_finish_takes_keywords_from_summary_answer() -> None:
    """prepare 默认把关键词并进 summary，finish 要能从同一条回答里取出来。

    这样两步走只需**两次模型调用**（summary+keywords / topics），而不是三次。
    """
    engine = _unconfigured_engine()
    payload = engine.prepare(load_transcript(_write(PLAIN)))
    summary_with_keywords = dict(SUMMARY, keywords=["知识平台", "部署", "客户演示"])
    result = engine.finish(payload, {"summary": json.dumps(summary_with_keywords, ensure_ascii=False)})
    assert result.keywords == ["知识平台", "部署", "客户演示"]
    assert not any("关键词" in warning for warning in result.warnings)


def test_prepare_full_text_keeps_every_line() -> None:
    """`--full-text` 是逃生口：不要压缩时能拿回逐行材料。"""
    transcript = load_transcript(_write(RANGE_TEXT))
    compact = _unconfigured_engine().prepare(transcript)
    full = _unconfigured_engine().prepare(transcript, with_compact_material=False)
    assert full["material"]["chars"] > compact["material"]["chars"]
    assert full["material"]["compact"] is False
    assert "行嘞行" in full["requests"]["summary"]["user"]
    assert "行嘞行" not in compact["requests"]["summary"]["user"]


def _minutes_text(minutes: int, sentence: str = "这一分钟讨论一个事项并给出结论。") -> str:
    lines = [f"张三 [{minute:02d}:00] {sentence}" for minute in range(minutes)]
    return "\n".join(lines) + "\n"


def test_prepare_recommends_split_topics_for_long_meetings() -> None:
    """材料规模踩到单段 topics 的上下文红线时，prepare 必须点名建议两阶段。

    实测教训：59 分钟会（压缩后 15960 字）走单段 topics，32K 级宿主的子代理上下文耗尽、
    **跑完零产出**，白烧 25 分钟再重走大纲腿。慢可以忍，白跑不能忍。
    """
    engine = _unconfigured_engine()

    long_meeting = engine.prepare(load_transcript(_write(_minutes_text(40))))
    assert long_meeting["plan"]["recommend"] == "split-topics"
    # 时长这条分支：字数才 1119（远低于 9000），单靠「约 39 分钟」就该建议
    assert long_meeting["material"]["chars"] < 9000
    assert "39 分钟" in long_meeting["plan"]["recommend_reason"]
    # 建议要顶到 note 里：Agent 往往只看 note / next 就动手
    assert long_meeting["plan"]["note"].startswith("压缩后材料")

    # 字数够多但时长没到，也建议（材料规模本身就是撑爆窗口的直接原因）
    verbose = _minutes_text(20, sentence="这件事的背景是这样的，" + "细节展开说明依据与口径。" * 60)
    verbose_meeting = engine.prepare(load_transcript(_write(verbose)))
    assert verbose_meeting["material"]["chars"] >= 9000
    assert verbose_meeting["plan"]["recommend"] == "split-topics"

    # 短会保持单段：少一次往返更快
    short = engine.prepare(load_transcript(_write(PLAIN)))
    assert short["plan"]["recommend"] == "" and short["plan"]["recommend_reason"] == ""

    # 已经选了 split-topics 就别再劝
    split = engine.prepare(load_transcript(_write(_minutes_text(40))), split_topics=True)
    assert split["plan"]["recommend"] == ""


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


def test_render_png_stitches_segments_beyond_shot_limit(tmp_path: Path, monkeypatch) -> None:
    """超过 `MAX_SHOT_HEIGHT` 的长报告走「分段截图 + 纵向拼接」，这条分支必须真跑到。

    实测踩过：拼接分支写成 `from PIL import Image as image`（类绑到小写哨兵），
    而下面引用的是大写 `Image` → 59 分钟会的 15589px 长图直接 `NameError`，退出码 3。
    其余用例都是短页，永远走不到这条分支，所以发布时没人发现。
    """
    import sys
    import types

    from minutecraft import render as render_mod

    total = render_mod.MAX_SHOT_HEIGHT + 2000
    monkeypatch.setattr(render_mod, "_measure_height", lambda _c, _p, _w: total)

    shots: list[tuple[str, int]] = []

    def fake_shot(_chrome: str, page: Path, out: Path, _width: int, height: int) -> None:
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_bytes(b"part")
        shots.append((page.name, height))

    monkeypatch.setattr(render_mod, "_shot", fake_shot)

    pasted: list[tuple[int, int]] = []
    canvases: list[tuple[int, int]] = []

    class FakeImage:
        def __init__(self, width: int, height: int) -> None:
            self.width, self.height = width, height

        @classmethod
        def open(cls, _path: Path) -> "FakeImage":
            return cls(10, 10)

        @classmethod
        def new(cls, _mode: str, size: tuple[int, int], _color: str) -> "FakeImage":
            canvases.append(size)
            return cls(*size)

        def paste(self, other: "FakeImage", box: tuple[int, int]) -> None:
            pasted.append((other.height, box[1]))

        def save(self, path: Path) -> None:
            Path(path).write_bytes(b"merged")

    fake_pil = types.ModuleType("PIL")
    fake_pil.Image = FakeImage  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "PIL", fake_pil)

    page = tmp_path / "report.html"
    page.write_text("<html><body></body></html>", encoding="utf-8")
    out = tmp_path / "long.png"

    assert render_mod.render_png("chrome", page, out) == out

    assert [height for _name, height in shots] == [render_mod.MAX_SHOT_HEIGHT, 2000]
    assert canvases == [(10, 20)], "拼接画布要等于各段高度之和"
    assert pasted == [(10, 0), (10, 10)], "第二段必须压在第一段下面，不能重叠"
    assert out.read_bytes() == b"merged"
    assert not list(tmp_path.glob("_part*.png")), "分段临时图要清掉"


def test_render_png_asks_for_pillow_before_shooting_when_too_tall(tmp_path: Path, monkeypatch) -> None:
    """没装 Pillow 时**在截图之前**失败，不留一堆半成品 PNG（docstring 里承诺过）。"""
    import builtins

    from minutecraft import render as render_mod

    real_import = builtins.__import__

    def no_pillow(name: str, *args, **kwargs):
        if name == "PIL" or name.startswith("PIL."):
            raise ImportError("No module named 'PIL'")
        return real_import(name, *args, **kwargs)

    shots: list[int] = []
    monkeypatch.setattr(render_mod, "_measure_height",
                        lambda _c, _p, _w: render_mod.MAX_SHOT_HEIGHT + 1)
    monkeypatch.setattr(render_mod, "_shot",
                        lambda _c, _p, _o, _w, height: shots.append(height))
    monkeypatch.setattr(builtins, "__import__", no_pillow)

    page = tmp_path / "report.html"
    page.write_text("<html><body></body></html>", encoding="utf-8")

    with pytest.raises(render_mod.ReportRenderError, match=r"pip install pillow"):
        render_mod.render_png("chrome", page, tmp_path / "long.png")
    assert shots == []
    assert not list(tmp_path.glob("_part*.png"))


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


def test_prepare_moves_chapters_and_mindmap_out_of_summary_when_topics_on() -> None:
    """开启 topics 时 summary 不再要 chapters/mindmap：这两项能从议题派生。

    实测这两项占 summary 回答约 1/3 的 output token，而 Agent 慢就慢在 output token。
    """
    engine = _unconfigured_engine()
    payload = engine.prepare(load_transcript(_write(PLAIN)))
    system = payload["requests"]["summary"]["system"]
    assert '"chapters"' not in system and '"mindmap"' not in system
    assert "visual" in system and "keywords" in system  # 可视化与关键词仍由模型产出
    assert payload["derive_from_topics"] is True and payload["plan"]["derive_from_topics"] is True

    no_topics = engine.prepare(load_transcript(_write(PLAIN)), with_topics=False)
    system = no_topics["requests"]["summary"]["system"]
    assert '"chapters"' in system and '"mindmap"' in system
    assert no_topics["derive_from_topics"] is False


def test_finish_derives_chapters_and_mindmap_from_topics() -> None:
    """summary 按新提示词不答 chapters/mindmap 时，产物字段仍要齐全。"""
    engine = _unconfigured_engine()
    payload = engine.prepare(load_transcript(_write(PLAIN)))
    summary = {key: value for key, value in SUMMARY.items() if key not in ("chapters", "mindmap")}
    result = engine.finish(payload, {"summary": json.dumps(summary, ensure_ascii=False),
                                     "topics": json.dumps(TOPICS_ANSWER, ensure_ascii=False)})
    assert [chapter["title"] for chapter in result.summary["chapters"]] == ["部署排期", "客户演示"]
    assert result.summary["chapters"][0]["start"] == 0 and result.summary["chapters"][0]["end"] == 12
    assert result.mindmap.startswith("# 会议纪要")
    assert "## 部署排期" in result.mindmap and "  - 李四负责部署" in result.mindmap
    # 待办与洞察单独成支
    assert "## 待办" in result.mindmap and "给出部署排期" in result.mindmap
    assert "## 洞察" in result.mindmap
    assert not any("导图" in warning for warning in result.warnings)


def test_finish_without_topics_says_derived_fields_are_lost() -> None:
    engine = _unconfigured_engine()
    payload = engine.prepare(load_transcript(_write(PLAIN)))
    summary = {key: value for key, value in SUMMARY.items() if key not in ("chapters", "mindmap")}
    result = engine.finish(payload, {"summary": json.dumps(summary, ensure_ascii=False)})
    assert any("chapters 与 mindmap 也无法派生" in warning for warning in result.warnings)


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


def test_cli_writes_prompt_files_and_reads_at_path_answers(tmp_path: Path) -> None:
    """Agent 场景的两个减负点：提示词落成文件、回答用 @文件 引用。

    这两步都不调模型；把整份转写塞进 request.json / answers.json 让 Agent 反复读写，
    是两步走里最容易被忽略的一段上下文与 output token 开销。
    """
    from minutecraft.cli import main

    raw = tmp_path / "meeting.txt"
    raw.write_text(PLAIN, encoding="utf-8")
    request = tmp_path / "request.json"
    assert main(["prepare", str(raw), "-o", str(request), "--title", "周会"]) == 0

    payload = json.loads(request.read_text(encoding="utf-8"))
    prompts = tmp_path / "request.prompts"
    for name in ("summary", "topics"):
        assert (prompts / f"{name}.system.txt").read_text(encoding="utf-8") == payload["requests"][name]["system"]
        assert (prompts / f"{name}.user.txt").read_text(encoding="utf-8") == payload["requests"][name]["user"]

    (tmp_path / "summary.txt").write_text(
        json.dumps(dict(SUMMARY, keywords=["知识平台", "部署"]), ensure_ascii=False), encoding="utf-8"
    )
    (tmp_path / "topics.txt").write_text(json.dumps(TOPICS_ANSWER, ensure_ascii=False), encoding="utf-8")
    answers = tmp_path / "answers.json"
    answers.write_text('{"summary": "@summary.txt", "topics": "@topics.txt"}', encoding="utf-8")

    out = tmp_path / "out"
    assert main(["finish", str(request), "--answers", str(answers), "-o", str(out), "--json"]) == 0
    minutes = json.loads((out / "minutes.json").read_text(encoding="utf-8"))
    assert minutes["summary"]["topics"][0]["title"] == "部署排期"
    assert minutes["summary"]["chapters"][0]["title"] == "部署排期"


def test_cli_missing_at_path_answer_is_input_error(tmp_path: Path) -> None:
    from minutecraft.cli import main

    raw = tmp_path / "meeting.txt"
    raw.write_text(PLAIN, encoding="utf-8")
    request = tmp_path / "request.json"
    assert main(["prepare", str(raw), "-o", str(request)]) == 0
    answers = tmp_path / "answers.json"
    answers.write_text('{"summary": "@nope.txt"}', encoding="utf-8")
    assert main(["finish", str(request), "--answers", str(answers)]) == 2


SPLIT_PLAIN = """张三 [00:00] 我们先过部署，目标是周五前上线。
李四 [02:00] 部署由我跟进，接口还剩两个没联调。
张三 [05:00] 好，部署风险记一下，联调是唯一外部依赖。
李四 [10:00] 接下来是客户演示，脚本今天下班前发群里。
张三 [20:00] 演示前要预演一次，重点讲知识问答。
"""

SPLIT_OUTLINE = {
    "topics": [
        {"title": "部署上线", "start": "00:00", "end": "10:00", "gist": "周五前上线，联调待完成"},
        {"title": "客户演示", "start": "10:00", "end": "25:00", "gist": "脚本今天发出，演示前预演"},
    ]
}


def test_split_topics_expand_slices_transcript_per_topic() -> None:
    """两阶段议题：大纲之后，每个议题只带自己的时间窗，互不依赖、可全部并发。"""
    engine = _unconfigured_engine()
    payload = engine.prepare(load_transcript(_write(SPLIT_PLAIN)), split_topics=True)
    assert set(payload["requests"]) == {"summary", "topics_outline"}
    assert payload["topics_mode"] == "outline"
    assert '"chapters"' not in payload["requests"]["summary"]["system"]

    expanded = engine.expand(payload, json.dumps(SPLIT_OUTLINE, ensure_ascii=False), margin=0)
    assert expanded["topics_mode"] == "split" and len(expanded["topic_outline"]) == 2
    assert list(expanded["requests"]) == ["topics#1", "topics#2"]
    first = expanded["requests"]["topics#1"]["user"]
    second = expanded["requests"]["topics#2"]["user"]
    assert "我们先过部署" in first and "联调是唯一外部依赖" in first
    assert "预演一次" not in first, "后一个议题的内容不该进前一个请求"
    assert "客户演示，脚本今天下班前发群里" in second and "预演一次" in second
    assert "联调是唯一外部依赖" not in second, "前一个议题的内容不该进后一个请求"


def test_finish_merges_split_topic_answers_with_outline() -> None:
    """扩写回答只负责正文，标题/起止以大纲为准；缺失的议题退回大纲并点名警告。"""
    engine = _unconfigured_engine()
    payload = engine.prepare(load_transcript(_write(SPLIT_PLAIN)), split_topics=True)
    expanded = engine.expand(payload, json.dumps(SPLIT_OUTLINE, ensure_ascii=False), margin=0)
    summary = {key: value for key, value in SUMMARY.items() if key not in ("chapters", "mindmap")}
    body_one = {"focus": ["部署聚焦上线时间"], "sections": [
        {"title": "上线安排", "groups": [{"title": "责任到人", "items": ["李四跟进部署"]}]}
    ], "insights": ["联调是唯一外部依赖"]}
    result = engine.finish(expanded, {
        "summary": json.dumps(dict(summary, keywords=["部署"]), ensure_ascii=False),
        "topics#1": json.dumps(body_one, ensure_ascii=False),
    }, out_dir=None)
    topics = result.summary["topics"]
    assert [topic["title"] for topic in topics] == ["部署上线", "客户演示"]
    assert topics[0]["sections"][0]["title"] == "上线安排"
    assert topics[0]["start"] == 0 and topics[0]["end"] == 600
    assert topics[1]["gist"] == "脚本今天发出，演示前预演"
    assert any("topics#2" in warning for warning in result.warnings)


def test_cli_split_topics_expand_and_finish(tmp_path: Path) -> None:
    from minutecraft.cli import main

    raw = tmp_path / "meeting.txt"
    raw.write_text(SPLIT_PLAIN, encoding="utf-8")
    request = tmp_path / "request.json"
    assert main(["prepare", str(raw), "-o", str(request), "--split-topics"]) == 0
    outline = tmp_path / "outline.txt"
    outline.write_text(json.dumps(SPLIT_OUTLINE, ensure_ascii=False), encoding="utf-8")

    expanded_path = tmp_path / "expand.json"
    assert main(["expand", str(request), "--outline", str(outline), "-o", str(expanded_path)]) == 0
    for index in (1, 2):
        assert (tmp_path / "expand.prompts" / f"topics-{index}.system.txt").is_file()
        assert (tmp_path / "expand.prompts" / f"topics-{index}.user.txt").is_file()

    summary = {key: value for key, value in SUMMARY.items() if key not in ("chapters", "mindmap")}
    (tmp_path / "summary.txt").write_text(
        json.dumps(dict(summary, keywords=["部署"]), ensure_ascii=False), encoding="utf-8"
    )
    for index in (1, 2):
        (tmp_path / f"topic-{index}.txt").write_text(
            json.dumps({"sections": [{"title": f"分节{index}", "groups": [{"title": "结论", "items": ["内容"]}]}]},
                       ensure_ascii=False),
            encoding="utf-8",
        )
    answers = tmp_path / "answers.json"
    answers.write_text(
        '{"summary": "@summary.txt", "topics#1": "@topic-1.txt", "topics#2": "@topic-2.txt"}',
        encoding="utf-8",
    )
    out = tmp_path / "out"
    assert main(["finish", str(expanded_path), "--answers", str(answers), "-o", str(out), "--json"]) == 0
    minutes = json.loads((out / "minutes.json").read_text(encoding="utf-8"))
    assert [topic["title"] for topic in minutes["summary"]["topics"]] == ["部署上线", "客户演示"]
    assert minutes["summary"]["chapters"][0]["title"] == "部署上线"


# ── 7. 长会复盘：split-topics 建议顶到 next / 大纲腿强压缩 ──

SKELETON_TEXT = """张三 [00:00] 先说部署，目标是周五前上线。接口联调还剩两个没打通，这是唯一的外部依赖。
李四 [00:20] 演示脚本我今天下班前写好。明天上午预演一次，重点讲知识问答那条链路。
张三 [01:05] 客户那边要的报表口径要统一，不然两边数据对不上，回头又说我们的数不准。
"""


def test_render_skeleton_keeps_every_anchor_but_only_head_sentence() -> None:
    """骨架材料：时间锚点一个不能少（切议题全靠它），正文只留开头句。"""
    transcript = load_transcript(_write(SKELETON_TEXT))
    skeleton = transcript.render_skeleton()
    assert "[00:00]" in skeleton and "[00:20]" in skeleton and "[01:05]" in skeleton
    assert "先说部署，目标是周五前上线。" in skeleton
    # 第二句往后全砍掉——这些字数就是大纲腿白烧的输入
    assert "接口联调还剩两个没打通" not in skeleton
    assert "明天上午预演一次" not in skeleton
    assert len(skeleton) < len(transcript.render())


def test_head_sentence_cuts_at_comma_when_first_sentence_too_long() -> None:
    """首句本身超长时退到最近的逗号，不硬切在词中间。"""
    from minutecraft.textutil import head_sentence

    # 版本号里的「.」不能被当句末（「3.5」切成「3.」）——所以 ASCII 句点不参与断句
    assert head_sentence("版本 3.5 已经发布，剩下的细节先不展开说明，后面再对齐一次口径要求。", limit=20) == "版本 3.5 已经发布，"
    assert head_sentence("短句子。后面还有内容。") == "短句子。"


def test_prepare_lean_outline_only_shrinks_the_outline_leg() -> None:
    """`--lean-outline` 只换大纲腿的输入：summary 一个字不能少，切分口径（system）不变。"""
    engine = _unconfigured_engine()
    transcript = load_transcript(_write(SKELETON_TEXT * 3))
    normal = engine.prepare(transcript, split_topics=True)
    lean = engine.prepare(transcript, split_topics=True, lean_outline=True)

    assert lean["material"]["lean_outline"] is True
    assert 0 < lean["material"]["outline_chars"] < normal["material"]["outline_chars"]
    assert lean["requests"]["summary"]["user"] == normal["requests"]["summary"]["user"]
    assert lean["requests"]["topics_outline"]["system"] == normal["requests"]["topics_outline"]["system"]
    assert "接口联调还剩两个没打通" not in lean["requests"]["topics_outline"]["user"]


def test_cli_prepare_puts_recommendation_first_in_next(tmp_path: Path, capsys) -> None:
    """Agent 通常照着 `next` 那行做，所以建议必须排在最前面，不能埋在 plan 里。"""
    from minutecraft.cli import main

    raw = tmp_path / "meeting.txt"
    raw.write_text(_minutes_text(40), encoding="utf-8")
    request = tmp_path / "request.json"
    assert main(["prepare", str(raw), "-o", str(request), "--json"]) == 0
    emitted = json.loads(capsys.readouterr().out)

    assert emitted["plan"]["recommend"] == "split-topics"
    assert emitted["next"].startswith("[建议]")
    assert "--split-topics" in emitted["next"]


# ── 议题正文的密度档：精简 / 详细 / 按宿主实测吞吐自动 ──────────────────────

# 两档共用同一个 JSON 形状：`finish` 与出图只认这一种结构，档位差只能体现在「条数与句长」，
# 一旦形状漂移，精简档就会在解析层被丢掉（现场表现是报告正文整块空掉）。
_TOPICS_SHAPE = '"sections": [{"title": "分节名", "groups": [{"title": "看法或子标题", "items": ["要点"]}]}]'
_SHARED_INVARIANTS = (
    _TOPICS_SHAPE,
    '<span class="warm">词</span>',
    "不编造",
    "反引号",
    "讨论情况",
)


def test_both_topics_prompts_keep_the_shared_invariants() -> None:
    """精简档只准收「条数与句长」，重点口径（结构 / 高亮 / 数字出处 / 空标题禁令）一条不能掉。"""
    summarizer = minutes_from_transcript()
    dense = summarizer.topics_request("x")["system"]
    lean = summarizer.topics_request("x", dense=False)["system"]

    for needle in _SHARED_INVARIANTS:
        assert needle in dense, f"详细档丢了 {needle}"
        assert needle in lean, f"精简档丢了 {needle}"
    assert "≤45 字" in lean and "2-4 个" not in lean
    assert "2-4 个" in dense, "详细档的篇幅口径是被实测钉住的，不能被精简档带跑"


def test_both_expand_prompts_keep_the_shared_invariants() -> None:
    """逐议题扩写同理：两档共用一套不变量，差异只在条数与句长。"""
    summarizer = minutes_from_transcript()
    topic = {"title": "部署上线", "start": 0, "end": 600, "gist": "周五前上线"}
    dense = summarizer.topic_expand_request(topic, "张三 [00:00] 部署", 1, 2)["system"]
    lean = summarizer.topic_expand_request(topic, "张三 [00:00] 部署", 1, 2, dense=False)["system"]

    for needle in _SHARED_INVARIANTS + ("区间外",):
        assert needle in dense, f"详细档丢了 {needle}"
        assert needle in lean, f"精简档丢了 {needle}"
    assert "每组 2-3 条" in dense and "≤45 字" in lean
    # 大纲腿与密度档无关：它本来就不写正文，两条路的输出量一样
    assert "不要** focus / sections" in minutes_from_transcript().topics_outline_request("x")["system"]


def test_resolve_density_uses_measured_speed_before_size() -> None:
    """吞吐实测优先于材料规模：慢宿主哪怕短会也别写详细档，快宿主长会照样能要详细的。"""
    from minutecraft.engine import DENSITY_DETAILED_MIN_BPS, resolve_density

    slow, reason = resolve_density("auto", chars=300, minutes=10, output_bps=12.0)
    assert slow == "concise" and "12" in reason
    fast, _ = resolve_density("auto", chars=20000, minutes=60, output_bps=60.0)
    assert fast == "detailed"
    # 阈值两侧都要判得对（12 字节/秒是一次 59 分钟会 summary 腿的真实水位）
    assert DENSITY_DETAILED_MIN_BPS > 12.0
    edge, _ = resolve_density("auto", chars=300, minutes=10, output_bps=DENSITY_DETAILED_MIN_BPS)
    assert edge == "detailed"


def test_resolve_density_without_speed_is_conservative_by_size(monkeypatch) -> None:
    """宿主没报吞吐时按规模保守判：长会先精简（宁可短，别让一腿烧半小时），短会保持详细。"""
    from minutecraft.engine import resolve_density

    monkeypatch.delenv("MINUTECRAFT_OUTPUT_BPS", raising=False)
    long_meeting, reason = resolve_density("auto", chars=15960, minutes=59)
    assert long_meeting == "concise" and "--output-bps" in reason
    assert resolve_density("auto", chars=119, minutes=0.5)[0] == "detailed"
    # 显式指定最大，永远压过自动判断
    assert resolve_density("detailed", chars=15960, minutes=59) == ("detailed", "手动指定 --density detailed")
    # 非法环境变量不能把整条链路带崩，退成「未知」按规模判
    monkeypatch.setenv("MINUTECRAFT_OUTPUT_BPS", "挺快的")
    assert resolve_density("auto", chars=15960, minutes=59)[0] == "concise"
    monkeypatch.setenv("MINUTECRAFT_OUTPUT_BPS", "40")
    assert resolve_density("auto", chars=15960, minutes=59)[0] == "detailed"


def test_prepare_density_concise_swaps_only_the_topics_prompt() -> None:
    """`--density concise` 换掉议题提示词，summary 纪要一个字不动（用户口径：只收议题正文）。"""
    engine = _unconfigured_engine()
    transcript = load_transcript(_write(SKELETON_TEXT * 3))
    dense = engine.prepare(transcript)
    lean = engine.prepare(transcript, density="concise")

    assert lean["plan"]["density"] == "concise"
    assert dense["plan"]["density"] == "detailed"
    assert lean["plan"]["density_requested"] == "concise"
    assert "≤45 字" in lean["requests"]["topics"]["system"]
    assert lean["requests"]["topics"]["user"] == dense["requests"]["topics"]["user"]
    assert lean["requests"]["summary"] == dense["requests"]["summary"]


def test_prepare_density_preview_does_not_touch_the_outline_leg() -> None:
    """两阶段时 prepare 只预告档位：大纲腿不写正文，提示词不能因为档位改变。"""
    engine = _unconfigured_engine()
    transcript = load_transcript(_write(SKELETON_TEXT * 3))
    payload = engine.prepare(transcript, split_topics=True, density="concise")

    assert "topics" not in payload["requests"]
    assert payload["plan"]["density"] == "concise"
    assert payload["requests"]["topics_outline"]["system"] == engine.prepare(
        transcript, split_topics=True
    )["requests"]["topics_outline"]["system"]


def test_expand_inherits_explicit_density_from_prepare() -> None:
    """prepare 显式指定的档位必须一路带到扩写——同一场会不能前后两次跑出两种版式。"""
    engine = _unconfigured_engine()
    payload = engine.prepare(load_transcript(_write(SPLIT_PLAIN)), split_topics=True, density="concise")
    outline = json.dumps(SPLIT_OUTLINE, ensure_ascii=False)

    inherited = engine.expand(payload, outline, margin=0)
    assert inherited["plan"]["density"] == "concise"
    assert "沿用 prepare" in inherited["plan"]["density_reason"]
    assert all("≤45 字" in request["system"] for request in inherited["requests"].values())

    # 扩写时显式覆盖 prepare 的选择，也要照办
    override = engine.expand(payload, outline, margin=0, density="detailed")
    assert override["plan"]["density"] == "detailed"
    assert all("每组 2-3 条" in request["system"] for request in override["requests"].values())

    # 没显式指定过（prepare 走 auto）时，扩写自己按吞吐判
    auto = engine.prepare(load_transcript(_write(SPLIT_PLAIN)), split_topics=True)
    judged = engine.expand(auto, outline, margin=0, output_bps=12.0)
    assert judged["plan"]["density"] == "concise"
    assert judged["plan"]["density_requested"] == ""


def test_expand_reuses_the_throughput_reported_at_prepare() -> None:
    """吞吐只报一次就够：第二阶段沿用它，否则快宿主的长会会在 expand 里被悄悄降成精简档。

    冒烟实测踩过：`prepare --output-bps 12` 判到 concise，`expand` 却报「吞吐未知」——
    反过来（prepare 30 字节/秒判 detailed、expand 按规模判 concise）同样错。
    """
    engine = _unconfigured_engine()
    payload = engine.prepare(load_transcript(_write(_minutes_text(45))), split_topics=True, output_bps=30.0)
    assert payload["plan"]["density"] == "detailed" and payload["plan"]["output_bps"] == 30.0

    outline = json.dumps(SPLIT_OUTLINE, ensure_ascii=False)
    expanded = engine.expand(payload, outline, margin=0)
    assert expanded["plan"]["density"] == "detailed"
    assert "30 字节/秒" in expanded["plan"]["density_reason"]

    # 扩写时重报一个更低的数以新的为准（宿主中途换模型、并发被压下来都会改吞吐）
    slower = engine.expand(payload, outline, margin=0, output_bps=6.0)
    assert slower["plan"]["density"] == "concise"


def test_finish_accepts_a_lean_topic_body_unchanged() -> None:
    """精简档的回答必须原样吃下：条数少不是漏答，不该出警告、更不能退回大纲。"""
    engine = _unconfigured_engine()
    payload = engine.prepare(load_transcript(_write(SPLIT_PLAIN)), split_topics=True, density="concise")
    expanded = engine.expand(payload, json.dumps(SPLIT_OUTLINE, ensure_ascii=False), margin=0)
    summary = {key: value for key, value in SUMMARY.items() if key not in ("chapters", "mindmap")}
    lean_body = {"focus": ["部署聚焦上线时间"], "sections": [
        {"title": "上线安排", "groups": [{"title": "责任到人", "items": ["李四跟进部署"]}]}
    ], "insights": []}
    answers = {
        "summary": json.dumps(dict(summary, keywords=["部署"]), ensure_ascii=False),
        "topics#1": json.dumps(lean_body, ensure_ascii=False),
        "topics#2": json.dumps(lean_body, ensure_ascii=False),
    }
    result = engine.finish(expanded, answers, out_dir=None)

    # 条数少不等于漏答：不能因为走了精简档就警告或退回大纲
    assert [w for w in result.warnings if "议题" in w or "topics" in w] == []
    assert result.summary["topics"][0]["sections"][0]["groups"][0]["items"] == ["李四跟进部署"]
    assert result.summary["topics"][0]["focus"] == ["部署聚焦上线时间"]


def test_cli_prepare_and_expand_report_the_density(tmp_path: Path, capsys) -> None:
    """档位与理由要出现在 `next` 里：宿主照着 next 做，看不到就会自己加戏写长。"""
    from minutecraft.cli import main

    raw = tmp_path / "meeting.txt"
    raw.write_text(_minutes_text(40), encoding="utf-8")
    request = tmp_path / "request.json"
    assert main(["prepare", str(raw), "-o", str(request), "--split-topics",
                 "--output-bps", "12", "--json"]) == 0
    emitted = json.loads(capsys.readouterr().out)
    assert emitted["plan"]["density"] == "concise"
    assert "[正文] concise 档" in emitted["next"]

    outline = tmp_path / "outline.json"
    outline.write_text(json.dumps(SPLIT_OUTLINE, ensure_ascii=False), encoding="utf-8")
    expanded = tmp_path / "expand.json"
    assert main(["expand", str(request), "--outline", str(outline), "-o", str(expanded),
                 "--density", "detailed", "--json"]) == 0
    after = json.loads(capsys.readouterr().out)
    assert after["plan"]["density"] == "detailed"
    assert after["next"].startswith("[正文] detailed 档")
