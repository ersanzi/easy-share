"""/meetings API：上传转写（异步）、列表、详情、音频回放、发言人改名、归档入库。

鉴权走 main.py 的 auth 中间件（/meetings 前缀在保护清单内）；
owner 可见性口径与 2b/2c 一致：owner 为空=共享，非空仅本人与 admin 可见。
"""
from __future__ import annotations

import logging
import threading
import time
import urllib.parse
from concurrent.futures import ThreadPoolExecutor
from concurrent.futures import TimeoutError as FuturesTimeout

from fastapi import APIRouter, Form, HTTPException, Request, UploadFile, status
from fastapi.responses import FileResponse, Response
from pydantic import BaseModel

from app.api.schemas import IngestRequest, IngestResponse
from app.kb.chunker import chunk_text
from app.services import AppServices

logger = logging.getLogger(__name__)
router = APIRouter()


class SpeakerRenameRequest(BaseModel):
    """发言人改名：speaker 为引擎输出的序号字符串（"0"/"1"/...）。"""

    speaker: str
    name: str


def _services(request: Request) -> AppServices:
    return request.app.state.services


def _meetings(request: Request):
    service = _services(request).meetings
    if service is None:
        raise HTTPException(
            status.HTTP_503_SERVICE_UNAVAILABLE,
            "会议听记未启用（需 ASR_ENABLED=true 并安装 requirements-asr.txt）",
        )
    return service


def _current_user(request: Request) -> dict | None:
    return getattr(request.state, "user", None)


def _is_admin(user: dict | None) -> bool:
    return bool(user and user.get("role") == "admin")


def _ensure_visible(meta: dict, user: dict | None) -> None:
    """owner 为空=共享；非空仅本人与 admin 可见（auth 未启用时 user 为 None，全部可见）。"""
    owner = meta.get("owner")
    if owner and user and user.get("username") != owner and not _is_admin(user):
        raise HTTPException(status.HTTP_404_NOT_FOUND, "会议不存在")


def _ensure_manageable(meta: dict, user: dict | None) -> None:
    """删除/改名/归档要求本人或 admin；共享会议（owner 空）仅 admin 可管理。"""
    owner = meta.get("owner")
    if not user:
        return  # auth 关闭的本机/实验场景
    if _is_admin(user):
        return
    if owner and owner == user.get("username"):
        return
    raise HTTPException(status.HTTP_404_NOT_FOUND, "会议不存在")


@router.get("/meetings/templates")
def list_templates() -> dict:
    """内置总结模板列表（无需鉴权数据，公开只读）。"""
    from app.meetings.templates import BUILTIN_TEMPLATES

    return {"templates": BUILTIN_TEMPLATES}


@router.post("/meetings/transcribe", status_code=status.HTTP_202_ACCEPTED)
async def transcribe_meeting(
    request: Request,
    audio: UploadFile,
    title: str = Form(""),
    template_id: str = Form(""),
    meeting_id: str = Form(""),
) -> dict:
    """上传会议录音，转写+总结异步执行；立即返回 processing 记录，前端轮询详情。

    meeting_id 非空时复用该记录（实时录制会话先建的那条），不再新建。
    """
    service = _meetings(request)
    user = _current_user(request)
    owner = (user or {}).get("username")
    meta = await service.save_upload(
        audio,
        title=title.strip(),
        owner=owner,
        max_bytes=_services(request).config.meeting_max_upload_bytes,
        template_id=template_id.strip() or None,
        meeting_id=meeting_id.strip() or None,
    )
    service.submit(meta["id"])
    logger.info("会议听记已受理: id=%s owner=%s title=%s", meta["id"], owner, meta["title"])
    return meta


# --- 实时上屏（边说边出字；预览性质，终态仍由上面的整段离线重跑给出） ---

# 单次推流片段的硬上限（600ms@16k s16 ≈ 19KB，留足冗余防大包打爆内存）
_STREAM_MAX_BYTES = 1024 * 1024


@router.post("/meetings/stream/warm", status_code=status.HTTP_202_ACCEPTED)
def warm_stream(request: Request) -> dict:
    """后台预热流式模型：听记页一开就调，避免第一个分片卡在几十秒的权重冷加载。"""
    service = _meetings(request)
    if service.streamer is None:
        raise HTTPException(status.HTTP_503_SERVICE_UNAVAILABLE, "实时转写不可用")
    threading.Thread(target=service.streamer.prewarm, daemon=True).start()
    return {"warming": True}


