"""端到端验证空间配额与共享授权。

验的是「配额真的拦得住」这条链，不是接口能不能调通：
    设配额 → 读回 → 传小文件放行 → 传超额文件被拒 → 未授权账号看不到共享空间

需要控制面在 8090 上跑着。用法：
    python scripts/verify-space-quota.py
"""

import io
import json
import sys
import urllib.error
import urllib.request

BASE = "http://localhost:8090"
CLIENT_ID = "e5cd7e4891bf95d1d19206ce24a7b32e"
GB = 1024 * 1024 * 1024

out = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8", errors="replace")
passed, failed = [], []


def call(method, path, token=None, body=None):
    """调控制面，返回 (code, data, msg)。RuoYi 的业务错误是 HTTP 200 + body 里的 code。"""
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    req.add_header("clientid", CLIENT_ID)
    if token:
        req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            payload = json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as e:
        payload = json.loads(e.read().decode("utf-8"))
    return payload.get("code"), payload.get("data"), payload.get("msg")


def check(name, ok, detail=""):
    (passed if ok else failed).append(name)
    out.write(f"  {'PASS' if ok else 'FAIL'}  {name}{(' — ' + detail) if detail else ''}\n")
    out.flush()


def login(username, password):
    code, data, msg = call("POST", "/auth/login", body={
        "username": username, "password": password,
        "clientId": CLIENT_ID, "grantType": "password",
    })
    if code != 200:
        return None, msg
    return (data or {}).get("access_token"), msg


