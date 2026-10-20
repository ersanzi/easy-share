"""会议听记编排：上传落盘 → 单 worker 串行转写 → LLM 总结 → 落盘。

单 worker 刻意串行：FunASR 常驻内存约 2GB，多任务并发只会互相挤内存，
排队是更可预期的行为。
"""
from __future__ import annotations

import logging
import re
import time
from concurrent.futures import ThreadPoolExecutor

from app.meetings.engine import (  # 薄壳：实现见 engine/minutecraft
    MeetingTranscriber,
    audio_duration,
    render_transcript,
)
from app.meetings.templates import find_template
from app.meetings.store import MeetingStore, _blank_blocks
from app.meetings.summarizer import MeetingSummarizer

logger = logging.getLogger(__name__)

# 落盘拷贝的分块大小（FastAPI UploadFile 的 SpooledTemporaryFile → 目标 wav）
_COPY_CHUNK = 1024 * 1024

# 引擎给的默认占位名（“说话人 1”）：改回它不视为真人标注，不沉淀声纹
_DEFAULT_SPEAKER_NAME = re.compile(r"^说话人\s*\d+$")


class MeetingService:
    def __init__(
        self,
        store: MeetingStore,
        transcriber: MeetingTranscriber,
        summarizer: MeetingSummarizer,
        streamer=None,
        hotwords=None,
        voiceprints=None,
    ) -> None:
        self.store = store
        self.transcriber = transcriber
        self.summarizer = summarizer
        self.streamer = streamer
        self.hotwords = hotwords
        self.voiceprints = voiceprints
        self._executor = ThreadPoolExecutor(max_workers=1, thread_name_prefix="meeting")

    async def save_upload(
        self,
        audio,
        title: str,
        owner: str | None,
        max_bytes: int,
        template_id: str | None = None,
        meeting_id: str | None = None,
    ) -> dict:
        """把上传的音频流式落盘并登记 processing 记录，返回会议元数据。

        meeting_id 用于「先开实时会话边录边上屏、录完再上传整段」的路径：
        复用同一条记录，不新建，避免一场会出现两条听记。
        """
        reused = None
        if meeting_id:
            try:
                candidate = self.store.get(meeting_id)
            except KeyError:
                candidate = None
            if candidate is not None and candidate.get("status") in ("recording", "failed"):
                reused = candidate
        meta = reused or self.store.create(title=title, owner=owner, template_id=template_id)
        target = self.store.audio_path(meta["id"])
        size = 0
        try:
            with target.open("wb") as sink:
                while chunk := await audio.read(_COPY_CHUNK):
                    size += len(chunk)
                    if size > max_bytes:
                        raise ValueError(f"音频超过大小上限 {max_bytes // (1024 * 1024)}MB")
                    sink.write(chunk)
        except Exception:
            if reused is None:
                target.unlink(missing_ok=True)
                self.store.delete(meta["id"])
            raise
        if size == 0:
            if reused is None:
                target.unlink(missing_ok=True)
                self.store.delete(meta["id"])
            raise ValueError("音频内容为空")
        if reused is not None:
            meta = self.store.update(meta["id"], status="processing", error=None, blocks=_blank_blocks())
        return meta

    # --- 实时上屏（边说边出字，预览性质；终态仍以离线整段重跑为准） ---

    def open_stream(self, title: str, owner: str | None, template_id: str | None) -> dict:
        """开一场录制会话：先落一条 recording 记录，返回给前端当流 id 用。"""
        if self.streamer is None:
            raise ValueError("实时转写未启用（流式模型不可用）")
        meta = self.store.create(title=title, owner=owner, template_id=template_id, status="recording")
        # 带上 owner：推流时按他的热词表做识别增强（与离线转写同口径）
        self.streamer.start(meta["id"], owner=owner)
        return meta

    def feed_stream(self, meeting_id: str, pcm: bytes, owner: str | None = None) -> dict:
        if self.streamer is None:
            raise ValueError("实时转写未启用（流式模型不可用）")
        return self.streamer.feed(meeting_id, pcm, hotword=self.hotword_phrase(owner))

    def close_stream(self, meeting_id: str) -> dict:
        if self.streamer is None:
            raise ValueError("实时转写未启用（流式模型不可用）")
        return self.streamer.stop(meeting_id)

    def live_snapshot(self, meeting_id: str, owner: str | None = None) -> dict:
        """不推音频也能取当前上屏状态（前端轮询兜底：推流断了字也不会停在半路）。"""
        if self.streamer is None:
            raise ValueError("实时转写未启用（流式模型不可用）")
        return self.streamer.snapshot(meeting_id, hotword=self.hotword_phrase(owner))

    def live_check(self, meeting_id: str) -> dict:
        """会中快检：拿**最近几行**（不是整场）让 LLM 核对事实口径，几秒内返回。

        对标钉钉听记「边说边问答/事实核对」的价值位：整场级要点要等总结，
        会中真正能救场的是「刚才这句话里数字对不上」。这里刻意只看尾部，
        既省 token 也让结果跟着话题走。
        """
        if self.streamer is None:
            raise ValueError("实时转写未启用（流式模型不可用）")
        material, last_at = self.streamer.recent_text(meeting_id, count=4)
        checked_at = time.strftime("%H:%M:%S")
        if not material.strip():
            return {"verdict": "unclear", "note": "还没有听到可核对的内容", "points": [],
                    "last_line_at": last_at, "checked_at": checked_at}
        checker = getattr(self.summarizer, "live_check", None)
        result = checker(material) if callable(checker) else None
        if result is None:
            return {"verdict": "unavailable", "note": "AI 快检暂不可用（未配置模型或调用失败）", "points": [],
                    "last_line_at": last_at, "checked_at": checked_at}
        return {**result, "last_line_at": last_at, "checked_at": checked_at}

    def live_points(self, meeting_id: str) -> list[str]:
        """会中要点（对标钉钉实时摘要树）：拿已上屏的行让 LLM 归纳几条，随录随长。"""
        if self.streamer is None:
            raise ValueError("实时转写未启用（流式模型不可用）")
        lines = self.streamer.active_lines(meeting_id)
        if not lines:
            return []
        return self.summarizer.live_points("\n".join(lines))

    def submit(self, meeting_id: str) -> None:
        self._executor.submit(self._process, meeting_id)

    def hotword_phrase(self, owner: str | None) -> str:
        """该用户的热词串（无词表时为空串，引擎按无热词转写）。"""
        return self.hotwords.asr_phrase(owner) if self.hotwords is not None else ""

    def retranscribe(self, meeting_id: str) -> dict:
        """重新转写（对标钉钉独立入口）：加了热词后让老记录重跑一遍全管线。"""
        meta = self.store.get(meeting_id)
        if meta.get("status") == "processing":
            raise ValueError("本场听记正在处理中，稍后再试")
        if not self.store.audio_path(meeting_id).is_file():
            raise ValueError("原始音频已不在，无法重新转写")
        updated = self.store.update(meeting_id, status="processing", error=None, blocks=_blank_blocks())
        self.submit(meeting_id)
        return updated

    def shutdown(self) -> None:
        self._executor.shutdown(wait=False)

    # --- 内部：worker 线程执行 ---

    def initial_speaker_names(self, meta: dict, segments: list[dict]) -> dict[str, str]:
        """说话人展示名：默认「说话人 N」。

        重新转写时保住用户改过的名字——但有前提：**不能把人工标注张冠李戴**。
        引擎会重新聚类、跨场按声纹认领，序号的含义不保证和上次一样；
        无脑沿用旧名字会让「张三」安到别人头上。所以：
          1. 声纹认领到的人优先（同一人跨场通常序号也会一致，但仍以声纹为准）；
          2. 旧名字与声纹冲突的一律不沿用；
          3. 没有冲突证据时才沿用用户标注（避免一次重跑把人工工作全抹掉）。
        """
        previous = meta.get("speaker_names") or {}
        names: dict[str, str] = {}
        for speaker in sorted({str(segment["speaker"]) for segment in segments}):
            old = str(previous.get(speaker) or "")
            names[speaker] = old if old and not old.startswith("说话人 ") else f"说话人 {int(speaker) + 1}"
        return names

    def apply_voiceprints(self, owner: str | None, wav, segments: list[dict], names: dict[str, str]) -> int:
        """用已登记声纹自动认领发言人。

        - 仍是默认名（「说话人 N」）的：命中也写上；
        - 有个人工标注但与声纹**指向另一人**的：以声纹为准（模型重聚类后序号变了，
          硬留旧名就是张冠李戴），并记一条日志方便排查；
        - 与声纹一致的标注：保持不动。
        """
        if self.voiceprints is None or not segments:
            return 0
        try:
            matched = self.voiceprints.recognize(owner, wav, segments)
        except Exception:  # noqa: BLE001 - 声纹是加分项，失败不影响转写产物
            logger.warning("声纹识别失败，跳过自动认领: owner=%s", owner, exc_info=True)
            return 0
        applied = 0
        for speaker, name in matched.items():
            current = names.get(speaker) or ""
            if not current or current.startswith("说话人 "):
                names[speaker] = name
                applied += 1
            elif current != name:
                logger.info("声纹覆盖人工标注（序号已重排）: owner=%s 说话人%s %s→%s", owner, speaker, current, name)
                names[speaker] = name
                applied += 1
        if applied:
            logger.info("声纹自动认领 %d 位发言人: owner=%s", applied, owner)
        return applied

    def rename_speaker(self, meeting_id: str, speaker: str, name: str) -> dict:
        """改名，并把这次标注沉淀成声纹（样本时长不足则只改名，不报错）。"""
        meta = self.store.rename_speaker(meeting_id, speaker, name)
        # 「说话人 N」是默认占位名，不是真人标注：改回默认名时不该生成同名声纹，
        # 否则以后每场都会多出一条叫「说话人 1」的声纹，列表越用越脏。
        if self.voiceprints is not None and not _DEFAULT_SPEAKER_NAME.match(name):
            try:
                self.voiceprints.enroll(
                    meta.get("owner"), name, self.store.audio_path(meeting_id), meta.get("segments") or [], speaker
                )
            except Exception:  # noqa: BLE001 - 声纹沉淀失败不影响改名本身
                logger.warning("声纹登记失败: meeting=%s speaker=%s", meeting_id, speaker, exc_info=True)
        return meta

    def merge_speakers(self, meeting_id: str, from_speaker: str, into_speaker: str) -> dict:
        """把一位发言人的全部轮次并给另一位（对标钉钉「合并发言人」）。

        用途：说话人分离偶尔把人拆成两簇（同一人 0/3），或两簇其实是同一人。
        合并只动转写分段与发言人表：不动音频、不动已经生成好的总结
        （两者都被改过，提示用户重生成即可）。
        """
        from_speaker = str(from_speaker or "").strip()
        into_speaker = str(into_speaker or "").strip()
        if not from_speaker or not into_speaker:
            raise ValueError("请指定要合并的两位发言人")
        if from_speaker == into_speaker:
            raise ValueError("不能把发言人合并到他自己身上")
        return self.store.merge_speakers(meeting_id, from_speaker, into_speaker)

    def update_segment(self, meeting_id: str, index: int, text: str) -> dict:
        """改一句转写文字（对标钉钉逐字稿可编辑）。

        「说话人 N」与别的展示口径都在别处，这里只改字；顺带把改过的句子
        记进 `edited_segments`，重新转写时不会被整段覆盖得无声无息。
        """
        text = (text or "").strip()
        if not text:
            raise ValueError("转写内容不能为空")
        meta = self.store.update_segment_text(meeting_id, index, text)
        edited = list(meta.get("edited_segments") or [])
        if index not in edited:
            edited.append(index)
        return self.store.update(meeting_id, edited_segments=sorted(edited))

    def _process(self, meeting_id: str) -> None:
        meta = self.store.get(meeting_id)
        wav = self.store.audio_path(meeting_id)
        try:
            self.store.set_block(meeting_id, "transcript", "processing")
            segments = self.transcriber.transcribe(wav, hotword=self.hotword_phrase(meta.get("owner")))
            speaker_names = self.initial_speaker_names(meta, segments)
            self.apply_voiceprints(meta.get("owner"), wav, segments, speaker_names)
            self.store.update(
                meeting_id,
                segments=segments,
                speaker_names=speaker_names,
                speaker_count=len(speaker_names),
                duration_seconds=audio_duration(wav),
            )
            self.store.set_block(meeting_id, "transcript", "ready")
        except Exception as exc:  # noqa: BLE001 - 转写是其他区块的前提，失败即整体失败
            logger.exception("会议转写失败: id=%s", meeting_id)
            self.store.set_block(meeting_id, "transcript", "failed", error=str(exc))
            self.store.update(meeting_id, status="failed", error=str(exc))
            return

        # 以下区块彼此独立：某块失败只置该块 failed，用户可单独补跑（对标钉钉分任务状态）
        # 后续区块：各自有 try（内部只置本块 failed），但**仍需一层兜底**。
        #
        # 为什么：这些区块内部会调用 `store.update()` / `set_block()`，
        # 而存储写入在 Windows 上可能因文件被占用而抛 OSError（见 store 的说明）。
        # 那种异常会穿过 `_process`，被 `ThreadPoolExecutor` 吞进 Future——
        # 于是下面的 `status='completed'` 永远执行不到，
        # 记录**永久停在 processing**，用户看到永不结束的「正在处理」。
        #
        # 实测（`test_process_marks_failed_instead_of_stuck_processing`）：
        # 让关键词区块抛 PermissionError，原实现下 status 恒为 'processing'。
        try:
            if segments:
                self._run_summary_block(meeting_id, meta, segments, speaker_names)
                self._run_keywords_block(meeting_id, segments, speaker_names)
                self._run_mindmap_block(meeting_id, segments, speaker_names)
            self.store.update(meeting_id, status="completed")
        except Exception as exc:  # noqa: BLE001 - 兜底：绝不能让记录停在 processing
            logger.exception("会议处理未预期失败: id=%s", meeting_id)
            try:
                self.store.update(meeting_id, status="failed", error=f"处理中断：{exc}")
            except Exception:  # noqa: BLE001 - 连失败都写不进去时，至少留下日志
                logger.exception("记录失败状态同样写入失败: id=%s", meeting_id)
            return
        fresh = self.store.get(meeting_id)
        logger.info(
            "会议听记处理完成: id=%s segments=%d speakers=%d blocks=%s keywords=%d todos=%d",
            meeting_id,
            len(segments),
            len(speaker_names),
            fresh.get("blocks"),
            len(fresh.get("keywords") or []),
            len(fresh.get("todos") or []),
        )

    def _transcript_text(self, meta: dict) -> str:
        return render_transcript(meta.get("segments") or [], meta.get("speaker_names") or {})

    def _run_summary_block(self, meeting_id: str, meta: dict, segments: list[dict], speaker_names: dict) -> dict | None:
        """总结区块：产出纪要/要点/章节，并把 AI 待办落到待办实体。返回总结 dict 或 None。"""
        self.store.set_block(meeting_id, "summary", "processing")
        template = find_template(str(meta.get("template_id") or ""))
        summary = self.summarizer.summarize(render_transcript(segments, speaker_names), template)
        if not summary:
            self.store.set_block(
                meeting_id, "summary", "failed", error="总结生成失败（LLM 未配置或调用失败），可点重试"
            )
            return None
        todos = summary.pop("todos", None) or []
        summary.pop("mindmap", None)  # 导图走独立区块（harness 管线），不进 summary
        if todos:
            self.store.replace_ai_todos(meeting_id, todos)
        self.store.update(meeting_id, summary=summary)
        self.store.set_block(meeting_id, "summary", "ready")
        return summary

    def _run_keywords_block(self, meeting_id: str, segments: list[dict], speaker_names: dict) -> list[str] | None:
        self.store.set_block(meeting_id, "keywords", "processing")
        keywords = self.summarizer.extract_keywords(render_transcript(segments, speaker_names))
        if keywords is None:
            self.store.set_block(
                meeting_id, "keywords", "failed", error="关键词生成失败（LLM 未配置或调用失败），可点重试"
            )
            return None
        self.store.update(meeting_id, keywords=keywords)
        self.store.set_block(meeting_id, "keywords", "ready")
        return keywords

    def _run_mindmap_block(self, meeting_id: str, segments: list[dict], speaker_names: dict) -> str:
        """导图区块：harness 管线（议题切分→逐议题深挖→确定性装配），失败只置本块 failed。"""
        self.store.set_block(meeting_id, "mindmap", "processing")
        mindmap = ""
        try:
            from minutecraft.mindmap_harness import build_mindmap_harness

            outline, log_lines = build_mindmap_harness(
                self.summarizer.ask_json, segments, speaker_names,
                meeting_title=str((self.store.get(meeting_id)).get("title") or ""),
            )
            for line in log_lines:
                logger.info("导图 harness: id=%s %s", meeting_id, line)
            mindmap = outline or ""
        except Exception:  # noqa: BLE001 - harness 失败不阻断其他区块
            logger.warning("导图 harness 失败: id=%s", meeting_id, exc_info=True)
        if not mindmap.strip():
            # 回退单次生成（总结里已剥掉 mindmap，这里单独问一次大纲）
            summary = self.summarizer.summarize(render_transcript(segments, speaker_names))
            mindmap = (summary or {}).get("mindmap") or ""
        if not mindmap.strip():
            self.store.set_block(meeting_id, "mindmap", "failed", error="导图生成失败，可点重试")
            return ""
        self.store.update(meeting_id, mindmap=mindmap)
        self.store.set_block(meeting_id, "mindmap", "ready")
        return mindmap

    def regenerate_summary(self, meeting_id: str, template_id: str) -> dict:
        """按指定模板重新生成总结（不动转写/关键词/导图），返回更新后的完整记录。

        AI 待办随总结整体刷新；用户手工新增的待办保留。
        """
        meta = self.store.get(meeting_id)
        if meta.get("status") != "completed":
            raise ValueError("仅已完成的会议可重新生成总结")
        template = find_template(template_id)
        if template is None:
            raise ValueError(f"未知模板：{template_id}")
        self.store.set_block(meeting_id, "summary", "processing")
        transcript = self._transcript_text(meta)
        summary = self.summarizer.summarize(transcript, template)
        if not summary:
            self.store.set_block(meeting_id, "summary", "failed", error="总结生成失败（LLM 未配置或调用失败），可点重试")
            raise ValueError("总结生成失败（LLM 未配置或调用失败），请稍后重试")
        todos = summary.pop("todos", None) or []
        mindmap = summary.pop("mindmap", None) or ""
        if todos:
            self.store.replace_ai_todos(meeting_id, todos)
        self.store.update(
            meeting_id,
            summary=summary,
            mindmap=meta.get("mindmap") or mindmap or None,
            template_id=template["id"],
        )
        self.store.set_block(meeting_id, "summary", "ready")
        return self.store.get(meeting_id)

    def regenerate_keywords(self, meeting_id: str) -> dict:
        """单独补跑关键词区块。"""
        meta = self.store.get(meeting_id)
        if meta.get("status") != "completed":
            raise ValueError("仅已完成的会议可生成关键词")
        if not (meta.get("segments") or []):
            raise ValueError("该记录没有转写内容，无法生成关键词")
        self.store.set_block(meeting_id, "keywords", "processing")
        keywords = self.summarizer.extract_keywords(self._transcript_text(meta))
        if keywords is None:
            self.store.set_block(meeting_id, "keywords", "failed", error="关键词生成失败（LLM 未配置或调用失败），可点重试")
            raise ValueError("关键词生成失败（LLM 未配置或调用失败），请稍后重试")
        self.store.update(meeting_id, keywords=keywords)
        self.store.set_block(meeting_id, "keywords", "ready")
        return self.store.get(meeting_id)

    def regenerate_mindmap(self, meeting_id: str) -> str:
        """对已完成的会议重新生成思维导图大纲，返回 markdown（LLM 未配置/失败抛错）。

        优先走 harness 管线（议题切分→逐议题深挖→确定性装配），失败回退单次生成。
        """
        meta = self.store.get(meeting_id)
        if meta.get("status") != "completed":
            raise ValueError("仅已完成的会议可生成导图")
        segments = meta.get("segments") or []
        mindmap = self._run_mindmap_block(meeting_id, segments, meta.get("speaker_names") or {})
        if not mindmap.strip():
            raise ValueError("导图生成失败（LLM 未返回有效大纲），请稍后重试")
        return mindmap

    # --- 待办（一等实体 CRUD，对标钉钉 MinutesTodoI） ---

    def add_todo(self, meeting_id: str, task: str, owner_hint: str = "", due_hint: str = "") -> dict:
        task = (task or "").strip()
        if not task:
            raise ValueError("待办内容不能为空")
        return self.store.add_todo(meeting_id, task, owner_hint.strip(), due_hint.strip(), source="manual")

    def update_todo(self, meeting_id: str, todo_id: str, **fields) -> dict:
        if "task" in fields and fields["task"] is not None:
            fields["task"] = str(fields["task"]).strip()
            if not fields["task"]:
                raise ValueError("待办内容不能为空")
        try:
            return self.store.update_todo(meeting_id, todo_id, **fields)
        except KeyError as exc:
            raise ValueError("待办不存在") from exc

    def remove_todo(self, meeting_id: str, todo_id: str) -> list[dict]:
        try:
            return self.store.remove_todo(meeting_id, todo_id)
        except KeyError as exc:
            raise ValueError("待办不存在") from exc

    # --- 听记 AI 问答（对标钉钉听记问答：单条记录全文进上下文，无需检索） ---

    _ASK_SYSTEM_PROMPT = (
        "你是 EasyShare 会议听记助手。只依据给定的听记内容（纪要/要点/待办/转写全文）回答用户问题，"
        "引用发言时注明说话人名；听记内容不足以回答的部分如实说明，不要编造。"
        "回答用简体中文，简洁分点，直接给结论。"
    )

    def ask(self, meeting_id: str, question: str, history: list[dict] | None = None) -> dict:
        """对本条听记提问（纪要+转写全文直接进上下文），返回 {"answer": str}。"""
        meta = self.store.get(meeting_id)
        if meta.get("status") != "completed":
            raise ValueError("仅已完成的会议可提问")
        question = (question or "").strip()
        if not question:
            raise ValueError("问题不能为空")
        clean_history = [
            {"role": str(turn.get("role") or ""), "content": str(turn.get("content") or "")}
            for turn in (history or [])[-6:]
            if str(turn.get("role") or "") in ("user", "assistant") and str(turn.get("content") or "").strip()
        ]
        context = self._ask_context(meta)
        messages = [
            *({"role": turn["role"], "content": turn["content"]} for turn in clean_history),
            {"role": "user", "content": f"{context}\n\n【用户提问】{question}"},
        ]
        answer = self.summarizer.ask_text(self._ASK_SYSTEM_PROMPT, messages)
        if not answer:
            raise ValueError("问答失败（LLM 未配置或调用失败），请稍后重试")
        return {"answer": answer}

    def _ask_context(self, meta: dict) -> str:
        names = meta.get("speaker_names") or {}
        lines: list[str] = [f"【会议标题】{meta.get('title') or ''}", f"【会议时间】{meta.get('created_at') or ''}"]
        if meta.get("duration_seconds"):
            minutes, seconds = divmod(int(meta["duration_seconds"]), 60)
            lines.append(f"【时长】{minutes:02d}:{seconds:02d}")
        summary = meta.get("summary") or {}
        if summary.get("sections"):
            for section in summary["sections"]:
                lines += [f"【{section.get('title') or '纪要'}】", str(section.get("text") or "")]
        else:
            if summary.get("minutes"):
                lines += ["【纪要】", str(summary["minutes"])]
            if summary.get("key_points"):
                lines += ["【要点】"] + [f"- {point}" for point in summary["key_points"]]
        if meta.get("todos"):
            lines.append("【待办】")
            lines += [f"- {self._todo_line(todo)}" for todo in meta["todos"]]
        lines += ["【转写全文】", render_transcript(meta.get("segments") or [], names)]
        return "\n".join(lines)

    @staticmethod
    def _todo_line(todo: dict) -> str:
        """待办的单行文本口径（问答/归档/导出共用）：内容（负责人，期限）+ 完成标记。"""
        parts = [p for p in (str(todo.get("owner_hint") or ""), str(todo.get("due_hint") or "")) if p]
        text = str(todo.get("task") or "")
        if parts:
            text += f"（{'，'.join(parts)}）"
        if todo.get("done"):
            text += "〔已完成〕"
        return text

    # --- 归档入知识库（与知识问答共享检索面） ---

    def archive_markdown(self, meeting_id: str) -> tuple[str, str, str | None]:
        """生成归档 Markdown，返回 (filename, markdown, owner)。"""
        meta = self.store.get(meeting_id)
        if meta.get("status") != "completed":
            raise ValueError("仅已完成的会议可归档")
        names = meta.get("speaker_names") or {}
        lines: list[str] = [f"# {meta['title']}", ""]
        lines.append(f"- 会议时间：{meta['created_at']}")
        if meta.get("duration_seconds"):
            minutes, seconds = divmod(int(meta["duration_seconds"]), 60)
            lines.append(f"- 时长：{minutes:02d}:{seconds:02d}")
        if names:
            lines.append(f"- 发言人：{'、'.join(names[name] for name in sorted(names))}")
        lines.append("")
        summary = meta.get("summary")
        if summary:
            if summary.get("minutes"):
                lines += ["## 纪要", summary["minutes"], ""]
            if summary.get("key_points"):
                lines += ["## 要点"] + [f"- {point}" for point in summary["key_points"]] + [""]
        if meta.get("todos"):
            lines += ["## 待办"]
            lines += [
                ("- [x] " if todo.get("done") else "- [ ] ") + self._todo_line(todo)
                for todo in meta["todos"]
            ]
            lines.append("")
        if meta.get("mindmap"):
            lines += ["## 思维导图大纲", "```", meta["mindmap"], "```", ""]
        lines += ["## 转写全文", render_transcript(meta.get("segments") or [], names)]
        return f"{meta['title']}.md", "\n".join(lines), meta.get("owner")

    # --- 备忘录导出（Markdown 前端本地拼；docx 在这里用 python-docx 生成） ---

    def export_docx(self, meeting_id: str, *, with_transcript: bool = False) -> tuple[str, "object"]:
        """生成纪要 Word（钉钉版式：封面可视化报告 + 文档体 + AI洞察 + 待办 + 章节 + 汇报卡）。

        版式实现见 `docx_export.py`；`with_transcript` 为 True 时附一张转写全文附页。
        """
        from app.meetings.docx_export import build_minutes_docx

        meta = self.store.get(meeting_id)
        if meta.get("status") != "completed":
            raise ValueError("仅已完成的会议可导出")
        content = build_minutes_docx(meta, with_transcript=with_transcript)
        return f"{meta['title']} 纪要.docx", content

