#!/usr/bin/env bash
# EasyShare 知识服务 + 基础设施 一键部署（Linux 服务器）。
# 与 knowledge/scripts/deploy.ps1（Windows 版）同职责：幂等可重跑，缺什么问什么。
#
# 前置：仓库已在服务器上——
#   sudo git clone -b dev https://gitee.com/liilaifeng/easy-share.git /opt/easyshare/repo
#   sudo bash /opt/easyshare/repo/deploy/server-linux/deploy.sh
#
# 控制面（RuoYi jar）不在本脚本职责内：jar 只能在开发机构建（platform/ 源码树
# gitignore），部署方式见 README.md「控制面上服务器」——开发机跑 scripts/ship-control-plane.ps1。
#
# 可选参数（均有合理默认，交互式补问）：
#   --root /opt/easyshare     运行时根目录
#   --port 8000                知识服务端口
#   --admin-password xxx       管理员口令（非交互必传）
#   --no-mirror                pip 不走清华镜像
#   --non-interactive          不问，按默认/env 走
set -euo pipefail

ROOT="/opt/easyshare"
PORT="8000"
ADMIN_PASSWORD=""
NO_MIRROR=0
NONINTERACTIVE=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --root) ROOT="$2"; shift 2 ;;
        --port) PORT="$2"; shift 2 ;;
        --admin-password) ADMIN_PASSWORD="$2"; shift 2 ;;
        --no-mirror) NO_MIRROR=1; shift ;;
        --non-interactive) NONINTERACTIVE=1; shift ;;
        *) echo "未知参数：$1" >&2; exit 1 ;;
    esac
done

REPO="$ROOT/repo"
CP="$ROOT/control-plane"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

log()  { printf '\n==> %s\n' "$1"; }
ok()   { printf '    [OK] %s\n' "$1"; }
fail() { printf '    [失败] %s\n' "$1" >&2; }

ask() { # ask <提示> <默认值>
    local prompt="$1" default="$2" value
    [[ $NONINTERACTIVE -eq 1 ]] && { echo "$default"; return; }
    read -r -p "$prompt（回车用默认：$default）" value || true
    echo "${value:-$default}"
}

ask_yes_no() { # ask_yes_no <提示> <默认yes/no>
    local prompt="$1" default="$2" value
    [[ $NONINTERACTIVE -eq 1 ]] && { [[ "$default" == "yes" ]] && return 0 || return 1; }
    local hint; [[ "$default" == "yes" ]] && hint="Y/n" || hint="y/N"
    read -r -p "$prompt（$hint）" value || true
    value="$(echo "$value" | tr '[:upper:]' '[:lower:]')"
    [[ -z "$value" ]] && { [[ "$default" == "yes" ]] && return 0 || return 1; }
    [[ "$value" == "y" || "$value" == "yes" ]]
}

rand_password() { python3 -c "import secrets,string;print(''.join(secrets.choice(string.ascii_letters+string.digits) for _ in range(12)))"; }

lan_ip() {
    local ip
    ip=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<NF;i++) if($i=="src"){print $(i+1); exit}}' || true)
    [[ -z "$ip" ]] && ip=$(hostname -I 2>/dev/null | awk '{print $1}')
    echo "${ip:-<服务器IP>}"
}

wait_healthy() { # wait_healthy <容器名> <超时秒>
    local name="$1" timeout="${2:-120}" i status
    for ((i=0; i<timeout/2; i++)); do
        status=$(docker inspect --format '{{.State.Health.Status}}' "$name" 2>/dev/null || echo starting)
        [[ "$status" == "healthy" ]] && return 0
        sleep 2
    done
    return 1
}

wait_http() { # wait_http <url> <超时秒> <判定子串>
    local url="$1" timeout="${2:-40}" needle="$3" i body
    for ((i=0; i<timeout; i++)); do
        body=$(curl -fsS --max-time 3 "$url" 2>/dev/null || true)
        [[ "$body" == *"$needle"* ]] && return 0
        sleep 1
    done
    return 1
}

# ---------- 0. 前置检查 ----------

log "环境检查"
[[ $EUID -eq 0 ]] || { fail "请用 root/sudo 运行（要装 systemd 单元、改防火墙）"; exit 1; }
for cmd in docker git python3 curl; do
    command -v "$cmd" >/dev/null 2>&1 || { fail "缺少 $cmd，请先安装"; exit 1; }
done
docker compose version >/dev/null 2>&1 || { fail "docker compose 不可用（装 Docker Engine 自带的 compose 插件）"; exit 1; }
PY_MINOR=$(python3 -c 'import sys;print(f"{sys.version_info.major}.{sys.version_info.minor}")')
python3 -c "import sys; sys.exit(0 if sys.version_info >= (3,11) else 1)" \
    || { fail "需要 Python 3.11+（当前 $PY_MINOR）。Ubuntu 24.04: apt install python3 python3-venv python3-pip"; exit 1; }