@router.post("/meetings/stream/session", status_code=status.HTTP_201_CREATED)
def open_stream_session(
    request: Request,
    title: str = Form(""),
    template_id: str = Form(""),
) -> dict:
    """开一场录制会话，先落一条 recording 记录，返回的 id 即推流会话 id。"""
    service = _meetings(request)
    user = _current_user(request)
    meta = service.open_stream(
        title=title.strip(),
        owner=(user or {}).get("username"),
        template_id=template_id.strip() or None,
    )
    return meta


def _stream_meeting_or_404(service, meeting_id: str, request: Request) -> dict:
    try:
        meta = service.store.get(meeting_id)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "会议不存在") from exc
    _ensure_visible(meta, _current_user(request))
    return meta


@router.post("/meetings/{meeting_id}/stream")
async def feed_stream(meeting_id: str, request: Request) -> dict:
    """推一段 PCM16/16kHz/单声道，回当前已上屏行与进行中的一句。

    owner 从记录里读：热词按录制者自己的词表增强（与离线转写同口径）。
    """
    service = _meetings(request)
    meta = _stream_meeting_or_404(service, meeting_id, request)
    body = await request.body()
    if len(body) > _STREAM_MAX_BYTES:
        raise HTTPException(status.HTTP_413_REQUEST_ENTITY_TOO_LARGE, "单次推流片段过大")
    return service.feed_stream(meeting_id, body, owner=meta.get("owner"))


@router.post("/meetings/{meeting_id}/stream/stop")
def stop_stream(meeting_id: str, request: Request) -> dict:
    """结束推流会话（冲掉尾包），返回完整实时字幕行。"""
    service = _meetings(request)
    _stream_meeting_or_404(service, meeting_id, request)
    return service.close_stream(meeting_id)


@router.get("/meetings/{meeting_id}/stream/live")
def stream_live(meeting_id: str, request: Request) -> dict:
    """不推音频也能取当前上屏状态（前端断线重连/切走再回来时用）。"""
    service = _meetings(request)
    meta = _stream_meeting_or_404(service, meeting_id, request)
    if service.streamer is None:
        return {"lines": [], "current": "", "elapsed_seconds": 0, "last_line_at": 0.0}
    return service.live_snapshot(meeting_id, owner=meta.get("owner"))


@router.post("/meetings/{meeting_id}/live-points")
def live_points(meeting_id: str, request: Request) -> dict:
    """会中要点（对标钉钉实时摘要树）：LLM 基于已上屏内容归纳，随录随长。"""
    service = _meetings(request)
    _stream_meeting_or_404(service, meeting_id, request)
    return {"points": service.live_points(meeting_id)}


def _live_check_timeout_seconds() -> float:
    """会中快检的硬上限（秒）。单列成函数，便于用例把等待时间压到毫秒级。"""
    return 25.0


@router.post("/meetings/{meeting_id}/live-check")
def live_check(meeting_id: str, request: Request) -> dict:
    """会中快检：只看刚上屏的最近几行，核对数字/期限/人名等事实口径（秒级 LLM 调用）。

    **必须在工作线程里跑**：``def``（非 ``async def``）路由由 FastAPI 丢进线程池，
    推理线程不阻塞事件循环；若写成 ``async def`` 又去调同步 LLM，整场会议的推流分片
    会一起排队等人家的 token。

    **上游不可用时要在 Core 的超时之前就返回**：实测上游网关 503 时 summarizer 会按
    0.5/1/2/4/8/16s 指数退避重试，一次 live-check 能拖到 120s+，正好撞上 Core 的
    ``knowledgeQueryTimeout``，前端拿到的是「服务器关闭了本应保持活动状态的连接」，
    而不是设计好的 ``verdict=unavailable``。所以这里再兜一层硬上限，
    超时即返回「AI 暂时不可用」——快检本来就该是秒级的。
    """
    service = _meetings(request)
    _stream_meeting_or_404(service, meeting_id, request)
    timeout_seconds = _live_check_timeout_seconds()
    pool = ThreadPoolExecutor(max_workers=1, thread_name_prefix="live-check")
    try:
        future = pool.submit(service.live_check, meeting_id)
        return future.result(timeout=timeout_seconds)
    except FuturesTimeout:
        logger.warning("会中快检超时（%ds），按不可用返回: meeting=%s", timeout_seconds, meeting_id)
        return {
            "verdict": "unavailable",
            "note": "AI 快检响应超时（上游模型不可用），稍后再试",
            "points": [],
            "last_line_at": 0.0,
            "checked_at": time.strftime("%H:%M:%S"),
        }
    except Exception:  # noqa: BLE001 - 快检失败绝不能影响录音会话
        logger.warning("会中快检失败: meeting=%s", meeting_id, exc_info=True)
        return {
            "verdict": "unavailable",
            "note": "AI 快检失败，稍后再试",
            "points": [],
            "last_line_at": 0.0,
            "checked_at": time.strftime("%H:%M:%S"),
        }
    finally:
        # 超时后不 join：让卡住的那次调用在后台自己结束，不要连累这次响应
        pool.shutdown(wait=False)


