"""minutecraft 命令行：给 Agent（WorkBuddy / DSH / Codex / Claude Code）与人工共用的稳定入口。

**Agent 用两步走（推荐，零 LLM 配置）**：
  prepare  转写文本 → 待回答的请求包（纯工程，不调模型）
  finish   把宿主 Agent 的模型回答 + 请求包 → 纪要与全部产物（纯工程）

**无人值守时用一步到位**（需要自配 LLM 端点）：
  run      转写文本 → 纪要 + Word/MD/JSON（内置 LLM 客户端）

辅助：schema（产物结构自描述）、visual（汇报卡结构）、templates（内置模板）、
      render（长网页版式报告 → PNG/PDF，需要本机 Chrome/Edge）

退出码：0=成功、2=输入无法解析、3=引擎/配置失败（含找不到浏览器）。全部支持 `--json`。

示例：
  minutecraft prepare meeting.txt -o req.json
  # → 把 req.json 里的 requests 交给你的模型，把回答存成 answers.json
  minutecraft finish req.json --answers answers.json -o out/
  minutecraft render out/minutes.json -o out/report.png --pdf out/report.pdf
  minutecraft run transcript.txt --out out/ --template weekly
"""
from __future__ import annotations

import argparse
import json
import logging
import sys
from pathlib import Path

from . import __version__
from .docx.visual import build_visual_report
from .engine import LLMSettings, MinuteEngine, MinuteError
from .models import SCHEMA
from .render import ReportRenderError, render_report
from .templates import BUILTIN_TEMPLATES
from .transcript import TranscriptFormatError, load_transcript

EXIT_INPUT = 2
EXIT_ENGINE = 3
# 产物出来了，但有东西被降级（缺 topics / 缺 keywords / 议题解析失败……）。
# 单列一个退出码而不是塞进 0，是因为 **exit 0 会被 Agent 当成「全都齐了」**：
# 实测踩过 keywords 给裸数组被静默降级、exit code 仍是 0，得翻 warnings 才发现关键词丢了。
EXIT_PARTIAL = 4


def _configure_logging(verbose: bool) -> None:
    logging.basicConfig(
        level=logging.INFO if verbose else logging.WARNING,
        format="%(levelname)s %(name)s: %(message)s",
    )


def _emit(payload: dict, as_json: bool) -> None:
    if as_json:
        print(json.dumps(payload, ensure_ascii=False, indent=2))
        return
    for key, value in payload.items():
        if isinstance(value, (dict, list)):
            print(f"{key}:")
            print(json.dumps(value, ensure_ascii=False, indent=2))
        else:
            print(f"{key}: {value}")


def _finish_code(warnings: list[str]) -> int:
    """有降级就返回 4，并**把警告打到 stderr**。

    两个出口都做，是因为 Agent 未必读 stdout 的 JSON，但一定会看退出码和 stderr：
    - 退出码：`0` = 齐全，`4` = 产物已出但有缺失（可重跑补齐，不是失败）
    - stderr：把每条 warning 原样打出来，不用再解析 JSON 才看得到
    """
    if not warnings:
        return 0
    print(f"产物已生成，但有 {len(warnings)} 项降级：", file=sys.stderr)
    for warning in warnings:
        print(f"  - {warning}", file=sys.stderr)
    return EXIT_PARTIAL