[[ -d "$REPO" ]] || { fail "仓库不在 $REPO。先：git clone https://gitee.com/liilaifeng/easy-share.git $REPO"; exit 1; }
ok "docker / git / python $PY_MINOR / 仓库就绪"

# ---------- 1. 布局与代码更新 ----------

log "准备目录与代码"
mkdir -p "$CP"
if [[ -d "$REPO/.git" ]]; then
    git -C "$REPO" pull --ff-only >/dev/null 2>&1 || echo "    git pull 失败/跳过（离线或冲突可忽略，用现有代码继续）"
    ok "仓库已更新（$(git -C "$REPO" rev-parse --short HEAD)）"
fi
cp "$SCRIPT_DIR/compose.yaml" "$ROOT/compose.yaml"
cp "$REPO/deploy/ruoyi-db/easyshare-drive.yml" "$CP/easyshare-drive.yml"
ok "compose 与控制面配置就位"

# ---------- 2. rustfs.env ----------

log "生成环境变量 rustfs.env（含对象存储凭据）"
if [[ -f "$CP/rustfs.env" ]]; then
    ok "已存在，保留（重置请删 $CP/rustfs.env 后重跑）"
else
    ACCESS_RAW=$(rand_password | tr '[:upper:]' '[:lower:]')
    ACCESS_KEY="easyshare-${ACCESS_RAW:0:10}"
    SECRET_KEY="$(rand_password)$(rand_password)"
    LAN_IP=$(lan_ip)
    ENDPOINT=$(ask "预签名 URL 用的 RustFS 对外地址（填服务器局域网 IP）" "http://${LAN_IP}:9000")
    CAPACITY_PATH=$(ask "云盘容量探测的宿主路径（数据盘挂载点）" "$ROOT")
    cat > "$CP/rustfs.env" <<EOF
# 由 deploy.sh 生成于 $(date '+%F %T')
RUSTFS_ACCESS_KEY=${ACCESS_KEY}
RUSTFS_SECRET_KEY=${SECRET_KEY}
RUSTFS_BUCKET=easyshare
RUSTFS_ENDPOINT=${ENDPOINT}
RUSTFS_CAPACITY_PATH=${CAPACITY_PATH}
EOF
    chmod 600 "$CP/rustfs.env"
    ok "已生成（凭据随机；改地址手编辑本文件后 docker compose restart rustfs ruoyi）"
fi
# shellcheck disable=SC1091
set -a; source "$CP/rustfs.env"; set +a

# ---------- 3. 基础设施容器 ----------

log "启动 PostgreSQL + Redis + RustFS（PG/Redis 仅本机，RustFS 对局域网开放 9000）"
docker compose --project-directory "$ROOT" -f "$ROOT/compose.yaml" up -d postgres redis rustfs
for name in easyshare-ruoyi-pg easyshare-ruoyi-redis easyshare-rustfs; do
    if wait_healthy "$name" 120; then ok "$name healthy"; else fail "$name 未就绪，看日志：docker logs $name"; exit 1; fi
done

# ---------- 4. 知识服务 Python 环境 ----------

log "准备知识服务虚拟环境"
KN="$REPO/knowledge"
if [[ -x "$KN/.venv/bin/python" ]]; then
    ok "虚拟环境已存在"
else
    python3 -m venv "$KN/.venv"
    ok "虚拟环境已创建"
