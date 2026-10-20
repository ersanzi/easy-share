#!/usr/bin/env bash
# 一键安装：检查 Python → 建虚拟环境（可选）→ 装本包 → 验证
# 用法：bash install.sh          （装到当前 Python）
#       bash install.sh --venv   （在当前目录建 .venv，避免污染系统环境）
set -euo pipefail
cd "$(dirname "$0")"

echo "== 1/4 检查 Python =="
if ! command -v python3 >/dev/null 2>&1 && ! command -v python >/dev/null 2>&1; then
  echo "未找到 Python。请先安装 3.10+：" >&2
  echo "  macOS:  brew install python@3.12" >&2
  echo "  Ubuntu: sudo apt install python3 python3-pip python3-venv" >&2
  exit 1
fi
PY=$(command -v python3 || command -v python)
"$PY" - <<'EOF'
import sys
if sys.version_info < (3, 10):
    sys.exit("需要 Python 3.10+，当前是 %s" % sys.version.split()[0])
print("Python", sys.version.split()[0])
EOF

if [[ "${1:-}" == "--venv" ]]; then
  echo "== 2/4 创建虚拟环境 .venv =="
  "$PY" -m venv .venv
  # shellcheck disable=SC1091
  source .venv/bin/activate
  PY=python
else
  echo "== 2/4 直接安装到当前环境 =="
fi

echo "== 3/4 安装 minutecraft =="
"$PY" -m pip install --quiet --upgrade pip
"$PY" -m pip install -e .

echo "== 4/4 验证 =="
"$PY" -m minutecraft --version

echo
echo "安装完成。下一步：配置 LLM 环境变量后运行"
echo '  export MINUTECRAFT_LLM_BASE_URL=... MINUTECRAFT_LLM_API_KEY=... MINUTECRAFT_LLM_MODEL=...'
echo "  minutecraft run <转写文件> --out ./out --json"
