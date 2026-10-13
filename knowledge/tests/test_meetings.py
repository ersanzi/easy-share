"""会议听记：区块级状态、关键词、待办实体的回归用例（不起 FunASR、不调 LLM）。"""
from __future__ import annotations

import json
import time

import pytest
import numpy as np

from app.meetings import mindmap_harness
from app.meetings.service import MeetingService
from app.meetings.store import MeetingStore, _ensure_schema
from app.meetings.summarizer import MeetingSummarizer

SEGMENTS = [
    {"start": 0.0, "end": 3.2, "text": "这季度上线听记", "speaker": "0"},
    {"start": 3.5, "end": 8.0, "text": "周五前给排期", "speaker": "1"},
]


class FakeTranscriber:
    def __init__(self, segments=None, error: Exception | None = None) -> None:
        self._segments = segments if segments is not None else SEGMENTS
        self._error = error
        self.hotword_used = ""

    def transcribe(self, wav, hotword: str = ""):  # noqa: ANN001 - 与 MeetingTranscriber 同签名
        self.hotword_used = hotword
        if self._error:
            raise self._error
        return [dict(segment) for segment in self._segments]


class FakeSummarizer:
    def __init__(self, summary: dict | None, keywords: list[str] | None) -> None:
        self._summary = summary
        self._keywords = keywords
        self.configured = True

    def summarize(self, transcript, template=None):  # noqa: ANN001
        return None if self._summary is None else {k: (list(v) if isinstance(v, list) else v) for k, v in self._summary.items()}

    def extract_keywords(self, transcript):  # noqa: ANN001
        return None if self._keywords is None else list(self._keywords)

    def ask_json(self, system, user):  # noqa: ANN001
        return None


@pytest.fixture(autouse=True)
def _no_real_audio(monkeypatch):
    """转写产物是内存夹具，绕开真实 WAV 解析。"""
    monkeypatch.setattr("app.meetings.service.audio_duration", lambda path: 8)


def _service(tmp_path, transcriber, summarizer) -> MeetingService:
    store = MeetingStore(tmp_path / "meetings")
    return MeetingService(store=store, transcriber=transcriber, summarizer=summarizer)  # type: ignore[arg-type]


def _finish(service: MeetingService, meeting_id: str) -> dict:
    """同步跑一次 worker 逻辑（绕开线程池，断言确定性）。"""
    service._process(meeting_id)  # noqa: SLF001 - 单测直调管线
    return service.store.get(meeting_id)


def test_new_record_carries_blocks_keywords_and_todos(tmp_path) -> None:
    meta = MeetingStore(tmp_path).create(title="周会", owner=None)
    assert meta["blocks"] == {"transcript": "pending", "keywords": "pending", "summary": "pending", "mindmap": "pending"}
    assert meta["keywords"] == [] and meta["todos"] == []


def test_legacy_record_is_upgraded_on_read(tmp_path) -> None:
    """本轮之前落盘的记录：无 blocks/keywords/todos，待办还长在 summary 里。"""
    directory = tmp_path / "meetings"
    legacy = {
        "id": "old1", "title": "旧会议", "created_at": "2026-10-12T10:00:00", "owner": None,
        "status": "completed", "error": None, "duration_seconds": 60, "speaker_count": 1,
        "speaker_names": {"0": "张三"}, "segments": SEGMENTS,
        "summary": {"minutes": "纪要", "key_points": [], "todos": [{"task": "补文档", "owner_hint": "李四", "due_hint": "周五"}]},
        "mindmap": "大纲",
    }
    (directory).mkdir(parents=True)
    (directory / "old1.json").write_text(json.dumps(legacy, ensure_ascii=False), encoding="utf-8")

    meta = _ensure_schema(json.loads(json.dumps(legacy)))
    assert meta["blocks"]["transcript"] == "ready" and meta["blocks"]["summary"] == "ready"
    assert meta["blocks"]["keywords"] == "skipped"
    assert meta["todos"][0]["task"] == "补文档" and meta["todos"][0]["source"] == "ai"

    store = MeetingStore(directory)
    assert store.get("old1")["todos"][0]["owner_hint"] == "李四"
    assert store.list()[0]["todos"][0]["task"] == "补文档"