fi
PIP_ARGS=(install --quiet --disable-pip-version-check -r "$KN/requirements.txt")
if [[ $NO_MIRROR -eq 0 ]]; then
    PIP_ARGS=(-i https://pypi.tuna.tsinghua.edu.cn/simple "${PIP_ARGS[@]}")
    echo "    走清华镜像（--no-mirror 关闭）"
fi
"$KN/.venv/bin/pip" "${PIP_ARGS[@]}"
ok "依赖安装完成"

# ---------- 5. 知识服务 .env ----------

log "生成知识服务 .env"
# 入库目录先兜底：.env 已存在时从中回读，供末尾生成使用指引引用
WATCH_DIR=$(grep '^WATCH_DIRS=' "$KN/.env" 2>/dev/null | cut -d= -f2- || true)
WATCH_DIR="${WATCH_DIR:-$ROOT/watch-inbox}"
if [[ -f "$KN/.env" ]]; then
    ok "已存在，保留（重置请删 $KN/.env 后重跑）"
else
    LLM_API_KEY=$(ask "LLM API Key（OpenAI 兼容；留空=纯检索模式）" "")
    LLM_BASE_URL=""; LLM_MODEL=""
    if [[ -n "$LLM_API_KEY" ]]; then
        LLM_BASE_URL=$(ask "LLM Base URL" "")
        LLM_MODEL=$(ask "LLM 模型名" "")
    fi
    EMB_API_KEY=$(ask "Embedding API Key（留空=无语义检索，仅关键词；强烈建议配）" "")
    EMB_BASE_URL=""; EMB_MODEL=""
    if [[ -n "$EMB_API_KEY" ]]; then
        EMB_BASE_URL=$(ask "Embedding Base URL" "")
        EMB_MODEL=$(ask "Embedding 模型名" "")
    fi
    WATCH_DIR=$(ask "知识库入库目录（同事往这放文件自动入库）" "$ROOT/watch-inbox")
    mkdir -p "$WATCH_DIR"
    cat > "$KN/.env" <<EOF
# 由 deploy.sh 生成于 $(date '+%F %T')
HOST=0.0.0.0
PORT=${PORT}
LOCAL_LAB_ENABLED=true

RUSTFS_ENDPOINT=http://127.0.0.1:9000
RUSTFS_ACCESS_KEY=${RUSTFS_ACCESS_KEY}
RUSTFS_SECRET_KEY=${RUSTFS_SECRET_KEY}
RUSTFS_BUCKET=${RUSTFS_BUCKET}

LLM_BASE_URL=${LLM_BASE_URL}
LLM_API_KEY=${LLM_API_KEY}
LLM_MODEL=${LLM_MODEL}

EMBEDDING_BASE_URL=${EMB_BASE_URL}
EMBEDDING_API_KEY=${EMB_API_KEY}
EMBEDDING_MODEL=${EMB_MODEL}
EMBEDDING_DIM=1024

OCR_ENABLED=true
OCR_LANG=ch
OCR_MIN_TEXT_CHARS=20

AUTH_ENABLED=true
AUTH_DB_PATH=./data/auth.db
AUTH_TOKEN_EXPIRY_HOURS=168

WATCH_DIRS=${WATCH_DIR}
WATCH_INTERVAL_SECONDS=30
WATCH_STABLE_SECONDS=5
EOF
    ok ".env 已生成（AUTH 开启；入库目录 $WATCH_DIR）"
fi

# ---------- 6. 初始化桶 ----------

log "初始化对象存储桶"
# 知识服务在宿主机跑，走本机回环访问 RustFS 即可
if ! "$KN/.venv/bin/python" - <<'EOF'
import os, boto3
from botocore.config import Config
client = boto3.client("s3", endpoint_url="http://127.0.0.1:9000",
                      aws_access_key_id=os.environ["RUSTFS_ACCESS_KEY"],
                      aws_secret_access_key=os.environ["RUSTFS_SECRET_KEY"],
                      config=Config(connect_timeout=5, read_timeout=10, retries={"max_attempts": 1}),
                      region_name="us-east-1")
bucket = os.environ.get("RUSTFS_BUCKET", "easyshare")
try:
    client.head_bucket(Bucket=bucket); print(f"    [OK] 桶 {bucket}：exists")
except Exception:
    client.create_bucket(Bucket=bucket); print(f"    [OK] 桶 {bucket}：created")
EOF
then
    fail "桶初始化失败（检查 rustfs.env 凭据 / RustFS 是否 healthy）"; exit 1
fi

# ---------- 7. systemd 服务 ----------

log "注册知识服务 systemd 单元"
cp "$SCRIPT_DIR/systemd/easyshare-knowledge.service" /etc/systemd/system/
systemctl daemon-reload
systemctl enable easyshare-knowledge >/dev/null 2>&1
systemctl restart easyshare-knowledge
if wait_http "http://127.0.0.1:${PORT}/health" 40 '"ok"'; then
    ok "服务已启动并通过健康检查（日志：journalctl -u easyshare-knowledge -f）"
else
    fail "健康检查未过，最近日志："
    journalctl -u easyshare-knowledge -n 20 --no-pager || true
    exit 1
fi

# ---------- 8. 账号 ----------

log "初始化账号"
BASE="http://127.0.0.1:${PORT}"
if [[ -z "$ADMIN_PASSWORD" ]]; then
    [[ $NONINTERACTIVE -eq 1 ]] && { fail "非交互模式必须传 --admin-password"; exit 1; }
    ADMIN_PASSWORD=$(ask "管理员（admin）口令" "")
    [[ -z "$ADMIN_PASSWORD" ]] && { fail "口令不能为空"; exit 1; }
fi
BOOT_CODE=$(curl -s -o /tmp/es-bootstrap.json -w '%{http_code}' -X POST "$BASE/auth/bootstrap" \
    -H 'Content-Type: application/json' -d "{\"username\":\"admin\",\"password\":\"$ADMIN_PASSWORD\"}" || echo 000)
if [[ "$BOOT_CODE" == "200" ]]; then
    ok "管理员已创建：admin"
elif [[ "$BOOT_CODE" == "409" ]]; then
    LOGIN=$(curl -s -X POST "$BASE/auth/login" -H 'Content-Type: application/json' \
        -d "{\"username\":\"admin\",\"password\":\"$ADMIN_PASSWORD\"}") || LOGIN=""
    [[ "$LOGIN" == *'"token"'* ]] && ok "管理员已存在，口令验证通过" || { fail "管理员已存在且口令不匹配"; exit 1; }
else
    fail "bootstrap 异常（HTTP $BOOT_CODE）：$(cat /tmp/es-bootstrap.json 2>/dev/null)"; exit 1
fi
TOKEN=$(curl -s -X POST "$BASE/auth/login" -H 'Content-Type: application/json' \
    -d "{\"username\":\"admin\",\"password\":\"$ADMIN_PASSWORD\"}" | python3 -c 'import json,sys;print(json.load(sys.stdin)["token"])')

NEW_ACCOUNTS=""
RAW=$(ask "批量创建同事账号（用户名逗号分隔；留空跳过）" "")
for name in $(echo "$RAW" | sed 's/[，,;；]/ /g'); do
    [[ -z "${name// /}" ]] && continue
    name=$(echo "$name" | xargs)
    PWD=$(rand_password)
    CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/auth/users" \
        -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
        -d "{\"username\":\"$name\",\"password\":\"$PWD\"}" || echo 000)
    if [[ "$CODE" == "200" || "$CODE" == "201" ]]; then
        ok "同事账号：$name / 初始口令 $PWD"
        NEW_ACCOUNTS="${NEW_ACCOUNTS}同事：${name} / 初始口令 ${PWD}\n"
    else
        fail "创建 $name 失败（HTTP $CODE，可能已存在）"
    fi
