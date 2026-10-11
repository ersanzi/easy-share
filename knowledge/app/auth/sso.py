"""控制面 SSO 信任（introspection 模式）：知识服务认控制面（RuoYi）登录令牌。

RuoYi-Vue-Plus 的令牌由 Sa-Token + Redis 管理（不透明随机串，非自包含 JWT），
本地无法验签，信任方式是回查控制面 `GET /system/user/getInfo`：应答 200 即
令牌有效，顺带取回用户名 / 部门 / 管理员标志——请求头与响应形状与 Go 客户端
（internal/account/client.go）同一套，已由桌面端在生产验证。introspection
结果缓存 60 秒，避免每个请求一跳控制面。

映射规则（权属划分，宁可少给不可多给）：
- 控制面新用户 → JIT 建本地档案：role 按控制面 superadmin / *:*:* 判定，
  dept 取控制面 deptId（与部门授权/文档可见性同源的部门 ID），口令为随机串
  （本地登录通道实际不可用，身份完全来自控制面）；
- 本地已有同名人 → 保留本地 role（管理页显式管理的语义不被 SSO 覆盖），
  dept 跟随控制面同步（组织架构的权威在控制面，见部门授权片 1 的 sys_dept 投影）。

本地账号/口令登录完全不受影响：中间件先查本地令牌，未命中才走本模块——
本地令牌与控制面令牌是不同随机空间，不存在歧义。
"""
from __future__ import annotations

import json
import secrets
import threading
import time
import urllib.request

from app.auth.store import UserStore

_CACHE_TTL_SECONDS = 60.0
_CACHE_MAX_ENTRIES = 512


class ControlPlaneSSO:
    """回查控制面验票并映射为知识服务本地用户（含 JIT 建档与缓存）。"""

    def __init__(
        self,
        base_url: str,
        *,
        client_id: str,
        users: UserStore,
        timeout_seconds: float = 5.0,
    ) -> None:
        self.base_url = base_url.rstrip("/")
        self.client_id = client_id
        self.users = users
        self.timeout_seconds = timeout_seconds
        self.lock = threading.Lock()
        self._cache: dict[str, tuple[dict, float]] = {}

    def resolve(self, token: str) -> dict | None:
        """令牌 → 本地用户 dict；令牌无效或控制面不可达返回 None（回退原行为）。"""
        if not token:
            return None
        cached = self._cache_get(token)
        if cached is not None:
            return cached
        info = self._introspect(token)
        if info is None:
            return None
        user = self._ensure_local_user(info)
        self._cache_put(token, user)
        return user

    def _introspect(self, token: str) -> dict | None:
        """回查控制面换身份；任何失败（网络/HTTP 错/业务非 0）返回 None。"""
        request = urllib.request.Request(f"{self.base_url}/system/user/getInfo")
        request.add_header("Authorization", f"Bearer {token}")
        request.add_header("clientid", self.client_id)
        try:
            with urllib.request.urlopen(request, timeout=self.timeout_seconds) as resp:
                payload = json.loads(resp.read())
        except (OSError, ValueError):
            # URLError/HTTPError/超时都是 OSError；响应非 JSON 是 ValueError
            return None
        if payload.get("code") != 200:
            return None
        data = payload.get("data") or {}
        user = data.get("user") or {}
        username = str(user.get("userName") or "").strip()
        if not username:
            return None
        roles = data.get("roles") or []
        permissions = data.get("permissions") or []
        return {
            "username": username,
            "is_admin": "superadmin" in roles or "*:*:*" in permissions,
            "dept": str(user.get("deptId") or "").strip(),
        }

    def _ensure_local_user(self, info: dict) -> dict:
        username, dept = info["username"], info["dept"]
        existing = self.users.get_user(username)
        if existing is None:
            role = "admin" if info["is_admin"] else "member"
            try:
                # 口令随机串：JIT 档案只承载身份，本地口令登录通道实际不可用
                self.users.create_user(username, secrets.token_urlsafe(32), role=role, dept=dept)
            except ValueError:
                pass  # 并发竞态下已被建档，直接读取
        elif dept and existing["dept"] != dept:
            self.users.set_user_dept(username, dept)
        return self.users.get_user(username) or {"username": username, "role": "member", "dept": dept}

    def _cache_get(self, token: str) -> dict | None:
        now = time.monotonic()
        with self.lock:
            entry = self._cache.get(token)
            if entry is None:
                return None
            if entry[1] <= now:
                self._cache.pop(token, None)
                return None
            return entry[0]

    def _cache_put(self, token: str, user: dict) -> None:
        now = time.monotonic()
        with self.lock:
            if len(self._cache) >= _CACHE_MAX_ENTRIES:
                expired = [key for key, (_, until) in self._cache.items() if until <= now]
                for key in expired:
                    self._cache.pop(key, None)
                if len(self._cache) >= _CACHE_MAX_ENTRIES:
                    self._cache.clear()  # 兜底：观察期规模到不了这里
            self._cache[token] = (user, now + _CACHE_TTL_SECONDS)