# --- 个性化热词（必须先于 /meetings/{meeting_id} 注册，否则 "hotwords" 会被当成会议 id） ---

class HotwordsBody(BaseModel):
    """批量新增热词；单条也以长度为 1 的数组提交。"""

    words: list[str] = []


@router.get("/meetings/hotwords")
def get_hotwords(request: Request) -> dict:
    from app.meetings.hotwords import MAX_WORDS_PER_USER

    store = _services(request).hotwords
    if store is None:
        return {"words": [], "max": MAX_WORDS_PER_USER}
    user = _current_user(request)
    return {"words": store.list((user or {}).get("username")), "max": MAX_WORDS_PER_USER}


@router.post("/meetings/hotwords")
def add_hotwords(body: HotwordsBody, request: Request) -> dict:
    from app.meetings.hotwords import HotwordError, MAX_WORDS_PER_USER

    store = _services(request).hotwords
    if store is None:
        raise HTTPException(status.HTTP_503_SERVICE_UNAVAILABLE, "热词存储不可用")
    user = _current_user(request)
    try:
        words = store.add((user or {}).get("username"), body.words)
    except HotwordError as exc:
        raise HTTPException(status.HTTP_422_UNPROCESSABLE_ENTITY, str(exc)) from exc
    return {"words": words, "max": MAX_WORDS_PER_USER}


@router.delete("/meetings/hotwords/{word}")
def delete_hotword(word: str, request: Request) -> dict:
    from app.meetings.hotwords import MAX_WORDS_PER_USER

    store = _services(request).hotwords
    if store is None:
        raise HTTPException(status.HTTP_503_SERVICE_UNAVAILABLE, "热词存储不可用")
    user = _current_user(request)
    return {
        "words": store.remove((user or {}).get("username"), word),
        "max": MAX_WORDS_PER_USER,
    }


@router.post("/meetings/{meeting_id}/retranscribe", status_code=status.HTTP_202_ACCEPTED)
def retranscribe_meeting(meeting_id: str, request: Request) -> dict:
    """重新转写整场（加了热词后让老记录重跑全管线），异步受理。"""
    service = _meetings(request)
    try:
        meta = service.store.get(meeting_id)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "会议不存在") from exc
    _ensure_manageable(meta, _current_user(request))
    try:
        return service.retranscribe(meeting_id)
    except ValueError as exc:
        raise HTTPException(status.HTTP_409_CONFLICT, str(exc)) from exc


# --- 声纹（生物特征个人信息：必须可查、可删） ---

@router.get("/meetings/voiceprints")
def list_voiceprints(request: Request) -> dict:
    """列出当前用户已登记的声纹（只回名字与样本时长，不外泄向量）。"""
    store = _services(request).voiceprints
    if store is None:
        return {"voiceprints": []}
    user = _current_user(request)
    return {"voiceprints": store.list((user or {}).get("username"))}


@router.delete("/meetings/voiceprints/{name}")
def delete_voiceprint(name: str, request: Request) -> dict:
    """删除一条声纹（彻底不保留向量）。"""
    store = _services(request).voiceprints
    if store is None:
        raise HTTPException(status.HTTP_503_SERVICE_UNAVAILABLE, "声纹存储不可用")
    user = _current_user(request)
    return {"voiceprints": store.remove((user or {}).get("username"), name)}


