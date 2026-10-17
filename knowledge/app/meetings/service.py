"""会议听记编排：上传落盘 → 单 worker 串行转写 → LLM 总结 → 落盘。

单 worker 刻意串行：FunASR 常驻内存约 2GB，多任务并发只会互相挤内存，
排队是更可预期的行为。
"""
from __future__ import annotations

import logging
from concurrent.futures import ThreadPoolExecutor

from app.meetings.engine import MeetingTranscriber, audio_duration, render_transcript
from app.meetings.templates import find_template
from app.meetings.store import MeetingStore, _blank_blocks
from app.meetings.summarizer import MeetingSummarizer

logger = logging.getLogger(__name__)

# 落盘拷贝的分块大小（FastAPI UploadFile 的 SpooledTemporaryFile → 目标 wav）
_COPY_CHUNK = 1024 * 1024


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
        self.streamer.start(meta["id"])
        return meta

    def feed_stream(self, meeting_id: str, pcm: bytes) -> dict:
        if self.streamer is None:
            raise ValueError("实时转写未启用（流式模型不可用）")
        return self.streamer.feed(meeting_id, pcm)

    def close_stream(self, meeting_id: str) -> dict:
        if self.streamer is None:
            raise ValueError("实时转写未启用（流式模型不可用）")
        return self.streamer.stop(meeting_id)

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
        """说话人展示名：默认「说话人 N」，重新转写时保住用户已改过的名字。"""
        previous = meta.get("speaker_names") or {}
        names: dict[str, str] = {}
        for speaker in sorted({str(segment["speaker"]) for segment in segments}):
            old = str(previous.get(speaker) or "")
            names[speaker] = old if old and not old.startswith("说话人 ") else f"说话人 {int(speaker) + 1}"
        return names

    def apply_voiceprints(self, owner: str | None, wav, segments: list[dict], names: dict[str, str]) -> int:
        """用已登记声纹自动认领发言人（轻口径：只填仍是默认名的，不覆盖人工改名）。"""
        if self.voiceprints is None or not segments:
            return 0
        try:
            matched = self.voiceprints.recognize(owner, wav, segments)
        except Exception:  # noqa: BLE001 - 声纹是加分项，失败不影响转写产物
            logger.warning("声纹识别失败，跳过自动认领: owner=%s", owner, exc_info=True)
            return 0
        applied = 0
        for speaker, name in matched.items():
            if not names.get(speaker) or names[speaker].startswith("说话人 "):
                names[speaker] = name
                applied += 1
        if applied:
            logger.info("声纹自动认领 %d 位发言人: owner=%s", applied, owner)
        return applied

    def rename_speaker(self, meeting_id: str, speaker: str, name: str) -> dict:
        """改名，并把这次标注沉淀成声纹（样本时长不足则只改名，不报错）。"""
        meta = self.store.rename_speaker(meeting_id, speaker, name)
        if self.voiceprints is not None:
            try:
                self.voiceprints.enroll(
                    meta.get("owner"), name, self.store.audio_path(meeting_id), meta.get("segments") or [], speaker
                )
            except Exception:  # noqa: BLE001 - 声纹沉淀失败不影响改名本身
                logger.warning("声纹登记失败: meeting=%s speaker=%s", meeting_id, speaker, exc_info=True)
        return meta

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
            from app.meetings.mindmap_harness import build_mindmap_harness

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

    def export_docx(self, meeting_id: str) -> tuple[str, "object"]:
        """生成备忘录 Word 文档，返回 (文件名, bytes)。依赖 python-docx（requirements 已含）。"""
        import io

        from docx import Document
        from docx.shared import Pt

        meta = self.store.get(meeting_id)
        if meta.get("status") != "completed":
            raise ValueError("仅已完成的会议可导出")
        names = meta.get("speaker_names") or {}
        document = Document()
        document.add_heading(meta["title"], level=0)

        info = document.add_paragraph()
        meta_line = f"会议时间：{meta['created_at']}"
        if meta.get("duration_seconds"):
            minutes, seconds = divmod(int(meta["duration_seconds"]), 60)
            meta_line += f"　时长 {minutes:02d}:{seconds:02d}"
        if meta.get("speaker_count"):
            meta_line += f"　发言人 {meta['speaker_count']} 位"
        info_run = info.add_run(meta_line)
        info_run.font.size = Pt(9)

        summary = meta.get("summary")
        if summary:
            if summary.get("minutes"):
                document.add_heading("纪要", level=1)
                document.add_paragraph(summary["minutes"])
            if summary.get("key_points"):
                document.add_heading("要点", level=1)
                for point in summary["key_points"]:
                    document.add_paragraph(point, style="List Bullet")
        if meta.get("todos"):
            document.add_heading("待办", level=1)
            for todo in meta["todos"]:
                mark = "✓ " if todo.get("done") else ""
                document.add_paragraph(mark + self._todo_line(todo), style="List Number")
        if meta.get("mindmap"):
            document.add_heading("思维导图大纲", level=1)
            for line in meta["mindmap"].splitlines():
                if not line.strip():
                    continue
                indent = line[: len(line) - len(line.lstrip())]
                depth = line.count("#", 0, len(line) - len(line.lstrip("#").lstrip())) if line.lstrip().startswith("#") else 0
                text = line.strip()
                if text.startswith("#"):
                    text = text.lstrip("#").strip()
                    paragraph = document.add_paragraph()
                    run = paragraph.add_run(text)
                    run.bold = True
                    paragraph.paragraph_format.left_indent = Pt(6 + 12 * min(depth, 3))
                elif text.startswith("- "):
                    paragraph = document.add_paragraph()
                    paragraph.paragraph_format.left_indent = Pt(12 + 12 * (len(indent) // 2))
                    paragraph.add_run("• " + text[2:])
                else:
                    document.add_paragraph(text)

        document.add_heading("转写全文", level=1)
        for segment in meta.get("segments") or []:
            who = names.get(segment["speaker"], f"说话人 {int(segment['speaker']) + 1}")
            minutes, seconds = divmod(int(segment["start"]), 60)
            paragraph = document.add_paragraph()
            run = paragraph.add_run(f"{who} [{minutes:02d}:{seconds:02d}] ")
            run.bold = True
            paragraph.add_run(segment["text"])

        buffer = io.BytesIO()
        document.save(buffer)
        return f"{meta['title']} 备忘录.docx", buffer.getvalue()
