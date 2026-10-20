"""长网页版式报告：数据 → report.html → 无头浏览器 → PNG / PDF。

**为什么是「网页 + 无头浏览器」而不是前端截 canvas**：这条链路只需要一份 HTML 与一个浏览器，
不依赖 Node/npm、不依赖 EasyShare 桌面端，命令行能跑、Agent 也能跑。前端那份（MeetingVisualReport.vue）
是产品内嵌预览，两者是同一版式的两个实现（见 skills/minutes-from-transcript/render/README.md）。

模板 `report.html` 与引擎是解耦的：它只认 `window.__REPORT__`（下面 `build_report` 组装的那份数据），
换版式只改 HTML，不动引擎。

**Windows 上的两个实测坑（都在这里处理，别再掉进去）**：
1. Chrome 输出的诊断信息按系统代码页（中文 Windows 是 GBK）编码，`text=True` 用 UTF-8/locale 解码会
   抛 `UnicodeDecodeError`，把真正的失败原因掩盖成一条无关的线程栈。→ 一律按字节读、顺手丢给父进程显示。
2. `--screenshot` 必须写成 `--screenshot=<路径>`；写成两个独立参数时 Chrome 会把路径当第二个 URL，
   报 "Multiple targets are not supported"（退出码 13）且不产出文件。
"""
from __future__ import annotations

import base64
import json
import re
import shutil
import subprocess
import tempfile
import time
from pathlib import Path
from urllib.parse import quote

TEMPLATE_NAME = "report.html"
DEFAULT_WIDTH = 1080

# 本机常见的浏览器位置（Windows / macOS / Linux）
BROWSER_CANDIDATES = [
    r"C:\Program Files\Google\Chrome\Application\chrome.exe",
    r"C:\Program Files (x86)\Google\Chrome\Application\chrome.exe",
    r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe",
    r"C:\Program Files\Microsoft\Edge\Application\msedge.exe",
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
    "/usr/bin/google-chrome",
    "/usr/bin/chromium",
    "/usr/bin/chromium-browser",
]

# 单次截图的最大高度：超过后 Chrome 会截断或撞 GPU 纹理上限，故分段再纵向拼接
MAX_SHOT_HEIGHT = 12000

REPORT_TEMPLATE_DIR = Path(__file__).resolve().parent / "report"


class ReportRenderError(RuntimeError):
    """渲染失败：模板缺失 / 找不到浏览器 / 浏览器没产出文件。消息要能直接照着做。"""


# ── 数据装配：把纪要记录翻成 report.html 认的形状 ──


def _as_list(value) -> list:
    return value if isinstance(value, list) else []


def _topic_title(chapter: dict) -> str:
    return str(chapter.get("title") or "").strip() or "未命名议题"


