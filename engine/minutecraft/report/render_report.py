"""用无头浏览器把 report.html + 数据渲染成 PNG / PDF。

为什么锁 Chrome：Edge/Chrome 的 `--screenshot` 对超长页面（本次实测 ~15000px）会被截断或
超出 GPU 纹理上限，所以走 `--print-to-pdf` 再栅格化，或分片截图后拼接。这里两条都支持：
  - 单张长图超过 max 高度时，自动分段截图再纵向拼接（PIL）
  - PDF 走 Chrome 原生打印，版式与网页一致
用法（命令行）：
    python render_report.py data.json -o out.png [--pdf out.pdf] [--width 1080]
"""
from __future__ import annotations

import argparse
import base64
import json
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

CHROME_CANDIDATES = [
    r"C:\Program Files\Google\Chrome\Application\chrome.exe",
    r"C:\Program Files (x86)\Google\Chrome\Application\chrome.exe",
    r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe",
    r"C:\Program Files\Microsoft\Edge\Application\msedge.exe",
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/usr/bin/google-chrome",
    "/usr/bin/chromium",
    "/usr/bin/chromium-browser",
]

# Chrome headless 单次截图的最大安全高度（超出后栅格化会失败或返回空白）
MAX_SHOT_HEIGHT = 12000


def find_chrome(explicit: str | None = None) -> str:
    if explicit:
        return explicit
    for path in CHROME_CANDIDATES:
        if Path(path).exists():
            return path
    found = shutil.which("chrome") or shutil.which("google-chrome") or shutil.which("chromium")
    if found:
        return found
    raise RuntimeError("找不到 Chrome/Edge。请用 --chrome 指定可执行文件路径")


def _build_page(template: Path, data: dict, out_dir: Path) -> Path:
    """把数据注入模板，写成一个临时 HTML（file:// 可直接打开，免起服务）。"""
    html = template.read_text(encoding="utf-8")
    payload = base64.b64encode(json.dumps(data, ensure_ascii=False).encode("utf-8")).decode("ascii")
    # 用 base64 注入，避免数据里的 </script> 或引号破坏页面
    inject = (
        "<script>"
        f"window.__REPORT__=JSON.parse(decodeURIComponent(escape(atob('{payload}'))));"
        "</script>"
    )
    html = html.replace("</head>", inject + "\n</head>", 1)
    page = out_dir / "_page.html"
    page.write_text(html, encoding="utf-8")
    return page


def _page_height(chrome: str, page: Path, width: int) -> int:
    """让页面自己报告高度（用 --dump-dom 不便，改用临时脚本写进标题再解析）。"""
    probe = page.with_name("_probe.html")
    html = page.read_text(encoding="utf-8")
    html = html.replace(
        "</body>",
        "<script>document.title='H='+document.documentElement.scrollHeight;</script></body>",
    )
    probe.write_text(html, encoding="utf-8")
    result = subprocess.run(
        [chrome, "--headless=new", "--disable-gpu", "--no-sandbox",
         f"--window-size={width},2000", "--dump-dom", probe.as_uri()],
        capture_output=True, text=True, encoding="utf-8", errors="replace", timeout=120,
    )
    match = re.search(r"H=(\d+)", result.stdout or "")
    if not match:
        raise RuntimeError("无法测量页面高度（Chrome dump-dom 未返回标题）")
    return int(match.group(1))


def _shot(chrome: str, page: Path, out: Path, width: int, height: int) -> None:
    """截一张图。

    注意两处踩过的坑：
    1. **`--screenshot` 必须用等号**（`--screenshot=/path/x.png`）。写成 `--screenshot /path/x.png`
       两个独立参数时，Chrome 会把路径当成第二个 URL，报
       "Multiple targets are not supported in headless mode"（退出码 13）且不产出文件；
    2. **不要加 `--force-device-scale-factor=2`**——它按 2 倍栅格化，输出尺寸翻倍，容易撞上
       GPU 纹理上限而静默失败。
    """
    out.parent.mkdir(parents=True, exist_ok=True)
    if out.exists():
        out.unlink()
    subprocess.run(
        [chrome, "--headless=new", "--disable-gpu", "--no-sandbox", "--hide-scrollbars",
         f"--window-size={width},{height}", f"--screenshot={out}", page.as_uri()],
        capture_output=True, text=True, timeout=180,
    )
    if not out.exists() or out.stat().st_size == 0:
        raise RuntimeError(f"截图失败：Chrome 未产出文件（{width}x{height}）")