def cmd_run(args: argparse.Namespace) -> int:
    try:
        transcript = load_transcript(args.input)
    except (TranscriptFormatError, FileNotFoundError, json.JSONDecodeError, UnicodeDecodeError) as exc:
        print(f"输入无法解析：{exc}", file=sys.stderr)
        return EXIT_INPUT

    settings = LLMSettings.from_env()
    # 命令行覆盖环境变量：想临时快点跑一次就不必改 shell
    if getattr(args, "thinking", None):
        settings.thinking = args.thinking
    engine = MinuteEngine(settings)
    if not engine.configured:
        # 先判环境再解析输入：Agent 的常见失败是「没配 key」，给一条能直接照做的提示，
        # 而不是等到调 LLM 才报一个笼统的「生成失败」
        print(
            "LLM 未配置：请设置 MINUTECRAFT_LLM_BASE_URL / MINUTECRAFT_LLM_API_KEY / "
            "MINUTECRAFT_LLM_MODEL（也接受 LLM_BASE_URL / LLM_API_KEY / LLM_MODEL）",
            file=sys.stderr,
        )
        return EXIT_ENGINE
    if args.input != "-":
        transcript.title = args.title or transcript.title or Path(args.input).stem
    elif args.title:
        transcript.title = args.title
    try:
        result = engine.build(
            transcript,
            template_id=args.template,
            with_keywords=not args.no_keywords,
            with_mindmap=not args.no_mindmap,
            with_docx=not args.no_docx,
            with_transcript_page=args.with_transcript,
            out_dir=args.out,
            with_topics=not getattr(args, "no_topics", False),
        )
    except MinuteError as exc:
        print(f"生成失败：{exc}", file=sys.stderr)
        return EXIT_ENGINE
    except Exception as exc:  # noqa: BLE001 - CLI 边界统一成退出码，不抛栈给 Agent
        print(f"未预期错误：{exc}", file=sys.stderr)
        return EXIT_ENGINE

    payload = {
        "ok": True,
        "title": transcript.title,
        "template": result.template_id,
        "todos": len(result.todos),
        "keywords": result.keywords,
        "chapters": len(result.summary.get("chapters") or []),
        "has_visual": bool(result.summary.get("visual")),
        "has_topics": bool(result.summary.get("topics")),
        "has_mindmap": bool(result.mindmap.strip()),
        "warnings": result.warnings,
        "files": result.files,
    }
    if args.json or args.print_summary:
        payload["summary"] = result.summary
    _emit(payload, args.json)
    return _finish_code(result.warnings)


def _result_payload(result, transcript) -> dict:
    """run / finish 共用的机器可读摘要（两条件路径产物一致，摘要也必须一致）。"""
    return {
        "ok": True,
        "title": transcript.title,
        "template": result.template_id,
        "todos": len(result.todos),
        "keywords": result.keywords,
        "chapters": len(result.summary.get("chapters") or []),
        "has_visual": bool(result.summary.get("visual")),
        "has_topics": bool(result.summary.get("topics")),
        "has_mindmap": bool(result.mindmap.strip()),
        "warnings": result.warnings,
        "files": result.files,
    }


def _load_json_source(source: str) -> dict:
    raw = json.loads(Path(source).read_text(encoding="utf-8-sig")) if source != "-" else json.load(sys.stdin)
    if not isinstance(raw, dict):
        raise ValueError("需要 JSON 对象")
    return raw


def cmd_prepare(args: argparse.Namespace) -> int:
    """第一步：转写 → 请求包。**不调模型、不需要任何 LLM 配置。**"""
    try:
        transcript = load_transcript(args.input)
    except (TranscriptFormatError, FileNotFoundError, json.JSONDecodeError, UnicodeDecodeError) as exc:
        print(f"输入无法解析：{exc}", file=sys.stderr)
        return EXIT_INPUT
    if args.input != "-":
        transcript.title = args.title or transcript.title or Path(args.input).stem
    elif args.title:
        transcript.title = args.title

    # LLMSettings 留空：prepare 只用 summarizer 的提示词组装，不碰网络
    engine = MinuteEngine(LLMSettings(base_url="", api_key="", model=""))
    try:
        payload = engine.prepare(transcript, template_id=args.template, with_keywords=not args.no_keywords,
                                 with_topics=not getattr(args, "no_topics", False))
    except MinuteError as exc:
        print(f"准备失败：{exc}", file=sys.stderr)
        return EXIT_ENGINE

    if args.out:
        target = Path(args.out)
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
    _emit(
        {
            "ok": True,
            "title": transcript.title,
            "template": payload["template"],
            "requests": list(payload["requests"]),
            "request_file": str(args.out) if args.out else "",
            "next": "把 requests 交给你的模型回答，再执行：minutecraft finish <请求包> --answers answers.json -o out/"
            "（summary 必答；topics 决定长报告正文，不答会退回按章节均分的粗版式并记一条警告）",
            **({"payload": payload} if not args.out else {}),
        },
        args.json,
    )
    return 0