def test_process_sets_block_states_and_materialises_entities(tmp_path, monkeypatch) -> None:
    monkeypatch.setattr(mindmap_harness, "build_mindmap_harness", lambda *a, **kw: ("# 主题\n## 议题\n- 要点", ["harness ok"]))
    summarizer = FakeSummarizer(
        summary={
            "minutes": "确定本季度上线听记",
            "key_points": ["周五前给排期"],
            "chapters": [{"title": "排期", "start": 3, "end": 8, "gist": "定排期"}],
            "todos": [{"task": "给排期", "owner_hint": "李四", "due_hint": "周五"}],
            "mindmap": "不该用这个",
        },
        keywords=["听记", "排期"],
    )
    service = _service(tmp_path, FakeTranscriber(), summarizer)
    try:
        meta = service.store.create(title="周会", owner=None)
        record = _finish(service, meta["id"])

        assert record["status"] == "completed"
        assert record["blocks"] == {"transcript": "ready", "keywords": "ready", "summary": "ready", "mindmap": "ready"}
        assert record["keywords"] == ["听记", "排期"]
        # 待办成为顶层实体，带 id 与完成态；总结里不再留 todos
        assert record["todos"][0]["task"] == "给排期" and record["todos"][0]["done"] is False
        assert record["todos"][0]["source"] == "ai" and record["todos"][0]["id"]
        assert "todos" not in (record["summary"] or {})
        assert record["summary"]["chapters"][0]["start"] == 3
        assert record["mindmap"].startswith("# 主题")
    finally:
        service.shutdown()


def test_summary_failure_only_fails_that_block(tmp_path, monkeypatch) -> None:
    monkeypatch.setattr(mindmap_harness, "build_mindmap_harness", lambda *a, **kw: ("# 主题", []))
    service = _service(tmp_path, FakeTranscriber(), FakeSummarizer(summary=None, keywords=None))
    try:
        meta = service.store.create(title="无 LLM 的会议", owner=None)
        record = _finish(service, meta["id"])

        assert record["status"] == "completed"  # 转写成功就仍是可用记录
        assert record["blocks"]["transcript"] == "ready"
        assert record["blocks"]["summary"] == "failed" and record["blocks"]["keywords"] == "failed"
        assert record["blocks"]["mindmap"] == "ready"
        assert "LLM" in record["block_errors"]["summary"]
    finally:
        service.shutdown()


def test_transcription_failure_fails_whole_record(tmp_path) -> None:
    service = _service(tmp_path, FakeTranscriber(error=RuntimeError("音频损坏")), FakeSummarizer({}, []))
    try:
        meta = service.store.create(title="坏音频", owner=None)
        record = _finish(service, meta["id"])
        assert record["status"] == "failed" and record["error"] == "音频损坏"
        assert record["blocks"]["transcript"] == "failed"
    finally:
        service.shutdown()


def test_todo_crud_keeps_manual_todo_when_summary_regenerated(tmp_path, monkeypatch) -> None:
    monkeypatch.setattr(mindmap_harness, "build_mindmap_harness", lambda *a, **kw: ("# 主题", []))
    service = _service(tmp_path, FakeTranscriber(), FakeSummarizer(
        summary={"minutes": "纪要", "key_points": [], "todos": [{"task": "AI 待办", "owner_hint": "", "due_hint": ""}]},
        keywords=[],
    ))
    try:
        meta = service.store.create(title="周会", owner=None)
        _finish(service, meta["id"])
        meeting_id = meta["id"]

        manual = service.add_todo(meeting_id, "手工待办", "王五")
        assert manual["source"] == "manual" and manual["id"]

        toggled = service.store.get(meeting_id)["todos"][0]
        service.update_todo(meeting_id, toggled["id"], done=True)
        assert service.store.get(meeting_id)["todos"][0]["done"] is True

        service.remove_todo(meeting_id, manual["id"])
        remaining = service.store.get(meeting_id)["todos"]
        assert [todo["id"] for todo in remaining] == [toggled["id"]]

        service.update_todo(meeting_id, toggled["id"], task="改过的内容")
        assert service.store.get(meeting_id)["todos"][0]["task"] == "改过的内容"

        with pytest.raises(ValueError):
            service.update_todo(meeting_id, "不存在的id", task="x")
        with pytest.raises(ValueError):
            service.add_todo(meeting_id, "   ")

        # 重生成总结：AI 待办整体刷新，用户手工新增的保留
        service.store.add_todo(meeting_id, "手工项", "王五")
        service.regenerate_summary(meeting_id, "meeting-minutes")
        refreshed = service.store.get(meeting_id)["todos"]
        assert [todo["task"] for todo in refreshed] == ["手工项", "AI 待办"]
        assert [todo["source"] for todo in refreshed] == ["manual", "ai"]
    finally:
        service.shutdown()


