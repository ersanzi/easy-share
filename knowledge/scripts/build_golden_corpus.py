"""把测试中的黄金样本构造器物化为可人工打开检查的文件。"""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

KNOWLEDGE_ROOT = Path(__file__).resolve().parents[1]
if str(KNOWLEDGE_ROOT) not in sys.path:
    sys.path.insert(0, str(KNOWLEDGE_ROOT))

from tests.golden.builders import build_case  # noqa: E402


def main() -> None:
    parser = argparse.ArgumentParser(description="生成 EasyShare Office 黄金测试文档")
    parser.add_argument(
        "--output",
        type=Path,
        default=KNOWLEDGE_ROOT / "tests" / "golden" / "generated",
        help="输出目录，默认 knowledge/tests/golden/generated",
    )
    args = parser.parse_args()
    cases_path = KNOWLEDGE_ROOT / "tests" / "golden" / "cases.json"
    cases = json.loads(cases_path.read_text(encoding="utf-8"))
    args.output.mkdir(parents=True, exist_ok=True)
    for case in cases:
        target = args.output / case["filename"]
        target.write_bytes(build_case(case["builder"]))
        print(target)


if __name__ == "__main__":
    main()