@router.get("/meetings")
def list_meetings(request: Request, limit: int = 50) -> dict:
    service = _meetings(request)
    user = _current_user(request)
    items = [
        meta
        for meta in service.store.list(limit=limit)
        if _visible(meta, user)
    ]
    return {"meetings": items}


def _visible(meta: dict, user: dict | None) -> bool:
    owner = meta.get("owner")
    if not owner or not user:
        return True
    return owner == user.get("username") or _is_admin(user)


@router.get("/meetings/{meeting_id}")
def get_meeting(meeting_id: str, request: Request) -> dict:
    service = _meetings(request)
    try:
        meta = service.store.get(meeting_id)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "会议不存在") from exc
    _ensure_visible(meta, _current_user(request))
    return meta


@router.delete("/meetings/{meeting_id}")
def delete_meeting(meeting_id: str, request: Request) -> dict:
    service = _meetings(request)
    try:
        meta = service.store.get(meeting_id)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "会议不存在") from exc
    _ensure_manageable(meta, _current_user(request))
    service.store.delete(meeting_id)
    return {"deleted": True}


@router.patch("/meetings/{meeting_id}/speakers")
def rename_speaker(meeting_id: str, req: SpeakerRenameRequest, request: Request) -> dict:
    """发言人改名（单次一个，前端逐个提交）。"""
    service = _meetings(request)
    try:
        meta = service.store.get(meeting_id)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "会议不存在") from exc
    _ensure_manageable(meta, _current_user(request))
    name = req.name.strip()
    if not name:
        raise HTTPException(status.HTTP_422_UNPROCESSABLE_ENTITY, "发言人名称不能为空")
    return service.rename_speaker(meeting_id, req.speaker.strip(), name)


class SpeakerMergeRequest(BaseModel):
    """合并发言人：把 from_speaker 的轮次全并进 into_speaker。"""

    from_speaker: str
    into_speaker: str


@router.post("/meetings/{meeting_id}/speakers/merge")
def merge_speakers(meeting_id: str, req: SpeakerMergeRequest, request: Request) -> dict:
    """合并两位发言人（对标钉钉「合并发言人」：同一人被拆成两簇时一键并回）。"""
    service = _meetings(request)
    try:
        meta = service.store.get(meeting_id)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "会议不存在") from exc
    _ensure_manageable(meta, _current_user(request))
    try:
        return service.merge_speakers(meeting_id, req.from_speaker, req.into_speaker)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "发言人不存在") from exc
    except ValueError as exc:
        raise HTTPException(status.HTTP_422_UNPROCESSABLE_ENTITY, str(exc)) from exc


class SegmentEditRequest(BaseModel):
    """改一句转写：text 为该句新内容。"""

    text: str


@router.patch("/meetings/{meeting_id}/segments/{index}")
def update_segment(meeting_id: str, index: int, req: SegmentEditRequest, request: Request) -> dict:
    """编辑转写正文的某一句（对标钉钉逐字稿可编辑），返回更新后的完整记录。"""
    service = _meetings(request)
    try:
        meta = service.store.get(meeting_id)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "会议不存在") from exc
    _ensure_manageable(meta, _current_user(request))
    try:
        return service.update_segment(meeting_id, index, req.text)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "该句不存在") from exc
    except ValueError as exc:
        raise HTTPException(status.HTTP_422_UNPROCESSABLE_ENTITY, str(exc)) from exc


@router.post("/meetings/{meeting_id}/keywords")
def regenerate_keywords(meeting_id: str, request: Request) -> dict:
    """单独补跑关键词区块（LLM 调用，慢链路），返回更新后的完整记录。"""
    service = _meetings(request)
    try:
        meta = service.store.get(meeting_id)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "会议不存在") from exc
    _ensure_manageable(meta, _current_user(request))
    try:
        return service.regenerate_keywords(meeting_id)
    except ValueError as exc:
        raise HTTPException(status.HTTP_409_CONFLICT, str(exc)) from exc


# --- 待办（一等实体：对标钉钉 MinutesTodoI 的增删改与完成态） ---