def cmd_finish(args: argparse.Namespace) -> int:
    """第二步：请求包 + Agent 的回答 → 全部产物。**不调模型、不需要任何 LLM 配置。**"""
    try:
        payload = _load_json_source(args.payload)
        answers = _load_json_source(args.answers)
    except (FileNotFoundError, json.JSONDecodeError, ValueError) as exc:
        print(f"输入无法解析：{exc}", file=sys.stderr)
        return EXIT_INPUT
    if not (payload.get("requests") or {}).get("summary"):
        print("不是有效的请求包（缺少 requests.summary）——请用 prepare 子命令生成", file=sys.stderr)
        return EXIT_INPUT

    engine = MinuteEngine(LLMSettings(base_url="", api_key="", model=""))
    try:
        result = engine.finish(
            payload,
            answers,
            with_keywords=not args.no_keywords,
            with_docx=not args.no_docx,
            with_transcript_page=args.with_transcript,
            out_dir=args.out,
        )
    except MinuteError as exc:
        print(f"生成失败：{exc}", file=sys.stderr)
        return EXIT_ENGINE
    except Exception as exc:  # noqa: BLE001 - CLI 边界统一成退出码
        print(f"未预期错误：{exc}", file=sys.stderr)
        return EXIT_ENGINE

    out = _result_payload(result, result.transcript)
    if args.json or args.print_summary:
        out["summary"] = result.summary
    _emit(out, args.json)
    return _finish_code(result.warnings)


def cmd_render(args: argparse.Namespace) -> int:
    """纪要记录 → 长网页版式报告（PNG，可选 PDF）。需要本机有 Chrome/Edge；不调模型。"""
    try:
        record = _load_record(args.input)
    except (FileNotFoundError, json.JSONDecodeError, ValueError) as exc:
        print(f"输入无法解析：{exc}", file=sys.stderr)
        return EXIT_INPUT
    if not record:
        print("输入里没有纪要内容（需要 minutes.json，或含 summary 的记录）", file=sys.stderr)
        return EXIT_INPUT

    out_png = args.out or str(Path(args.input).with_suffix(".png")) if args.input != "-" else args.out
    if not out_png:
        print("请用 -o 指定 PNG 输出路径（从标准输入读时无法推断文件名）", file=sys.stderr)
        return EXIT_INPUT
    try:
        result = render_report(
            record,
            out_png,
            pdf=args.pdf,
            width=args.width,
            template=args.template,
            browser=args.browser,
        )
    except ReportRenderError as exc:
        print(f"渲染失败：{exc}", file=sys.stderr)
        return EXIT_ENGINE
    except Exception as exc:  # noqa: BLE001 - CLI 边界统一成退出码
        print(f"渲染失败：{exc}", file=sys.stderr)
        return EXIT_ENGINE
    _emit({"ok": True, **result}, args.json)
    return 0


def _load_record(source: str) -> dict:
    """读纪要记录：支持 minutes.json、`visual` 子命令的输出、以及带 record/result 包装的 JSON。"""
    raw = _load_json_source(source)
    record = raw.get("record") if "record" in raw else raw
    if isinstance(record, dict) and "summary" not in record and isinstance(record.get("result"), dict):
        record = record["result"]
    if not isinstance(record, dict):
        raise ValueError("需要 JSON 对象")
    return record


def cmd_schema(args: argparse.Namespace) -> int:
    _emit({"version": __version__, "schema": SCHEMA}, args.json)
    return 0


def cmd_visual(args: argparse.Namespace) -> int:
    try:
        raw = json.loads(Path(args.input).read_text(encoding="utf-8-sig")) if args.input != "-" else json.load(sys.stdin)
    except (FileNotFoundError, json.JSONDecodeError) as exc:
        print(f"输入无法解析：{exc}", file=sys.stderr)
        return EXIT_INPUT
    record = raw.get("record") if isinstance(raw, dict) and "record" in raw else raw
    if isinstance(record, dict) and "summary" not in record and "result" in record:
        record = record["result"]
    _emit({"ok": True, "visual": build_visual_report(record if isinstance(record, dict) else {})}, args.json)
    return 0