def build_report(record: dict) -> dict:
    """纪要记录 → 长网页报告数据。

    口径（与钉钉听记的可视化报告一致）：
    - **议题（topics）是正文骨架**，来源按优先级取：
        ① `summary.topics` —— 引擎新的「报告议题」独立调用产出（议题→分节→要点，最完整）
        ② 记录顶层 `topics` —— 手工补的结构样例，或 `minutecraft render report.json` 的现成数据
        ③ `summary.chapters` —— 旧产物：只有时间轴与一句话，分节从 `visual.sections` 按顺序均分
        ④ 都没有 —— 退化成单议题（标题取会议标题），**不编造时间段**
    - `focus`/`insights` 有则用，没有就不画；没给的字段不进报告，模板据此不画空框。

    已装配好的数据（带 `topics` 顶层键）会被原样透传，所以 `report.json` 可以再喂回来。
    """
    record = record or {}
    summary = record.get("summary") if isinstance(record.get("summary"), dict) else {}
    visual = summary.get("visual") if isinstance(summary.get("visual"), dict) else {}
    title = str(record.get("title") or summary.get("title") or "会议纪要").strip()

    sections = [s for s in _as_list(visual.get("sections")) if isinstance(s, dict) and s.get("title")]

    # 三级优先级：「结构最完整」到「最原始」。见 docstring。
    #   ① summary.topics（引擎新产出）  ② 顶层 topics（样板/现成 report.json）
    #   ③ summary.chapters 均分 visual.sections  ④ 单议题退化
    given_topics = [t for t in _as_list(summary.get("topics")) if isinstance(t, dict) and t.get("title")]
    if not given_topics:
        given_topics = [t for t in _as_list(record.get("topics")) if isinstance(t, dict) and t.get("title")]
    if given_topics:
        topics = [_given_topic(topic) for topic in given_topics]
        return _report_head(record, summary, visual, title, topics)

    chapters = [c for c in _as_list(summary.get("chapters")) if isinstance(c, dict) and c.get("title")]
    if not chapters:
        chapters = [{"title": title, "start": 0, "end": record.get("duration_seconds") or 0, "gist": ""}]
    count = len(chapters)
    assigned: list[list[dict]] = [[] for _ in range(count)]
    if count == 1:
        assigned[0] = sections
    elif sections:
        # 多议题按顺序均分（保证「每节都出现在某个议题下」，不会有一节被丢掉）
        for index, section in enumerate(sections):
            target = min(count - 1, index * count // len(sections))
            assigned[target].append(section)

    topics: list[dict] = []
    for index, chapter in enumerate(chapters):
        topic = {
            "title": _topic_title(chapter),
            "start": _num(chapter.get("start")),
            "end": _num(chapter.get("end")),
            # 一句话摘要按优先级取：议题自己的 gist → 对应分节里第一条要点 → 空
            "summary": str(chapter.get("gist") or "").strip() or _first_bullet(assigned[index]),
        }
        # 这两个字段只有新结构才有；没有就不放进数据里，模板不画空框
        if _as_list(chapter.get("focus")):
            topic["focus"] = [str(item) for item in chapter["focus"] if str(item or "").strip()]
        if _as_list(chapter.get("insights")):
            topic["insights"] = [str(item) for item in chapter["insights"] if str(item or "").strip()]
        if assigned[index]:
            topic["sections"] = assigned[index]
        topics.append(topic)

    return _report_head(record, summary, visual, title, topics)


def _given_topic(topic: dict) -> dict:
    """规整调用方直接给的议题：只留模板认识的字段，空的就不放进去。"""
    cleaned = {
        "title": _topic_title(topic),
        "start": _num(topic.get("start")),
        "end": _num(topic.get("end")),
    }
    if str(topic.get("summary") or "").strip():
        cleaned["summary"] = str(topic["summary"]).strip()
    for key in ("focus", "insights"):
        values = [str(item) for item in _as_list(topic.get(key)) if str(item or "").strip()]
        if values:
            cleaned[key] = values
    sections = [s for s in _as_list(topic.get("sections")) if isinstance(s, dict) and s.get("title")]
    if sections:
        cleaned["sections"] = sections
    return cleaned


def _report_head(record: dict, summary: dict, visual: dict, title: str, topics: list[dict]) -> dict:
    """报告的头尾（标题区 / 参与人 / 待办 / 全场洞察）——与议题怎么来无关。"""
    report = {
        "title": title,
        "category": str(record.get("category") or "").strip() or "会议纪要",
        "subtitle": str(record.get("subtitle") or visual.get("summary") or summary.get("minutes") or "").strip()[:80],
        "tags": [str(tag) for tag in _as_list(record.get("tags")) if str(tag or "").strip()][:6],
        "duration_seconds": _num(record.get("duration_seconds")),
        "topics": topics,
    }
    speakers = list((record.get("speaker_names") or {}).values())
    if speakers:
        report["participants"] = [name for name in dict.fromkeys(speakers) if name]
    todos = []
    for todo in _as_list(record.get("todos")):
        if not isinstance(todo, dict):
            continue
        task = str(todo.get("task") or "").strip()
        if not task:
            continue
        todos.append({
            "task": task,
            "owner_hint": str(todo.get("owner_hint") or "").strip(),
            "due_hint": str(todo.get("due_hint") or "").strip(),
        })
    if todos:
        report["todos"] = todos
    if _as_list(summary.get("insights")):
        report["insights"] = [str(item) for item in summary["insights"] if str(item or "").strip()]
    return report


def _num(value) -> float:
    try:
        return float(value or 0)
    except (TypeError, ValueError):
        return 0.0


def _first_bullet(sections: list[dict]) -> str:
    for section in sections:
        for item in _as_list(section.get("items")):
            if isinstance(item, dict) and _as_list(item.get("bullets")):
                return str(item["bullets"][0])[:60]
    return ""


# ── 浏览器 ──


def find_browser(explicit: str | None = None) -> str:
    """找可用的 Chrome/Edge；找不到时给一条能直接照做的提示，而不是干巴巴报错。"""
    if explicit:
        if not Path(explicit).exists():
            raise ReportRenderError(f"指定的浏览器不存在：{explicit}")
        return explicit
    for path in BROWSER_CANDIDATES:
        if Path(path).exists():
            return path
    for name in ("chrome", "google-chrome", "chromium", "chromium-browser", "msedge"):
        found = shutil.which(name)
        if found:
            return found
    raise ReportRenderError(
        "找不到 Chrome/Edge，无法把报告渲染成图片。"
        "装一个 Chrome/Edge，或用 --browser 指定可执行文件路径"
        "（仍不想装的话：直接把输出的 HTML 拖进浏览器手动截图/打印）"
    )


def resolve_template(explicit: str | None = None) -> Path:
    template = Path(explicit) if explicit else REPORT_TEMPLATE_DIR / TEMPLATE_NAME
    if not template.exists():
        raise ReportRenderError(f"报告模板不存在：{template}")
    return template


def _build_page(template: Path, data: dict, work: Path) -> Path:
    """把数据注入模板，写成一份临时 HTML（file:// 直接可开，不用起服务）。"""
    html = template.read_text(encoding="utf-8")
    payload = base64.b64encode(json.dumps(data, ensure_ascii=False).encode("utf-8")).decode("ascii")
    # base64 注入：数据里的引号、</script> 都不会破坏页面
    inject = (
        "<script>"
        f"window.__REPORT__=JSON.parse(decodeURIComponent(escape(atob('{payload}'))));"
        "</script>"
    )
    page = work / "_report.html"
    page.write_text(html.replace("</head>", inject + "\n</head>", 1), encoding="utf-8")
    return page


def _uri(path: Path) -> str:
    # Windows 路径转 file:// 时必须编码空格与非 ASCII（否则 Chrome 拿到的是坏 URL）
    return "file:///" + quote(path.resolve().as_posix(), safe="/:")


def _run_chrome(chrome: str, args: list[str], timeout: float) -> tuple[int, str]:
    """跑一次无头浏览器。stdout/stderr 一律按字节读，再宽松解码——见模块注释坑 1。"""
    proc = subprocess.run(
        [chrome, "--headless=new", "--disable-gpu", "--no-sandbox", "--hide-scrollbars", *args],
        capture_output=True,
        timeout=timeout,
    )
    output = (proc.stdout or b"") + (proc.stderr or b"")
    return proc.returncode, output.decode("utf-8", errors="replace")


def _measure_height(chrome: str, page: Path, width: int) -> int:
    """让页面自己报告高度：临时往 DOM 里塞一段脚本，用 --dump-dom 取回。"""
    probe = page.with_name("_probe.html")
    probe.write_text(
        page.read_text(encoding="utf-8").replace(
            "</body>",
            "<script>var r=document.getElementById('root');"
            "r.setAttribute('data-h',String(Math.ceil(r.getBoundingClientRect().height)));</script></body>",
        ),
        encoding="utf-8",
    )
    _, dom = _run_chrome(chrome, [f"--window-size={width},1200", "--dump-dom", _uri(probe)], timeout=120)
    match = re.search(r'data-h="(\d+)"', dom)
    if not match:
        raise ReportRenderError("无法测量报告高度：浏览器没有返回页面 DOM（模板里缺少 #root？）")
    return int(match.group(1))


def _shot(chrome: str, page: Path, out: Path, width: int, height: int) -> None:
    out.parent.mkdir(parents=True, exist_ok=True)
    out.unlink(missing_ok=True)
    code, output = _run_chrome(chrome, [f"--window-size={width},{height}", f"--screenshot={out}", _uri(page)], timeout=180)
    if not out.exists() or out.stat().st_size == 0:
        detail = " ".join(output.split())[-300:]
        raise ReportRenderError(f"截图失败（浏览器退出码 {code}，{width}x{height}）：{detail}")


def render_png(chrome: str, page: Path, out: Path, width: int = DEFAULT_WIDTH) -> Path:
    """整页 PNG；页面过长时分段截图再纵向拼接（拼接要 Pillow，只有长报告才用得上）。

    长报告（>12000px）才需要 Pillow，所以它没进硬依赖、只在 `render` extra 里。
    **检查放在截图之前**：实测先截好几段再抛 ImportError，用户看到的是一堆临时 PNG
    和一句「装 pillow」；现在开头就问一次，失败时不产出任何半成品。
    """
    total = _measure_height(chrome, page, width)
    image = None
    if total > MAX_SHOT_HEIGHT:
        try:
            from PIL import Image as image
        except ImportError as exc:
            raise ReportRenderError(
                f"报告高 {total}px，超过单次截图上限（{MAX_SHOT_HEIGHT}px），需要分段拼接，"
                "但没装 Pillow。装一个再跑：pip install pillow（或 pip install 'minutecraft[render]'）；"
                "不装的话可以加 --pdf 出 PDF，同样能交差"
            ) from exc

    if total <= MAX_SHOT_HEIGHT:
        _shot(chrome, page, out, width, total)
        return out
    assert image is not None

    out.parent.mkdir(parents=True, exist_ok=True)
    parts: list[Path] = []
    offset = 0
    index = 0
    source = page.read_text(encoding="utf-8")
    while offset < total:
        chunk = min(MAX_SHOT_HEIGHT, total - offset)
        # 用负 margin 把内容整体上移，等价于「滚到第 N 段再截」
        shifted = page.with_name("_shift.html")
        shifted.write_text(
            source.replace("</body>", f"<script>document.body.style.marginTop='-{offset}px';</script></body>"),
            encoding="utf-8",
        )
        part = page.with_name(f"_part{index}.png")
        _shot(chrome, shifted, part, width, chunk)
        parts.append(part)
        offset += chunk
        index += 1

    images = [Image.open(path) for path in parts]
    merged = Image.new("RGB", (images[0].width, sum(image.height for image in images)), "white")
    y = 0
    for image in images:
        merged.paste(image, (0, y))
        y += image.height
    merged.save(out)
    for part in parts:
        part.unlink(missing_ok=True)
    return out


def render_pdf(chrome: str, page: Path, out: Path) -> Path:
    out.parent.mkdir(parents=True, exist_ok=True)
    out.unlink(missing_ok=True)
    code, output = _run_chrome(chrome, ["--no-pdf-header-footer", f"--print-to-pdf={out}", _uri(page)], timeout=180)
    if not out.exists() or out.stat().st_size == 0:
        detail = " ".join(output.split())[-300:]
        raise ReportRenderError(f"PDF 生成失败（浏览器退出码 {code}）：{detail}")
    return out


# ── 对外入口 ──


def render_report(
    record: dict,
    out_png: str | Path,
    *,
    pdf: str | Path | None = None,
    width: int = DEFAULT_WIDTH,
    template: str | Path | None = None,
    browser: str | None = None,
) -> dict:
    """纪要记录 → (PNG, 可选 PDF)。返回 {"png": 绝对路径, "pdf": 绝对路径或 ""}。

    浏览器找不到 / 模板缺失 / 截图失败都抛 `ReportRenderError`，消息可以直接照着做。
    """
    resolved_template = resolve_template(str(template) if template else None)
    chrome = find_browser(browser)
    data = build_report(record)
    png = Path(out_png).expanduser().resolve()
    pdf_path = Path(pdf).expanduser().resolve() if pdf else None

    started = time.time()
    with tempfile.TemporaryDirectory(prefix="minutecraft-report-") as tmp:
        page = _build_page(resolved_template, data, Path(tmp))
        render_png(chrome, page, png, width)
        if pdf_path:
            render_pdf(chrome, page, pdf_path)
    return {
        "png": str(png),
        "pdf": str(pdf_path) if pdf_path else "",
        "browser": chrome,
        "width": width,
        "topics": len(data["topics"]),
        "elapsed_seconds": round(time.time() - started, 2),
    }
