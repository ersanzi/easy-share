"""从 QueryLog 生成观察期周报文本（stdout 或 --output 文件）。

用法（部署机或开发机均可跑，只读 query_log.db，不改任何状态）：
    python scripts/weekly_report.py                    # 最近 7 天，读 .env 配置的库
    python scripts/weekly_report.py --days 14          # 两周观察期整窗
    python scripts/weekly_report.py --output 报告.txt  # 写文件
"""
from __future__ import annotations

import argparse
import sys
from pathlib import Path

KNOWLEDGE_ROOT = Path(__file__).resolve().parents[1]
if str(KNOWLEDGE_ROOT) not in sys.path:
    sys.path.insert(0, str(KNOWLEDGE_ROOT))

from app.config import settings  # noqa: E402
from app.kb.query_log import QueryLog  # noqa: E402
from app.kb.weekly_report import build_weekly_report  # noqa: E402


def main() -> int:
    parser = argparse.ArgumentParser(description="EasyShare 知识服务观察周报")
    parser.add_argument("--days", type=int, default=7, help="观察窗口天数（默认 7，两周观察用 14）")
    parser.add_argument("--db", type=Path, default=None, help="query_log.db 路径（默认读 .env 的 QUERY_LOG_PATH）")
    parser.add_argument("--output", type=Path, default=None, help="报告写入文件（默认打印 stdout）")
    args = parser.parse_args()

    db_path = args.db or Path(settings.query_log_path)
    if not db_path.exists():
        print(f"日志库不存在：{db_path}（服务从未跑过或路径不对）——输出空数据周报。", file=sys.stderr)
    # QueryLog 构造会建库建表；空库走 build_weekly_report 的空数据兜底文案
    log = QueryLog(str(db_path))
    report = build_weekly_report(log, days=args.days)

    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(report + "\n", encoding="utf-8")
        print(f"周报已写入：{args.output}")
    else:
        print(report)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