def test_regenerate_keywords_flips_block_state(tmp_path, monkeypatch) -> None:
    monkeypatch.setattr(mindmap_harness, "build_mindmap_harness", lambda *a, **kw: ("# 主题", []))
    service = _service(tmp_path, FakeTranscriber(), FakeSummarizer(summary={"minutes": "纪要", "key_points": [], "todos": []}, keywords=None))
    try:
        meta = service.store.create(title="周会", owner=None)
        meeting_id = meta["id"]
        _finish(service, meeting_id)
        assert service.store.get(meeting_id)["blocks"]["keywords"] == "failed"

        service.summarizer = FakeSummarizer(summary={}, keywords=["听记"])
        record = service.regenerate_keywords(meeting_id)
        assert record["keywords"] == ["听记"] and record["blocks"]["keywords"] == "ready"
    finally:
        service.shutdown()


def test_extract_keywords_normalises_llm_output(monkeypatch) -> None:
    summarizer = MeetingSummarizer(base_url="http://x/v1", api_key="k", model="m")
    monkeypatch.setattr(
        summarizer,
        "ask_json",
        lambda system, user: {"keywords": ["  听记  ", "听记", "排期计划评审会议专项", "", 1234, None] + [f"词{i}" for i in range(20)]},
    )
    keywords = summarizer.extract_keywords("转写正文")
    assert keywords is not None
    assert keywords[0] == "听记" and "听记" in keywords            # 去空白且去重
    assert all(len(word) <= 12 for word in keywords)              # 12 字截断
    assert len(keywords) <= 10                                    # 最多 10 个
    assert "" not in keywords


def test_extract_keywords_returns_none_without_llm_or_audio() -> None:
    assert MeetingSummarizer(base_url="", api_key="", model="").extract_keywords("正文") is None
    configured = MeetingSummarizer(base_url="http://x/v1", api_key="k", model="m")
    configured._client = lambda: (_ for _ in ()).throw(RuntimeError("network down"))  # noqa: SLF001
    assert configured.extract_keywords("正文") is None


# ─── 实时上屏（流式会话逻辑，不依赖真实模型）───


def _pcm(samples: int) -> bytes:
    return bytes(samples * 2)


