"""会议听记编排：上传落盘 → 单 worker 串行转写 → LLM 总结 → 落盘。

单 worker 刻意串行：FunASR 常驻内存约 2GB，多任务并发只会互相挤内存，
排队是更可预期的行为。
"""
from __future__ import annotations

import logging
from concurrent.futures import ThreadPoolExecutor

from app.meetings.engine import MeetingTranscriber, audio_duration, render_transcript
from app.meetings.templates import find_template
from app.meetings.store import MeetingStore
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
    ) -> None:
        self.store = store
        self.transcriber = transcriber
        self.summarizer = summarizer
        self._executor = ThreadPoolExecutor(max_workers=1, thread_name_prefix="meeting")

    async def save_upload(self, audio, title: str, owner: str | None, max_bytes: int, template_id: str | None = None) -> dict:
        """把上传的音频流式落盘并登记 processing 记录，返回会议元数据。"""
        meta = self.store.create(title=title, owner=owner, template_id=template_id)
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
            target.unlink(missing_ok=True)
            self.store.delete(meta["id"])
            raise
        if size == 0:
            target.unlink(missing_ok=True)
            self.store.delete(meta["id"])
            raise ValueError("音频内容为空")
        return meta

    def submit(self, meeting_id: str) -> None:
        self._executor.submit(self._process, meeting_id)

    def shutdown(self) -> None:
        self._executor.shutdown(wait=False)

    # --- 内部：worker 线程执行 ---

    def _process(self, meeting_id: str) -> None:
        meta = self.store.get(meeting_id)
        wav = self.store.audio_path(meeting_id)
        try:
            segments = self.transcriber.transcribe(wav)
            speaker_names = {
                speaker: f"说话人 {int(speaker) + 1}" for speaker in sorted({s["speaker"] for s in segments})
            }
            mindmap = None
            summary = None
            if segments:
                template = find_template(str(meta.get("template_id") or ""))
                summary = self.summarizer.summarize(render_transcript(segments, speaker_names), template)
                if summary:
                    # 导图是顶层字段（可独立重生成），不进 summary
                    mindmap = summary.pop("mindmap", None) or None
                if segments:
                    # 导图走 harness 管线（议题切分→逐议题深挖→确定性装配），失败回退单次大纲
                    try:
                        from app.meetings.mindmap_harness import build_mindmap_harness

                        outline, log_lines = build_mindmap_harness(
                            self.summarizer.ask_json, segments, speaker_names,
                            meeting_title=str(meta.get("title") or ""),
                        )
                        for line in log_lines:
                            logger.info("导图 harness: id=%s %s", meeting_id, line)
                        mindmap = outline or mindmap
                    except Exception:  # noqa: BLE001 - harness 失败不阻断转写主流程
                        logger.warning("导图 harness 失败，回退单次大纲: id=%s", meeting_id, exc_info=True)
            self.store.update(
                meeting_id,
                status="completed",
                segments=segments,
                speaker_names=speaker_names,
                speaker_count=len(speaker_names),
                duration_seconds=audio_duration(wav),
                summary=summary,
                mindmap=mindmap,
            )
            logger.info(
                "会议转写完成: id=%s segments=%d speakers=%d summarized=%s mindmap=%s",
                meeting_id,
                len(segments),
                len(speaker_names),
                summary is not None,
                bool(mindmap),
            )
        except Exception as exc:  # noqa: BLE001 - 单任务失败以 failed 状态呈现
            logger.exception("会议转写失败: id=%s", meeting_id)
            self.store.update(meeting_id, status="failed", error=str(exc))

    def regenerate_summary(self, meeting_id: str, template_id: str) -> dict:
        """按指定模板重新生成总结（不动转写/导图），返回更新后的完整记录。"""
        meta = self.store.get(meeting_id)
        if meta.get("status") != "completed":
            raise ValueError("仅已完成的会议可重新生成总结")
        template = find_template(template_id)
        if template is None:
            raise ValueError(f"未知模板：{template_id}")
        names = meta.get("speaker_names") or {}
        transcript = render_transcript(meta.get("segments") or [], names)
        summary = self.summarizer.summarize(transcript, template)
        if not summary:
            raise ValueError("总结生成失败（LLM 未配置或调用失败），请稍后重试")
        mindmap = meta.get("mindmap")
        if not mindmap and summary.get("mindmap"):
            mindmap = summary["mindmap"]
        return self.store.update(meeting_id, summary=summary, mindmap=mindmap, template_id=template["id"])

    def regenerate_mindmap(self, meeting_id: str) -> str:
        """对已完成的会议重新生成思维导图大纲，返回 markdown（LLM 未配置/失败抛错）。

        优先走 harness 管线（议题切分→逐议题深挖→确定性装配），失败回退单次生成。
        """
        meta = self.store.get(meeting_id)
        if meta.get("status") != "completed":
            raise ValueError("仅已完成的会议可生成导图")
        names = meta.get("speaker_names") or {}
        segments = meta.get("segments") or []
        mindmap = ""
        if segments:
            try:
                from app.meetings.mindmap_harness import build_mindmap_harness

                mindmap, _ = build_mindmap_harness(
                    self.summarizer.ask_json, segments, names, meeting_title=str(meta.get("title") or "")
                )
            except Exception:  # noqa: BLE001 - harness 失败回退单次
                logger.warning("导图 harness 失败，回退单次生成: id=%s", meeting_id, exc_info=True)
                mindmap = ""
        if not mindmap.strip():
            transcript = render_transcript(segments, names)
            summary = self.summarizer.summarize(transcript)
            mindmap = (summary or {}).get("mindmap") or ""
        if not mindmap.strip():
            raise ValueError("导图生成失败（LLM 未返回有效大纲），请稍后重试")
        self.store.update(meeting_id, mindmap=mindmap)
        return mindmap

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
        if summary.get("todos"):
            lines.append("【待办】")
            lines += [
                "- " + todo.get("task", "")
                + (f"（{todo.get('owner_hint')}" + (f"，{todo.get('due_hint')}" if todo.get("due_hint") else "") + "）" if todo.get("owner_hint") or todo.get("due_hint") else "")
                for todo in summary["todos"]
            ]
        lines += ["【转写全文】", render_transcript(meta.get("segments") or [], names)]
        return "\n".join(lines)

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
            if summary.get("todos"):
                lines += ["## 待办"]
                lines += [
                    "- [ ] " + todo["task"]
                    + (f"（{todo['owner_hint']}" + (f"，{todo['due_hint']}" if todo.get("due_hint") else "") + "）" if todo.get("owner_hint") or todo.get("due_hint") else "")
                    for todo in summary["todos"]
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
            if summary.get("todos"):
                document.add_heading("待办", level=1)
                for todo in summary["todos"]:
                    suffix = ""
                    if todo.get("owner_hint") or todo.get("due_hint"):
                        parts = [p for p in (todo.get("owner_hint"), todo.get("due_hint")) if p]
                        suffix = f"（{'，'.join(parts)}）"
                    document.add_paragraph(todo["task"] + suffix, style="List Number")
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