done

# ---------- 9. 防火墙 ----------

log "防火墙放行（同事访问必需：${PORT}/8090/9000）"
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
    ufw allow "${PORT}/tcp" >/dev/null && ufw allow 8090/tcp >/dev/null && ufw allow 9000/tcp >/dev/null
    ok "ufw 已放行 ${PORT} / 8090 / 9000（9001 管理控制台按需：ufw allow 9001/tcp）"
elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state 2>/dev/null | grep -q running; then
    firewall-cmd --permanent --add-port="${PORT}/tcp" --add-port=8090/tcp --add-port=9000/tcp >/dev/null
    firewall-cmd --reload >/dev/null
    ok "firewalld 已放行 ${PORT} / 8090 / 9000"
else
    echo "    未检测到 ufw/firewalld。若同事访问不通，检查云安全组/iptables 放行 ${PORT} 8090 9000"
fi

# ---------- 10. 使用指引 ----------

log "生成「同事使用指引」"
LAN_IP=$(lan_ip)
GUIDE="$ROOT/同事使用指引.txt"
printf '%b' \
"# 公司知识库使用指引（发给同事的一页纸）\n\n" \
"## 怎么提问\n" \
"浏览器打开 http://${LAN_IP}:${PORT}/lab ，用你的账号登录，底部「知识问答」输入问题。\n" \
"回答带引用来源，点引用可以看原文出处。\n\n" \
"## 怎么贡献文件\n" \
"把文件放进共享目录：$WATCH_DIR\n" \
"支持 Word / PDF / Excel / PPT / TXT / Markdown / 图片，约 1 分钟后自动入库可被检索；\n" \
"文件更新后重新放入即可，答案会引用最新版本。\n\n" \
"## 桌面客户端\n" \
"安装 EasyShare 客户端后登录（服务器 http://${LAN_IP}:8090）；\n" \
"「知识」页首次使用填服务器地址 http://${LAN_IP}:${PORT} 并登录。\n\n" \
"## 注意\n" \
"- 从入库文件夹删除文件不会从知识库删除（需要管理员处理）；\n" \
"- 敏感文件先问 IT 再放。\n\n" \
"## 本次部署的账号（分发完建议删除本文件）\n" \
"管理员：admin\n" "${NEW_ACCOUNTS:-}" > "$GUIDE"
ok "指引已生成：$GUIDE"

echo
echo "==================== 基础设施 + 知识服务部署完成 ===================="
echo " 同事访问地址 ：http://${LAN_IP}:${PORT}/lab"
echo " 质量驾驶舱   ：http://${LAN_IP}:${PORT}/lab/cockpit"
echo " RustFS 控制台：http://${LAN_IP}:9001"
echo " 下一步（控制面）：在开发机跑 scripts/ship-control-plane.ps1 把 RuoYi jar 送上来"
echo " 更新知识服务 ：bash $REPO/deploy/server-linux/update.sh"
echo "====================================================================="
