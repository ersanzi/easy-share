"""skills/minutes-from-transcript/scripts 是 engine/ 的发布副本，本脚本检查两者是否一致。

为什么需要：skill 必须能「整个复制走」独立使用，所以源码要自带一份；
两份并存必然漂移，漂移后「产品里生成的」和「复制出去生成的」就不一样了。
本脚本做机械对账：缺文件 / 内容不同 / 多出文件，全部列出并返回非零退出码。

用法（仓库根目录）：
    python scripts/check_skill_sync.py           # 检查
    python scripts/check_skill_sync.py --apply   # 用 engine/ 覆盖到 skill（改完引擎后跑这个）
"""
from __future__ import annotations

import argparse
import shutil
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "engine"
TARGET = ROOT / "skills" / "minutes-from-transcript" / "scripts"

# 只比对产物源码；缓存与构建产物不参与
SKIP_DIRS = {"__pycache__", ".pytest_cache", "build", "dist", ".egg-info", "tests"}
# skill 副本独有、engine/ 里本来就没有的文件（安装脚本与说明，属于分发层而非引擎本体）
SKILL_ONLY = {"README.md", "install.sh", "install.ps1"}


def collect(base: Path) -> dict[str, Path]:
    files: dict[str, Path] = {}
    for path in base.rglob("*"):
        if not path.is_file():
            continue
        rel = path.relative_to(base)
        if any(part in SKIP_DIRS or part.endswith(".egg-info") for part in rel.parts):
            continue
        if rel.name in SKILL_ONLY:
            continue
        files[str(rel).replace("\\", "/")] = path
    return files


def main() -> int:
    parser = argparse.ArgumentParser(description="对账 engine/ 与 skill 内的发布副本")
    parser.add_argument("--apply", action="store_true", help="用 engine/ 覆盖 skill 副本")
    args = parser.parse_args()

    if not SOURCE.is_dir() or not TARGET.is_dir():
        print(f"目录不存在：{SOURCE} 或 {TARGET}", file=sys.stderr)
        return 2

    source_files = collect(SOURCE)
    target_files = collect(TARGET)

    missing = sorted(set(source_files) - set(target_files))
    extra = sorted(set(target_files) - set(source_files))
    differ = sorted(
        name for name in set(source_files) & set(target_files)
        if source_files[name].read_bytes() != target_files[name].read_bytes()
    )

    if args.apply:
        for name in missing + differ:
            destination = TARGET / name
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source_files[name], destination)
        print(f"已同步 {len(missing) + len(differ)} 个文件到 skill 副本")
        if extra:
            print("注意：skill 里还有 engine/ 中没有的文件（未删除，请人工确认）：")
            for name in extra:
                print(f"  + {name}")
        return 0

    if not (missing or extra or differ):
        print(f"一致：{len(source_files)} 个文件")
        return 0

    print("skill 副本与 engine/ 不一致：", file=sys.stderr)
    for name in missing:
        print(f"  缺失  {name}", file=sys.stderr)
    for name in differ:
        print(f"  不同  {name}", file=sys.stderr)
    for name in extra:
        print(f"  多出  {name}", file=sys.stderr)
    print("\n改完引擎后执行：python scripts/check_skill_sync.py --apply", file=sys.stderr)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