class TodoBody(BaseModel):
    """新增/编辑待办；字段留空表示不改动（新增时 task 必填）。"""

    task: str | None = None
    owner_hint: str | None = None
    due_hint: str | None = None
    done: bool | None = None


def _meeting_or_404(service, meeting_id: str, user: dict | None) -> dict:
    try:
        meta = service.store.get(meeting_id)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "会议不存在") from exc
    _ensure_manageable(meta, user)
    return meta


@router.post("/meetings/{meeting_id}/todos")
def add_todo(meeting_id: str, body: TodoBody, request: Request) -> dict:
    """手工新增待办，返回更新后的完整记录。"""
    service = _meetings(request)
    _meeting_or_404(service, meeting_id, _current_user(request))
    try:
        service.add_todo(
            meeting_id,
            task=body.task or "",
            owner_hint=body.owner_hint or "",
            due_hint=body.due_hint or "",
        )
    except ValueError as exc:
        raise HTTPException(status.HTTP_422_UNPROCESSABLE_ENTITY, str(exc)) from exc
    return service.store.get(meeting_id)


@router.patch("/meetings/{meeting_id}/todos/{todo_id}")
def update_todo(meeting_id: str, todo_id: str, body: TodoBody, request: Request) -> dict:
    """编辑待办内容/负责人/期限，或勾选完成；返回更新后的完整记录。"""
    service = _meetings(request)
    _meeting_or_404(service, meeting_id, _current_user(request))
    fields = {key: value for key, value in body.model_dump().items() if value is not None}
    if not fields:
        raise HTTPException(status.HTTP_422_UNPROCESSABLE_ENTITY, "没有要更新的字段")
    try:
        service.update_todo(meeting_id, todo_id, **fields)
    except ValueError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, str(exc)) from exc
    return service.store.get(meeting_id)


@router.delete("/meetings/{meeting_id}/todos/{todo_id}")
def delete_todo(meeting_id: str, todo_id: str, request: Request) -> dict:
    """删除一条待办；返回更新后的完整记录。"""
    service = _meetings(request)
    _meeting_or_404(service, meeting_id, _current_user(request))
    try:
        service.remove_todo(meeting_id, todo_id)
    except ValueError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, str(exc)) from exc
    return service.store.get(meeting_id)


@router.post("/meetings/{meeting_id}/mindmap")
def regenerate_mindmap(meeting_id: str, request: Request) -> dict:
    """重新生成思维导图大纲（LLM 调用，数秒到数十秒），返回更新后的完整记录。"""
    service = _meetings(request)
    try:
        meta = service.store.get(meeting_id)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "会议不存在") from exc
    _ensure_manageable(meta, _current_user(request))
    try:
        service.regenerate_mindmap(meeting_id)
    except ValueError as exc:
        raise HTTPException(status.HTTP_409_CONFLICT, str(exc)) from exc
    return service.store.get(meeting_id)


def _attachment_response(filename: str, content: bytes | str, media_type: str) -> Response:
    """附件下载响应；中文文件名走 RFC 5987 filename*（ASCII 回退名保底）。"""
    quoted = urllib.parse.quote(filename)
    return Response(
        content=content,
        media_type=media_type,
        headers={
            "Content-Disposition": f"attachment; filename=\"meeting-export\"; filename*=UTF-8''{quoted}"
        },
    )


class SummaryRegenBody(BaseModel):
    """可选：指定模板重生成（同时改写记录的 template_id）；缺省用记录已有模板。"""

    template_id: str = ""


@router.post("/meetings/{meeting_id}/summary")
def regenerate_summary(meeting_id: str, body: SummaryRegenBody | None = None, request: Request = None) -> dict:
    """按模板重新生成总结（LLM 调用，慢链路）。缺省用记录已有模板。"""
    service = _meetings(request)
    try:
        meta = service.store.get(meeting_id)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "会议不存在") from exc
    _ensure_manageable(meta, _current_user(request))
    template_id = (body.template_id if body else "") or str(meta.get("template_id") or "meeting-minutes")
    try:
        return service.regenerate_summary(meeting_id, template_id)
    except ValueError as exc:
        raise HTTPException(status.HTTP_409_CONFLICT, str(exc)) from exc


class MeetingAskTurn(BaseModel):
    """问答历史单轮（只收 user/assistant，超出部分由 service 截断）。"""

    role: str
    content: str