def cmd_templates(args: argparse.Namespace) -> int:
    _emit({"templates": [{k: t[k] for k in ("id", "name", "description", "sections")} for t in BUILTIN_TEMPLATES]}, args.json)
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="minutecraft",
        description="转写文本 → 会议纪要（纪要/要点/待办/章节/关键词/洞察/导图 + 钉钉版式 Word）",
    )
    parser.add_argument("--version", action="version", version=f"minutecraft {__version__}")
    parser.add_argument("-v", "--verbose", action="store_true", help="打开信息级日志")
    subs = parser.add_subparsers(dest="command", required=True)

    # ── 两步走（Agent 用，零 LLM 配置）──
    prepare = subs.add_parser("prepare", help="第一步：转写 → 待回答的请求包（不调模型）")
    prepare.add_argument("input", help="转写文件（txt/json/srt/md），或 - 从标准输入读")
    prepare.add_argument("-o", "--out", help="把请求包写到该 JSON 文件（不传则直接打印）")
    prepare.add_argument("--template", default="meeting-minutes", help="总结模板 id")
    prepare.add_argument("--title", help="覆盖会议标题")
    prepare.add_argument("--no-keywords", action="store_true", help="不要关键词请求")
    prepare.add_argument("--no-topics", action="store_true", help="不要报告议题请求（只要纪要与 Word 时省一次调用）")
    prepare.add_argument("--json", action="store_true", help="机器可读输出")
    prepare.set_defaults(func=cmd_prepare)

    finish = subs.add_parser("finish", help="第二步：请求包 + 模型回答 → 纪要与产物（不调模型）")
    finish.add_argument("payload", help="prepare 产出的请求包 JSON（或 - 从标准输入读）")
    finish.add_argument("--answers", required=True, help='模型回答 JSON：{"summary": "...", "keywords": "..."}')
    finish.add_argument("-o", "--out", "--out-dir", dest="out", help="输出目录")
    finish.add_argument("--no-keywords", action="store_true", help="忽略关键词回答")
    finish.add_argument("--no-docx", action="store_true", help="不导出 Word（仍写 md/json）")
    finish.add_argument("--with-transcript", action="store_true", help="Word 末尾附转写全文附页")
    finish.add_argument("--json", action="store_true", help="机器可读输出")
    finish.add_argument("--print-summary", action="store_true", help="纯文本模式下也打印纪要正文")
    finish.set_defaults(func=cmd_finish)

    # ── 一步到位（无人值守，需自配 LLM）──
    run = subs.add_parser("run", help="生成会议纪要（内置 LLM 客户端，需配置端点）")
    run.add_argument("input", help="转写文件（txt/json/srt/md），或 - 从标准输入读")
    run.add_argument("--out", help="输出目录（不传则只打印结果，不落盘）")
    run.add_argument("--template", default="meeting-minutes", help="总结模板 id（见 templates 子命令）")
    run.add_argument("--title", help="覆盖会议标题（缺省取文件名）")
    run.add_argument("--no-keywords", action="store_true", help="不生成关键词")
    run.add_argument("--no-mindmap", action="store_true", help="不生成思维导图")
    run.add_argument("--no-topics", action="store_true",
                     help="不生成报告议题（省掉最慢的一次调用：实测 82 分钟会议约 250-300s，代价是长图只剩章节均分）")
    run.add_argument("--thinking", choices=["auto", "none", "low", "medium", "high"],
                     help="思考档位（默认 auto）：auto 按转写长短自动降档——≤3k 字用 medium、"
                          "3k-8k 用 low、8k 字以上（约 1 小时会）用 none；给具体值就全局固定")
    run.add_argument("--no-docx", action="store_true", help="不导出 Word（仍写 md/json）")
    run.add_argument("--with-transcript", action="store_true", help="Word 末尾附转写全文附页")
    run.add_argument("--json", action="store_true", help="输出机器可读 JSON（Agent 用）")
    run.add_argument("--print-summary", action="store_true", help="纯文本模式下也打印纪要正文")
    run.set_defaults(func=cmd_run)

    schema = subs.add_parser("schema", help="打印输入/产物结构（Agent 自描述）")
    schema.add_argument("--json", action="store_true")
    schema.set_defaults(func=cmd_schema)

    visual = subs.add_parser("visual", help="只生成可视化报告结构")
    visual.add_argument("input", help="minutes.json（run 的产物）或 - 从标准输入读")
    visual.add_argument("--json", action="store_true")
    visual.set_defaults(func=cmd_visual)

    templates = subs.add_parser("templates", help="列出内置总结模板")
    templates.add_argument("--json", action="store_true")
    templates.set_defaults(func=cmd_templates)

    render = subs.add_parser("render", help="纪要记录 → 长网页版式报告 PNG/PDF（需本机 Chrome/Edge）")
    render.add_argument("input", help="minutes.json（run/finish 的产物），或 - 从标准输入读")
    render.add_argument("-o", "--out", help="PNG 输出路径（缺省与输入同名 .png）")
    render.add_argument("--pdf", help="同时输出 PDF 的路径")
    render.add_argument("--width", type=int, default=1080, help="页面宽度（默认 1080）")
    render.add_argument("--template", help="报告模板路径（默认用引擎内置 report.html）")
    render.add_argument("--browser", help="Chrome/Edge 可执行文件路径（默认自动查找）")
    render.add_argument("--json", action="store_true", help="机器可读输出")
    render.set_defaults(func=cmd_render)
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    _configure_logging(getattr(args, "verbose", False))
    return int(args.func(args))


if __name__ == "__main__":  # pragma: no cover
    raise SystemExit(main())