def main():
    out.write("=== 空间配额与共享授权端到端验证 ===\n\n")

    token, msg = login("admin", "admin123")
    if not token:
        out.write(f"管理员登录失败：{msg}\n")
        return 1
    out.write("管理员已登录\n\n")

    code, spaces, _ = call("GET", "/easyshare/space/mine", token)
    me = next((s for s in (spaces or []) if s["spaceType"] == "personal"), None)
    user_id = me["ownerId"] if me else None
    if not user_id:
        out.write("拿不到当前账号的个人空间，无法继续\n")
        return 1

    out.write("--- 配额设定与读回 ---\n")
    code, _, msg = call("POST", "/easyshare/space/admin/personal-quota", token,
                        {"userId": user_id, "quotaBytes": 2 * GB})
    check("设定个人配额 2 GB", code == 200, msg if code != 200 else "")

    code, spaces, _ = call("GET", "/easyshare/space/mine", token)
    me = next((s for s in (spaces or []) if s["spaceType"] == "personal"), None)
    check("读回配额为 2 GB", me and me["quotaBytes"] == 2 * GB,
          f"实得 {me['quotaBytes'] if me else '无'}")
    check("已用量为 0（桶是空的）", me and me["usedBytes"] == 0,
          f"实得 {me['usedBytes'] if me else '无'}")

    out.write("\n--- 配额判定 ---\n")
    code, data, msg = call("POST", "/easyshare/drive/presign-put", token,
                           {"path": "verify/small.bin", "space": "personal", "size": 1024})
    check("小文件（1 KB）放行", code == 200 and data and data.get("url"), msg if code != 200 else "")

    code, data, msg = call("POST", "/easyshare/drive/presign-put", token,
                           {"path": "verify/huge.bin", "space": "personal", "size": 3 * GB})
    check("超额文件（3 GB > 2 GB）被拒", code != 200, f"code={code} msg={msg}")

    out.write("\n--- 收回配额 ---\n")
    code, _, msg = call("POST", "/easyshare/space/admin/personal-quota", token,
                        {"userId": user_id, "quotaBytes": 0})
    check("配额收回为 0", code == 200, msg if code != 200 else "")
    code, data, msg = call("POST", "/easyshare/drive/presign-put", token,
                           {"path": "verify/x.bin", "space": "personal", "size": 1})
    check("待开空间拒绝上传", code != 200, f"code={code} msg={msg}")

    out.write("\n--- 共享空间授权 ---\n")
    # 自己先建立已知起点：不能假设库是干净的。
    # 之前这里直接断言「未授权」，在跑过一次或手工授权过之后就会假失败——
    # 一个只在干净库上才通过的验证脚本，日后只会误导人。
    call("POST", "/easyshare/space/admin/shared-grant", token,
         {"userId": user_id, "permission": ""})
    call("POST", "/easyshare/space/admin/shared-quota", token, {"quotaBytes": 0})

    # 未授权时，共享空间不应出现在 /mine，也不该能写
    code, spaces, _ = call("GET", "/easyshare/space/mine", token)
    has_shared = any(s["spaceType"] == "shared" for s in (spaces or []))
    check("未授权时 /mine 不含共享空间", not has_shared)

    code, data, msg = call("POST", "/easyshare/drive/presign-put", token,
                           {"path": "t.bin", "space": "shared", "size": 1})
    check("未授权拒绝写共享空间", code != 200, f"code={code} msg={msg}")

    code, _, msg = call("POST", "/easyshare/space/admin/shared-quota", token, {"quotaBytes": 5 * GB})
    check("设定共享容量 5 GB", code == 200, msg if code != 200 else "")

    code, _, msg = call("POST", "/easyshare/space/admin/shared-grant", token,
                        {"userId": user_id, "permission": "read"})
    check("授予只读", code == 200, msg if code != 200 else "")

    code, spaces, msg = call("GET", "/easyshare/space/mine", token)
    # 先钉住 code：否则接口 500 会表现成「没有共享空间」，把服务端错误伪装成数据缺失
    check("/mine 正常返回", code == 200, f"code={code} msg={msg}")
    shared = next((s for s in (spaces or []) if s["spaceType"] == "shared"), None)
    check("授权后 /mine 出现共享空间", shared is not None)
    check("权限为只读", shared and shared["permission"] == "read",
          f"实得 {shared['permission'] if shared else '无'}")

    code, data, msg = call("POST", "/easyshare/drive/presign-put", token,
                           {"path": "t.bin", "space": "shared", "size": 1})
    check("只读权限拒绝写入", code != 200, f"code={code} msg={msg}")

    code, _, msg = call("POST", "/easyshare/space/admin/shared-grant", token,
                        {"userId": user_id, "permission": "write"})
    check("升级为读写", code == 200, msg if code != 200 else "")

    code, data, msg = call("POST", "/easyshare/drive/presign-put", token,
                           {"path": "t.bin", "space": "shared", "size": 1024})
    check("读写权限放行写入", code == 200 and data and data.get("url"), msg if code != 200 else "")

    code, data, msg = call("POST", "/easyshare/drive/presign-put", token,
                           {"path": "t.bin", "space": "shared", "size": 6 * GB})
    check("共享空间超额被拒（6 GB > 5 GB）", code != 200, f"code={code} msg={msg}")

    out.write("\n--- 撤销授权 ---\n")
    code, _, msg = call("POST", "/easyshare/space/admin/shared-grant", token,
                        {"userId": user_id, "permission": ""})
    check("撤销共享授权", code == 200, msg if code != 200 else "")
    code, spaces, _ = call("GET", "/easyshare/space/mine", token)
    check("撤销后 /mine 不再含共享空间",
          not any(s["spaceType"] == "shared" for s in (spaces or [])))

    out.write("\n--- 容量总览与池上限 ---\n")
    code, cap, msg = call("GET", "/easyshare/space/admin/capacity", token)
    check("读容量总览", code == 200 and cap is not None, msg if code != 200 else "")
    if cap:
        out.write(f"        启用={cap['enabled']} 物理可用={cap['usableBytes'] / GB:.1f} GB "
                  f"池={cap['poolBytes'] / GB:.1f} GB 已承诺={cap['committedBytes'] / GB:.1f} GB "
                  f"实际已用={cap['usedBytes']} B\n")
        check("池上限已启用（配了探测路径）", cap["enabled"] is True,
              "capacity-path 未生效，池上限不会拦任何东西")
        if cap["enabled"]:
            check("探测到的物理可用为正数", cap["usableBytes"] > 0,
                  f"实得 {cap['usableBytes']}")
            # 这条是本功能的核心：承诺超过物理时必须能看出来
            over = cap["committedBytes"] > cap["poolBytes"]
            out.write(f"        超配={'是' if over else '否'}"
                      f"（承诺 {cap['committedBytes'] / GB:.1f} GB vs 池 {cap['poolBytes'] / GB:.1f} GB）\n")

            # 池上限要能被单独验证，就必须先排除「配额不足」这条更早的拒绝路径：
            # 给一个大到不会先撞配额的额度，这样被拒只可能是因为池。
            # 前面的步骤把配额收回成 0 了，不重设的话这里撞到的是「尚未分配空间」。
            call("POST", "/easyshare/space/admin/personal-quota", token,
                 {"userId": user_id, "quotaBytes": -1})  # -1 = 不限
            huge = cap["poolBytes"] + 100 * GB
            code, _, msg = call("POST", "/easyshare/drive/presign-put", token,
                                {"path": "verify/pool.bin", "space": "personal", "size": huge})
            check("超过物理容量被拒", code != 200, f"code={code}")
            # 两种「满」必须分得清：用户删自己的文件能解决前者，解决不了后者
            check("错误信息指向服务器存储（而非用户配额）",
                  msg is not None and "服务器存储不足" in msg,
                  f"实得：{msg}")
            # 「不限」配额也要受池约束，否则不限就等于绕过池上限
            check("「不限」配额同样受池上限约束",
                  msg is not None and "服务器存储不足" in msg,
                  "不限配额绕过了池判定")
            call("POST", "/easyshare/space/admin/personal-quota", token,
                 {"userId": user_id, "quotaBytes": 0})

    out.write("\n--- 越权路径 ---\n")
    for evil in ["../other/x.bin", "/abs.bin", "a/../../escape.bin"]:
        code, _, msg = call("POST", "/easyshare/drive/presign-put", token,
                            {"path": evil, "space": "personal", "size": 1})
        check(f"拒绝越权路径 {evil}", code != 200, f"code={code}")

    code, _, msg = call("POST", "/easyshare/space/admin/personal-quota", token,
                        {"userId": "1 OR 1=1", "quotaBytes": 1024})
    check("拒绝非数字账号标识", code != 200, f"code={code} msg={msg}")

    out.write(f"\n=== 通过 {len(passed)} 项，失败 {len(failed)} 项 ===\n")
    if failed:
        out.write("失败项：\n")
        for name in failed:
            out.write(f"  - {name}\n")
    out.flush()
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