def render_png(chrome: str, page: Path, out: Path, width: int) -> None:
    """整页 PNG；页面过长时分段截图再拼接（见模块注释）。"""
    total = _page_height(chrome, page, width)
    out.parent.mkdir(parents=True, exist_ok=True)
    if total <= MAX_SHOT_HEIGHT:
        _shot(chrome, page, out, width, total)
        return

    from PIL import Image

    parts: list[Path] = []
    offset = 0
    index = 0
    while offset < total:
        chunk = min(MAX_SHOT_HEIGHT, total - offset)
        part = page.with_name(f"_part{index}.png")
        # 用 scrollTo 截图：给页面临时加负 margin 把内容上移，等价于分段
        html = page.read_text(encoding="utf-8")
        shifted = html.replace(
            "</body>",
            f"<script>document.body.style.marginTop='-{offset}px';</script></body>",
        )
        page.with_name("_shift.html").write_text(shifted, encoding="utf-8")
        _shot(chrome, page.with_name("_shift.html"), part, width, chunk)
        parts.append(part)
        offset += chunk
        index += 1

    images = [Image.open(p) for p in parts]
    merged = Image.new("RGB", (images[0].width, sum(img.height for img in images)), "white")
    y = 0
    for img in images:
        merged.paste(img, (0, y))
        y += img.height
    merged.save(out)
    for p in parts:
        p.unlink(missing_ok=True)


def render_pdf(chrome: str, page: Path, out: Path) -> None:
    out.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(
        [chrome, "--headless=new", "--disable-gpu", "--no-sandbox",
         "--no-pdf-header-footer", f"--print-to-pdf={out}", page.as_uri()],
        capture_output=True, text=True, timeout=180,
    )
    if not out.exists() or out.stat().st_size == 0:
        raise RuntimeError("PDF 生成失败")


def main() -> int:
    parser = argparse.ArgumentParser(description="把报告数据渲染成 PNG/PDF")
    parser.add_argument("data", help="报告 JSON（含 topics/sections 结构）")
    parser.add_argument("-o", "--out", required=True, help="输出 PNG 路径")
    parser.add_argument("--pdf", help="同时输出 PDF 路径")
    parser.add_argument("--width", type=int, default=1080, help="页面宽度（默认 1080）")
    parser.add_argument("--template", help="模板路径（默认同目录 report.html）")
    parser.add_argument("--chrome", help="Chrome/Edge 可执行文件路径")
    args = parser.parse_args()

    data = json.loads(Path(args.data).read_text(encoding="utf-8-sig"))
    template = Path(args.template) if args.template else Path(__file__).with_name("report.html")
    chrome = find_chrome(args.chrome)
    # 输出路径必须绝对化：Chrome 子进程的 CWD 与当前进程未必一致，
    # 相对路径会让它把图写到别处或干脆不产出（2026-10-19 实测踩过）
    out = Path(args.out).expanduser().resolve()

    with tempfile.TemporaryDirectory() as tmp:
        work = Path(tmp)
        page = _build_page(template, data, work)
        out = Path(args.out)
        render_png(chrome, page, out, args.width)
        print(f"PNG  {out}  {out.stat().st_size} bytes")
        if args.pdf:
            pdf = Path(args.pdf)
            render_pdf(chrome, page, pdf)
            print(f"PDF  {pdf}  {pdf.stat().st_size} bytes")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
