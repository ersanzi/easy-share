"""控制面是企业身份及权限的唯一权威，每次请求回查，失败关闭访问。

本地用户档案仅供兼容历史展示，不得把本地角色提升为控制面权限。
独立部署未配置 SSO 时仍可使用本地账号；企业模式不接受本地令牌。
"""
from __future__ import annotations

import json
import logging
import secrets
import datetime
import urllib.error

from fastapi import HTTPException
import urllib.request

from app.auth.store import UserStore

_FEATURES = ("knowledge", "meeting", "drive", "shared", "plugin")
_log = logging.getLogger(__name__)



class ControlPlaneSSO:
    """回查控制面身份与权限；不复用跨请求的令牌、角色或功能授权。"""

    def __init__(self, base_url: str, *, client_id: str, users: UserStore,
                 timeout_seconds: float = 5.0) -> None:
        self.base_url = base_url.rstrip("/")
        self.client_id = client_id
        self.users = users
        self.timeout_seconds = timeout_seconds

    def login(self, username: str, password: str) -> dict:
        """企业入口只向配置的控制面登录，不在本地校验口令或签发备用身份。"""
        request = urllib.request.Request(
            f"{self.base_url}/auth/login",
            data=json.dumps({"username": username, "password": password,
                             "clientId": self.client_id, "grantType": "password"}).encode(),
            headers={"Content-Type": "application/json", "clientid": self.client_id}, method="POST")
        try:
            with urllib.request.urlopen(request, timeout=self.timeout_seconds) as response:
                payload = json.loads(response.read())
        except urllib.error.HTTPError as exc:
            if exc.code in (400, 401, 403):
                raise HTTPException(401, "企业登录被拒绝；请确认账号口令。若企业要求验证码或加密登录，请使用企业控制面的受支持登录入口") from exc
            raise HTTPException(503, "企业账号服务暂不可用，请稍后重试") from exc
        except (OSError, ValueError) as exc:
            raise HTTPException(503, "企业账号服务暂不可用，请稍后重试") from exc
        if not isinstance(payload, dict) or payload.get("code") != 200:
            raise HTTPException(401, "企业登录被拒绝；请确认账号口令及企业的验证码/加密登录要求")
        data = payload.get("data")
        token = data.get("access_token") if isinstance(data, dict) else None
        if not isinstance(token, str) or not token.strip():
            raise HTTPException(502, "企业账号服务没有返回有效会话")
        user = self.resolve(token)
        if user is None:
            raise HTTPException(401, "企业会话无法验证，请重新登录")
        # 有效期只作展示；后续每次请求仍向控制面验票。
        expiry = ""
        seconds = data.get("expire_in")
        if isinstance(seconds, (int, float)) and 0 < seconds < 315360000:
            expiry = (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(seconds=seconds)).isoformat()
        return {"token": token, "username": user["username"], "role": user["role"],
                "dept": user.get("dept", ""), "expires_at": expiry}

    def logout(self, token: str) -> None:
        """显式退出只撤销所提交的控制面会话；失败不得伪装成已退出。"""
        request = urllib.request.Request(f"{self.base_url}/auth/logout", data=b"{}",
            headers={"Authorization": f"Bearer {token}", "clientid": self.client_id,
                     "Content-Type": "application/json"}, method="POST")
        try:
            with urllib.request.urlopen(request, timeout=self.timeout_seconds) as response:
                payload = json.loads(response.read())
        except urllib.error.HTTPError as exc:
            if exc.code == 401:
                return  # 控制面确认令牌已经失效，幂等退出。
            raise HTTPException(503, "企业会话注销未完成，请稍后重试") from exc
        except (OSError, ValueError) as exc:
            raise HTTPException(503, "企业会话注销未完成，请稍后重试") from exc
        if not isinstance(payload, dict) or payload.get("code") not in (200, 401):
            raise HTTPException(503, "企业会话注销未完成，请稍后重试")

    def resolve(self, token: str) -> dict | None:
        if not token:
            return None
        info = self._introspect(token)
        if info is None:
            return None
        user = self._ensure_local_user(info)
        flags = info.get("features")
        role = "admin" if info.get("is_admin") is True else "member"
        # 所有消费者（会议导出、账号管理、知识读取）都只看到当前权威角色。
        return {**user, "role": role, "authorization_role": role,
                "features": {name: isinstance(flags, dict) and flags.get(name) is True for name in _FEATURES},
                "authority": "ruoyi", "subject_id": info.get("subject_id"), "dept": info.get("dept") or ""}

    def _introspect(self, token: str) -> dict | None:
        """任何无法确认身份的响应都拒绝，不能继承上一次成功结果。"""
        request = urllib.request.Request(f"{self.base_url}/system/user/getInfo")
        request.add_header("Authorization", f"Bearer {token}")
        request.add_header("clientid", self.client_id)
        try:
            with urllib.request.urlopen(request, timeout=self.timeout_seconds) as resp:
                payload = json.loads(resp.read())
        except (OSError, ValueError):
            return None
        if not isinstance(payload, dict) or payload.get("code") != 200:
            return None
        data = payload.get("data")
        if not isinstance(data, dict) or not isinstance(data.get("user"), dict):
            return None
        user = data["user"]
        username = str(user.get("userName") or "").strip()
        subject_id = str(user.get("userId") or "").strip()
        if not username or not subject_id or user.get("status") not in (None, "0", 0):
            return None
        permission = self._permission_snapshot(token, subject_id)
        roles = data.get("roles")
        permissions = data.get("permissions")
        return {
            "username": username,
            "subject_id": subject_id,
            "is_admin": permission.get("adminScope") == "all" and (
                (isinstance(roles, list) and "superadmin" in roles)
                or (isinstance(permissions, list) and "*:*:*" in permissions)),
            "dept": str(user.get("deptId") or "").strip(),
            "features": {name: permission.get(name) is True for name in _FEATURES},
        }

    def _permission_snapshot(self, token: str, subject_id: str) -> dict:
        """功能与管理权限必须来自同一次实时快照；身份角色不能覆盖撤权。"""
        request = urllib.request.Request(f"{self.base_url}/easyshare/permission/mine")
        request.add_header("Authorization", f"Bearer {token}")
        request.add_header("clientid", self.client_id)
        try:
            with urllib.request.urlopen(request, timeout=self.timeout_seconds) as resp:
                payload = json.loads(resp.read())
        except (OSError, ValueError):
            _log.warning("控制面功能权限不可用，本次拒绝业务访问")
            return {}
        if not isinstance(payload, dict) or payload.get("code") != 200 or not isinstance(payload.get("data"), dict):
            _log.warning("控制面功能权限响应无效，本次拒绝业务访问")
            return {}
        permission = payload["data"]
        if permission.get("userId") is not None and str(permission["userId"]) != subject_id:
            _log.warning("控制面权限与身份不一致，本次拒绝业务访问")
            return {}
        return permission

    def _ensure_local_user(self, info: dict) -> dict:
        username, dept = info["username"], info["dept"]
        existing = self.users.get_user(username)
        if existing is None:
            try:
                self.users.create_user(username, secrets.token_urlsafe(32),
                                       role="admin" if info["is_admin"] else "member", dept=dept)
            except ValueError:
                pass  # 并发请求已完成建档，读取最终记录。
        elif existing["dept"] != dept:
            self.users.set_user_dept(username, dept)
        return self.users.get_user(username) or {"username": username, "role": "member", "dept": dept}
