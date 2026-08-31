#!/usr/bin/env bash
# P2 存储隔离验收：证明「A 传的文件只在 A 空间，B 看不到也拿不到」。
#
# 覆盖 ADR-0007 不变量 1（客户端不持 RustFS 凭据，只拿预签名 URL）
# 与不变量 2（对象键带用户前缀，服务端强制校验归属）。
#
# 用法：bash scripts/verify-drive-isolation.sh [控制面地址]
# 前置：控制面已启动（含 easyshare-drive 模块），RustFS 可达。
set -uo pipefail

BASE="${1:-http://localhost:8090}"
CLIENT_ID='e5cd7e4891bf95d1d19206ce24a7b32e'
PASS=0
FAIL=0

# 断言：期望值与实际值一致
check() {
    local name="$1" expected="$2" actual="$3"
    if [ "$expected" = "$actual" ]; then
        echo "  [PASS] $name"
        PASS=$((PASS + 1))
    else
        echo "  [FAIL] $name — 期望 $expected，实际 $actual"
        FAIL=$((FAIL + 1))
    fi
}

# 登录取 JWT。令牌只留在变量里，不打印。
login() {
    curl -s -X POST "$BASE/auth/login" \
        -H 'Content-Type: application/json' -H "clientid: $CLIENT_ID" \
        -d "{\"clientId\":\"$CLIENT_ID\",\"grantType\":\"password\",\"username\":\"$1\",\"password\":\"$2\"}" |
        python -c 'import json,sys; d=json.load(sys.stdin); print((d.get("data") or {}).get("access_token",""))'
}

# 带 JWT 调控制面，只输出响应体。
# 注意不用 curl -w 拼状态码：本机 curl 不解析 -w 里的 \xNN 转义，会污染 JSON。
api() {
    local token="$1" method="$2" path="$3" body="${4:-}"
    local args=(-s -X "$method" "$BASE$path"
        -H "Authorization: Bearer $token" -H "clientid: $CLIENT_ID")
    if [ -n "$body" ]; then
        args+=(-H 'Content-Type: application/json' -d "$body")
    fi
    curl "${args[@]}"
}

# 从 RuoYi 统一响应体里取字段（默认取 code）
resp_field() {
    python -c 'import json,sys
field=sys.argv[1] if len(sys.argv)>1 else "code"
try:
    d=json.load(sys.stdin)
except Exception:
    print("parse_error"); raise SystemExit
if field=="code":
    print(d.get("code",""))
else:
    print((d.get("data") or {}).get(field,""))' "${1:-code}"
}

echo "== P2 存储隔离验收 =="
echo "控制面：$BASE"

TOKEN_A=$(login admin admin123)
TOKEN_B=$(login test 666666)
if [ -z "$TOKEN_A" ] || [ -z "$TOKEN_B" ]; then
    echo "登录失败：A=$([ -n "$TOKEN_A" ] && echo ok || echo empty) B=$([ -n "$TOKEN_B" ] && echo ok || echo empty)"
    exit 1
fi
echo "登录：A(admin) 与 B(test) 均已取得 JWT"

STAMP=$(date +%s)
REL="iso-${STAMP}.txt"
CONTENT="only-A-should-see-this-${STAMP}"

echo
echo "-- 1. A 经控制面换预签名 URL 并直传 RustFS --"
PUT_JSON=$(api "$TOKEN_A" POST /easyshare/drive/presign-put "{\"path\":\"$REL\"}")
PUT_URL=$(printf '%s' "$PUT_JSON" | resp_field url)
if [ -z "$PUT_URL" ] || [ "$PUT_URL" = "parse_error" ]; then
    echo "  [FAIL] A 未取得上传 URL，响应 code=$(printf '%s' "$PUT_JSON" | resp_field code)"
    exit 1
fi
echo "  A 取得预签名 PUT URL（凭据未下发，URL 由控制面签发）"
UP_CODE=$(printf '%s' "$CONTENT" | curl -s -o /dev/null -w '%{http_code}' -X PUT --data-binary @- "$PUT_URL")
check "A 直传 RustFS 成功" 200 "$UP_CODE"

echo
echo "-- 2. A 能在自己空间看到该文件 --"
A_LIST=$(api "$TOKEN_A" GET /easyshare/drive/objects)
A_HAS=$(printf '%s' "$A_LIST" | python -c "import json,sys
data=json.load(sys.stdin).get('data') or []
print('yes' if any(o.get('path')=='$REL' for o in data) else 'no')")
check "A 的列表含该文件" yes "$A_HAS"

echo
echo "-- 3. B 的列表看不到 A 的文件（KI-3 隔离） --"
B_LIST=$(api "$TOKEN_B" GET /easyshare/drive/objects)
B_HAS=$(printf '%s' "$B_LIST" | python -c "import json,sys
data=json.load(sys.stdin).get('data') or []
print('yes' if any(o.get('path')=='$REL' for o in data) else 'no')")
check "B 的列表不含 A 的文件" no "$B_HAS"

echo
echo "-- 4. B 用同名相对路径拿不到 A 的内容（各自命名空间） --"
B_GET=$(api "$TOKEN_B" POST /easyshare/drive/presign-get "{\"path\":\"$REL\"}")
B_URL=$(printf '%s' "$B_GET" | resp_field url)
if [ -n "$B_URL" ] && [ "$B_URL" != "parse_error" ]; then
    B_FETCH=$(curl -s -o /dev/null -w '%{http_code}' "$B_URL")
    # B 拿到的是 users/{B}/ 下的键，该对象不存在 → 404
    check "B 按同名路径取到的是自己空间（404 而非 A 的内容）" 404 "$B_FETCH"
else
    check "B 取下载 URL 被拒" rejected rejected
fi

echo
echo "-- 5. B 用路径穿越构造 A 的键被拒（不变量 2） --"
for evil in "../$REL" "../../users/1761100000000000001/$REL" "/$REL"; do
    # 经 stdin 传路径：Git Bash(MSYS) 会把 argv 里以 / 开头的值改写成 Windows 路径，
    # 那样 "/x.txt" 会变成 "D:/Git/x.txt"，测不到前导斜杠这条分支。
    EVIL_BODY=$(printf '%s' "$evil" | python -c 'import json,sys; print(json.dumps({"path": sys.stdin.read()}))')
    EVIL_CODE=$(api "$TOKEN_B" POST /easyshare/drive/presign-get "$EVIL_BODY" | resp_field code)
    if [ "$EVIL_CODE" = "200" ]; then
        echo "  [FAIL] 穿越路径被接受：$evil"
        FAIL=$((FAIL + 1))
    else
        echo "  [PASS] 穿越路径被拒：$evil (code=$EVIL_CODE)"
        PASS=$((PASS + 1))
    fi
done

echo
echo "-- 6. 未登录不得访问 --"
# RuoYi 惯例：业务错误走 HTTP 200 + body 里的 code，故必须看 code 而非 HTTP 状态
ANON_CODE=$(curl -s "$BASE/easyshare/drive/objects" | resp_field code)
check "未登录访问列表被拒(401)" 401 "$ANON_CODE"

echo
echo "-- 清理：A 删除测试文件 --"
DEL_CODE=$(api "$TOKEN_A" DELETE /easyshare/drive/object "{\"path\":\"$REL\"}" | resp_field code)
check "A 删除自己的文件成功" 200 "$DEL_CODE"

echo
echo "== 结果：$PASS 通过，$FAIL 失败 =="
[ "$FAIL" -eq 0 ]
