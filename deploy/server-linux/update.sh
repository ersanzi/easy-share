#!/usr/bin/env bash
# EasyShare 知识服务 快速更新（Linux 服务器）。
# 日常迭代节奏：开发机推 git → 服务器跑本脚本（约 10 秒停机）。
#
# 做什么：git pull --ff-only → pip 依赖同步 → systemctl restart → /health 探活；
#         探活失败自动回退到更新前提交并重启（回滚只回代码，不动数据）。
# 不做什么：控制面 jar（走开发机 scripts/ship-control-plane.ps1）、
#           PG 增量 SQL（deploy/ruoyi-db/*.sql 手动灌，灌前 pg_dump 备份）、
#           .env / data/ / rustfs-data（都不在 git 里，天然安全）。
#
# 用法：bash /opt/easyshare/repo/deploy/server-linux/update.sh [--no-mirror]
set -euo pipefail

ROOT="/opt/easyshare"
REPO="$ROOT/repo"
KN="$REPO/knowledge"
PORT=$(grep '^PORT=' "$KN/.env" 2>/dev/null | cut -d= -f2- || echo 8000)
NO_MIRROR=0
[[ "${1:-}" == "--no-mirror" ]] && NO_MIRROR=1

log()  { printf '\n==> %s\n' "$1"; }
ok()   { printf '    [OK] %s\n' "$1"; }
fail() { printf '    [失败] %s\n' "$1" >&2; }

[[ -d "$REPO/.git" ]] || { fail "$REPO 不是 git 仓库"; exit 1; }
[[ -x "$KN/.venv/bin/python" ]] || { fail "虚拟环境缺失，先跑 deploy.sh"; exit 1; }

OLD_HEAD=$(git -C "$REPO" rev-parse --short HEAD)
log "当前 $OLD_HEAD，同步 origin/dev（服务器跟 dev 分支：快速迭代主战场）"
# fetch + reset 而非 pull：服务器仓库不该有本地改动，确定性同步到刚推的提交
if ! git -C "$REPO" fetch origin dev; then
    fail "git fetch 失败（网络/凭据）。未做任何变更，退出。"
    exit 1
fi
git -C "$REPO" reset --hard origin/dev
NEW_HEAD=$(git -C "$REPO" rev-parse --short HEAD)
if [[ "$OLD_HEAD" == "$NEW_HEAD" ]]; then
    ok "已是最新（$NEW_HEAD），仍重启一次服务以应用 .env 等手工改动"
fi

log "同步 Python 依赖（有变化才需要，幂等很快）"
PIP_ARGS=(install --quiet --disable-pip-version-check -r "$KN/requirements.txt")
if [[ $NO_MIRROR -eq 0 ]]; then PIP_ARGS=(-i https://pypi.tuna.tsinghua.edu.cn/simple "${PIP_ARGS[@]}"); fi
"$KN/.venv/bin/pip" "${PIP_ARGS[@]}"

restart_and_check() {
    systemctl restart easyshare-knowledge
    for ((i = 0; i < 40; i++)); do
        body=$(curl -fsS --max-time 3 "http://127.0.0.1:${PORT}/health" 2>/dev/null || true)
        [[ "$body" == *'"ok"'* ]] && return 0
        sleep 1
    done
    return 1
}

log "重启知识服务并探活（$OLD_HEAD → $NEW_HEAD）"
if restart_and_check; then
    ok "更新完成：$OLD_HEAD → $NEW_HEAD（日志：journalctl -u easyshare-knowledge -f）"
else
    fail "新版本未过健康检查，自动回退到 $OLD_HEAD"
    journalctl -u easyshare-knowledge -n 20 --no-pager || true
    git -C "$REPO" reset --hard "$OLD_HEAD"
    "$KN/.venv/bin/pip" "${PIP_ARGS[@]}" >/dev/null 2>&1 || true
    if restart_and_check; then
        ok "已回退到 $OLD_HEAD 并恢复服务。请在开发机排查后重推"
    else
        fail "回退后仍不健康，请人工介入：journalctl -u easyshare-knowledge -n 50 --no-pager"
        exit 1
    fi
    exit 1
fi