def test_stream_batches_until_chunk_boundary(monkeypatch) -> None:
    """不足 600ms 不推模型；攒够一片才推一次，尾数留到下轮。"""
    from app.meetings.stream import CHUNK_BYTES, CHUNK_SAMPLES, StreamingTranscriber

    streamer = StreamingTranscriber()
    calls: list[int] = []
    monkeypatch.setattr(streamer, "_ensure_model", lambda: object())
    monkeypatch.setattr(
        streamer,
        "_recognize",
        lambda model, session, piece, is_final=False: calls.append(len(piece)) or f"字{len(calls)}",
    )

    streamer.start("m1")
    view = streamer.feed("m1", _pcm(CHUNK_SAMPLES // 2 - 50))
    assert calls == [] and view["lines"] == [] and view["current"] == ""

    streamer.feed("m1", _pcm(CHUNK_SAMPLES // 2 + 50))  # 两片合起来刚好凑满一片
    assert calls == [CHUNK_BYTES]
    view = streamer.feed("m1", _pcm(CHUNK_SAMPLES * 2))
    assert len(calls) == 3
    assert view["current"] == "字1字2字3"
    assert view["elapsed_seconds"] == 1  # 3 片 × 9600 样本 = 1.8s，向下取整
    assert streamer.snapshot("m1")["current"] == view["current"]


def test_stream_breaks_lines_on_punctuation_and_flushes_on_stop(monkeypatch) -> None:
    from app.meetings.stream import CHUNK_SAMPLES, StreamingTranscriber

    streamer = StreamingTranscriber()
    texts = iter(["今天先把排期定了。", "然后客户演示"])
    monkeypatch.setattr(streamer, "_ensure_model", lambda: object())
    monkeypatch.setattr(
        streamer,
        "_recognize",
        lambda model, session, piece, is_final=False: "尾巴" if is_final else next(texts, ""),
    )

    streamer.start("m2")
    streamer.feed("m2", _pcm(CHUNK_SAMPLES))  # 一句带句号 → 立即成行
    # 一片 + 零头：零头留在 buffer 里，等 stop 用 is_final 冲掉
    view = streamer.feed("m2", _pcm(CHUNK_SAMPLES + 100))
    assert len(view["lines"]) == 1 and view["lines"][0]["text"] == "今天先把排期定了"
    assert view["current"] == "然后客户演示"

    final = streamer.stop("m2")
    assert len(final["lines"]) == 2
    assert final["lines"][-1]["text"] == "然后客户演示尾巴"  # 尾包内容并入同一行
    assert final["current"] == ""
    assert streamer.snapshot("m2")["lines"] == []  # 会话已回收


def test_live_points_normalises_and_caps(monkeypatch) -> None:
    summarizer = MeetingSummarizer(base_url="http://x/v1", api_key="k", model="m")
    monkeypatch.setattr(
        summarizer,
        "ask_json",
        lambda system, user: {"points": ["  确定排期  ", "确定排期", "", "超长" * 30]},
    )
    points = summarizer.live_points("转写内容")
    assert points[0] == "确定排期"
    assert all(len(point) <= 40 for point in points)
    assert len(points) <= 6
    assert MeetingSummarizer(base_url="", api_key="", model="").live_points("正文") == []


def test_stream_session_reuse_on_upload(tmp_path) -> None:
    """先开实时会话再上传整段：必须复用同一条记录，不落两条听记。"""
    import asyncio

    service = _service(tmp_path, FakeTranscriber(), FakeSummarizer(summary={"minutes": "x"}, keywords=[]))
    try:
        meta = service.store.create(title="实时场次", owner=None, status="recording")

        class FakeAudio:
            def __init__(self, payload: bytes) -> None:
                self._payload = payload
                self._done = False

            async def read(self, _size: int) -> bytes:
                if self._done:
                    return b""
                self._done = True
                return self._payload

        reused = asyncio.run(service.save_upload(FakeAudio(b"RIFFfake"), "改名后的场次", None, 10**6, "meeting-minutes", meta["id"]))
        assert reused["id"] == meta["id"]
        assert reused["status"] == "processing"
        assert len(service.store.list()) == 1
    finally:
        service.shutdown()


# ─── 个性化热词（校验口径取自钉钉文案：中英日混合上限、纯英文词数上限）───


def test_hotword_validation_rules() -> None:
    from app.meetings.hotwords import HotwordError, validate

    assert validate("  文档入库  ") == "文档入库"
    assert validate("Q3 Roadmap Review") == "Q3 Roadmap Review"
    for bad in ["", "   ", "知识库@v2", "一二三四五六七八九十加", "one two three four five six"]:
        with pytest.raises(HotwordError):
            validate(bad)


def test_hotword_store_scopes_per_user_and_returns_space_joined_phrase(tmp_path) -> None:
    from app.meetings.hotwords import HotwordStore

    store = HotwordStore(tmp_path / "hotwords.json")
    store.add("alice", ["文档入库", "知识库", "文档入库"])
    store.add("bob", ["来风"])
    assert store.list("alice") == ["文档入库", "知识库"]
    assert store.asr_phrase("alice") == "文档入库 知识库"  # seaco 只吃空格分隔字符串
    assert store.asr_phrase("bob") == "来风"
    assert store.asr_phrase(None) == ""
    assert store.remove("alice", "知识库") == ["文档入库"]


def test_hotword_store_rejects_over_cap_and_bad_charset(tmp_path) -> None:
    from app.meetings.hotwords import HotwordError, HotwordStore, MAX_WORDS_PER_USER

    store = HotwordStore(tmp_path / "hotwords.json")
    with pytest.raises(HotwordError):
        store.add("alice", ["坏词#1"])
    with pytest.raises(HotwordError):
        store.add("alice", [f"词{i}" for i in range(MAX_WORDS_PER_USER + 1)])


def test_transcribe_receives_owner_hotwords(tmp_path, monkeypatch) -> None:
    from app.meetings.hotwords import HotwordStore

    monkeypatch.setattr(mindmap_harness, "build_mindmap_harness", lambda *a, **kw: ("# 主题", []))
    transcriber = FakeTranscriber()
    service = _service(tmp_path, transcriber, FakeSummarizer(summary={"minutes": "x"}, keywords=[]))
    service.hotwords = HotwordStore(tmp_path / "hotwords.json")
    try:
        service.hotwords.add("alice", ["会议听记"])
        meta = service.store.create(title="周会", owner="alice")
        _finish(service, meta["id"])
        assert transcriber.hotword_used == "会议听记"

        other = service.store.create(title="别人的会", owner="bob")
        _finish(service, other["id"])
        assert transcriber.hotword_used == ""  # 词表按用户隔离，不串号
    finally:
        service.shutdown()


def test_retranscribe_resets_blocks_and_requeues(tmp_path, monkeypatch) -> None:
    monkeypatch.setattr(mindmap_harness, "build_mindmap_harness", lambda *a, **kw: ("# 主题", []))
    service = _service(tmp_path, FakeTranscriber(), FakeSummarizer(summary={"minutes": "纪要"}, keywords=["听记"]))
    try:
        meta = service.store.create(title="周会", owner=None)
        service.store.audio_path(meta["id"]).write_bytes(b"RIFFfake-wav")  # 重转写要有原始音频
        _finish(service, meta["id"])
        service.store.update(meta["id"], keywords=["旧词"])

        updated = service.retranscribe(meta["id"])
        assert updated["status"] == "processing"
        assert set(updated["blocks"].values()) == {"pending"}

        # 排队任务跑完后关键词被重算
        for _ in range(200):
            if service.store.get(meta["id"])["status"] == "completed":
                break
            time.sleep(0.02)
        record = service.store.get(meta["id"])
        assert record["keywords"] == ["听记"]
        assert record["blocks"]["keywords"] == "ready"

        # 与同层其他方法一致：记录不存在抛 KeyError，由路由翻成 404
        with pytest.raises(KeyError):
            service.retranscribe("不存在的id")
    finally:
        service.shutdown()


def test_retranscribe_refuses_when_audio_missing(tmp_path) -> None:
    service = _service(tmp_path, FakeTranscriber(), FakeSummarizer(summary={}, keywords=[]))
    try:
        meta = service.store.create(title="没音频", owner=None)
        with pytest.raises(ValueError):
            service.retranscribe(meta["id"])
    finally:
        service.shutdown()


# ─── 声纹（轻口径：标注过的发言人下次自动认）───


class FakeSvModel:
    """替身声纹模型：按输入音频字节数返回预定向量，避免真跑 cam++。"""

    def __init__(self, vectors: list[np.ndarray]) -> None:
        self._vectors = vectors
        self.calls = 0

    def generate(self, input=None, **kwargs):  # noqa: A002 - funasr 形参名就是 input
        vector = self._vectors[min(self.calls, len(self._vectors) - 1)]
        self.calls += 1
        return [{"spk_embedding": vector.reshape(1, -1)}]


def _write_wav(path, seconds: float, rate: int = 16000) -> None:
    import wave as _wave

    with _wave.open(str(path), "wb") as handle:
        handle.setnchannels(1)
        handle.setsampwidth(2)
        handle.setframerate(rate)
        handle.writeframes(np.zeros(int(seconds * rate), dtype=np.int16).tobytes())


def _voiceprint_store(vectors) -> object:
    from app.meetings.voiceprint import VoiceprintStore

    store = VoiceprintStore("/tmp/unused.json")
    store._sv = FakeSvModel(vectors)  # noqa: SLF001
    return store


def test_voiceprint_skips_too_short_samples(tmp_path) -> None:
    from app.meetings.voiceprint import _cosine

    vector = np.array([1.0] + [0.0] * 191, dtype=np.float32)
    store = _voiceprint_store([vector])
    wav = tmp_path / "a.wav"
    _write_wav(wav, 10)
    segments = [{"speaker": "0", "start": 0.0, "end": 1.0, "text": "太短"}]
    got, seconds = store.embedding_of_speaker(wav, segments, "0")
    assert got is None and seconds == 0.0
    assert _cosine(vector, vector) == pytest.approx(1.0)


def test_voiceprint_enroll_match_and_isolation(tmp_path) -> None:
    from app.meetings.voiceprint import MATCH_THRESHOLD, VoiceprintStore

    enrolled = np.zeros(192, dtype=np.float32)
    enrolled[0] = 1.0
    sameish = np.zeros(192, dtype=np.float32)
    sameish[0] = 0.9
    sameish[1] = 0.1  # 与已登记向量余弦≈0.993 > 阈值
    stranger = np.zeros(192, dtype=np.float32)
    stranger[1] = 1.0  # 正交，余弦 0 < 阈值

    path = tmp_path / "voiceprints.json"
    store = VoiceprintStore(path)
    store._sv = FakeSvModel([enrolled])  # noqa: SLF001
    wav = tmp_path / "a.wav"
    _write_wav(wav, 12)
    segments = [{"speaker": "0", "start": 0.0, "end": 10.0, "text": "我说的话"}]

    assert store.enroll("alice", "张三", wav, segments, "0")["name"] == "张三"
    listed = store.list("alice")
    assert listed[0]["name"] == "张三" and listed[0]["sample_seconds"] == 10.0
    assert "vector" not in listed[0]  # 列表不外泄向量本体

    # 另一个人（不同场次）来认领：抽到的向量与张三很像 → 命中
    store_other = VoiceprintStore(path)
    store_other._sv = FakeSvModel([sameish])  # noqa: SLF001
    other_wav = tmp_path / "b.wav"
    _write_wav(other_wav, 12)
    matched = store_other.recognize("alice", other_wav, [{"speaker": "1", "start": 0.0, "end": 10.0, "text": "x"}])
    assert matched == {"1": "张三"}

    # 陌生人正交向量：不命中（阈值确实起作用）
    store_third = VoiceprintStore(path)
    store_third._sv = FakeSvModel([stranger])  # noqa: SLF001
    assert MATCH_THRESHOLD > 0.5
    assert store_third.recognize("alice", other_wav, [{"speaker": "1", "start": 0.0, "end": 10.0, "text": "x"}]) == {}

    # 按用户隔离：bob 名下没有登记，认不出张三
    assert store_other.recognize("bob", other_wav, [{"speaker": "1", "start": 0.0, "end": 10.0, "text": "x"}]) == {}

    assert store.remove("alice", "张三") == []
    assert not path.read_text(encoding="utf-8").count("张三")


def test_apply_voiceprints_keeps_manual_names_and_rename_enrolls(tmp_path) -> None:
    vector = np.zeros(192, dtype=np.float32)
    vector[0] = 1.0
    store = _voiceprint_store([vector])
    recorded: list[tuple] = []

    def fake_enroll(owner, name, wav_path, segments, speaker):
        recorded.append((owner, name, speaker))
        return {"name": name}

    monkey_store = store
    monkey_store.enroll = fake_enroll  # type: ignore[method-assign]
    monkey_store.recognize = lambda owner, wav_path, segments: {"0": "张三", "1": "李四"}  # type: ignore[assignment]

    service = _service(tmp_path, FakeTranscriber(), FakeSummarizer(summary={"minutes": "x"}, keywords=[]))
    service.voiceprints = monkey_store
    try:
        names = {"0": "说话人 1", "1": "王五"}
        assert service.apply_voiceprints("alice", "wav", SEGMENTS, names) == 1
        assert names == {"0": "张三", "1": "王五"}  # 人工改过的名字不被声纹覆盖

        service.store.audio_path("x").parent.mkdir(parents=True, exist_ok=True)
        meta = service.store.create(title="周会", owner="alice")
        service.store.update(meta["id"], segments=SEGMENTS)
        updated = service.rename_speaker(meta["id"], "0", "赵六")
        assert updated["speaker_names"]["0"] == "赵六"
        assert recorded and recorded[0][0] == "alice" and recorded[0][2] == "0"
    finally:
        service.shutdown()