class MeetingAskBody(BaseModel):
    """听记问答：当前问题 + 可选最近几轮历史。"""

    question: str
    # None 容错：Go 侧 nil 切片会序列化成显式 null，不能只靠缺省值 []
    history: list[MeetingAskTurn] | None = None


@router.post("/meetings/{meeting_id}/ask")
def ask_meeting(meeting_id: str, body: MeetingAskBody, request: Request) -> dict:
    """对本条听记提问（纪要+转写全文进上下文，LLM 慢链路），返回 {"answer": str}。"""
    service = _meetings(request)
    try:
        meta = service.store.get(meeting_id)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "会议不存在") from exc
    _ensure_visible(meta, _current_user(request))
    try:
        return service.ask(
            meeting_id,
            body.question,
            [turn.model_dump() for turn in body.history] if body.history else [],
        )
    except ValueError as exc:
        raise HTTPException(status.HTTP_409_CONFLICT, str(exc)) from exc


@router.get("/meetings/{meeting_id}/export/markdown")
def export_meeting_markdown(meeting_id: str, request: Request) -> Response:
    """备忘录 Markdown 导出（纪要/要点/待办/转写全文）。"""
    service = _meetings(request)
    try:
        meta = service.store.get(meeting_id)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "会议不存在") from exc
    _ensure_visible(meta, _current_user(request))
    _, markdown, _ = service.archive_markdown(meeting_id)
    return _attachment_response(f"{meta['title']} 备忘录.md", markdown, "text/markdown; charset=utf-8")


@router.get("/meetings/{meeting_id}/export/docx")
def export_meeting_docx(meeting_id: str, request: Request, with_transcript: bool = False) -> Response:
    """纪要 Word 导出（钉钉版式，python-docx 生成）；with_transcript=true 附转写全文附页。"""
    service = _meetings(request)
    try:
        meta = service.store.get(meeting_id)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "会议不存在") from exc
    _ensure_visible(meta, _current_user(request))
    try:
        filename, content = service.export_docx(meeting_id, with_transcript=with_transcript)
    except ValueError as exc:
        raise HTTPException(status.HTTP_409_CONFLICT, str(exc)) from exc
    return _attachment_response(
        filename, content, "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
    )


@router.get("/meetings/{meeting_id}/audio")
def meeting_audio(meeting_id: str, request: Request) -> FileResponse:
    service = _meetings(request)
    try:
        meta = service.store.get(meeting_id)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "会议不存在") from exc
    _ensure_visible(meta, _current_user(request))
    path = service.store.audio_path(meeting_id)
    if not path.is_file():
        raise HTTPException(status.HTTP_404_NOT_FOUND, "音频文件不存在")
    return FileResponse(path, media_type="audio/wav", filename=f"{meta['title']}.wav")


@router.post("/meetings/{meeting_id}/archive", response_model=IngestResponse)
def archive_meeting(meeting_id: str, request: Request) -> IngestResponse:
    """把会议纪要+转写归档进知识库，之后可被知识问答检索引用。"""
    service = _meetings(request)
    try:
        meta = service.store.get(meeting_id)
    except KeyError as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "会议不存在") from exc
    _ensure_manageable(meta, _current_user(request))

    services = _services(request)
    filename, markdown, owner = service.archive_markdown(meeting_id)
    chunks = chunk_text(markdown, services.config.chunk_size, services.config.chunk_overlap)
    if not chunks:
        raise HTTPException(status.HTTP_422_UNPROCESSABLE_ENTITY, "归档内容为空")
    doc_id = f"meeting-{meeting_id}"
    embeddings = services.embedder.embed(chunks)
    items = [
        {
            "id": f"{doc_id}:{index}",
            "doc_id": doc_id,
            "text": chunk,
            "metadata": {"filename": filename, "owner": owner},
            "embedding": embedding,
        }
        for index, (chunk, embedding) in enumerate(zip(chunks, embeddings))
    ]
    services.vector_store.replace_doc(doc_id, items)
    logger.info("会议归档入库: id=%s doc_id=%s chunks=%d", meeting_id, doc_id, len(chunks))
    return IngestResponse(doc_id=doc_id, filename=filename, chunks=len(chunks), chars=len(markdown))
