"""用户与令牌存储：SQLite + PBKDF2 口令哈希，零新依赖。

与 job_store/query_log 同模式（per-call 连接 + 锁）。接口按可拆设计：
未来控制面平移到 Java/PostgreSQL 时，本存储可整体替换。
"""
from __future__ import annotations

import hashlib
import hmac
import secrets
import sqlite3
import threading
from datetime import datetime, timedelta, timezone
from pathlib import Path

_PBKDF2_ITERATIONS = 100_000


class UserStore:
    """用户库：用户表 + 令牌表，追加式令牌（重启存活，带过期）。"""

    def __init__(self, path: str, *, token_expiry_hours: int = 24 * 7) -> None:
        self.path = path
        self.token_expiry_hours = token_expiry_hours
        self.lock = threading.RLock()
        if path != ":memory:":
            Path(path).parent.mkdir(parents=True, exist_ok=True)
        self._init_db()

    def _connect(self) -> sqlite3.Connection:
        conn = sqlite3.connect(self.path, check_same_thread=False)
        conn.row_factory = sqlite3.Row
        return conn

    def _init_db(self) -> None:
        with self.lock, self._connect() as conn:
            conn.execute("""
                CREATE TABLE IF NOT EXISTS users (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    username TEXT NOT NULL UNIQUE,
                    password_hash TEXT NOT NULL,
                    salt TEXT NOT NULL,
                    role TEXT NOT NULL DEFAULT 'member',
                    created_at TEXT NOT NULL
                )
            """)
            conn.execute("""
                CREATE TABLE IF NOT EXISTS tokens (
                    token TEXT PRIMARY KEY,
                    username TEXT NOT NULL,
                    expires_at TEXT NOT NULL
                )
            """)
            conn.commit()

    # ---------- 用户 ----------

    def count_users(self) -> int:
        with self.lock, self._connect() as conn:
            return conn.execute("SELECT COUNT(*) FROM users").fetchone()[0]

    def create_user(self, username: str, password: str, role: str = "member") -> dict:
        if role not in ("member", "admin"):
            raise ValueError("role 只能是 member 或 admin")
        if not username.strip() or not password:
            raise ValueError("用户名与口令不能为空")
        salt = secrets.token_hex(16)
        password_hash = hashlib.pbkdf2_hmac(
            "sha256", password.encode("utf-8"), salt.encode("ascii"), _PBKDF2_ITERATIONS
        ).hex()
        with self.lock, self._connect() as conn:
            try:
                conn.execute(
                    "INSERT INTO users (username, password_hash, salt, role, created_at) VALUES (?, ?, ?, ?, ?)",
                    (username.strip(), password_hash, salt, role, datetime.now(timezone.utc).isoformat()),
                )
                conn.commit()
            except sqlite3.IntegrityError as exc:
                raise ValueError(f"用户已存在：{username}") from exc
        return {"username": username.strip(), "role": role}

    def verify(self, username: str, password: str) -> dict | None:
        with self.lock, self._connect() as conn:
            row = conn.execute(
                "SELECT username, password_hash, salt, role FROM users WHERE username = ?",
                (username.strip(),),
            ).fetchone()
        if row is None:
            return None
        candidate = hashlib.pbkdf2_hmac(
            "sha256", password.encode("utf-8"), row["salt"].encode("ascii"), _PBKDF2_ITERATIONS
        ).hex()
        if not hmac.compare_digest(candidate, row["password_hash"]):
            return None
        return {"username": row["username"], "role": row["role"]}

    def get_user(self, username: str) -> dict | None:
        with self.lock, self._connect() as conn:
            row = conn.execute(
                "SELECT username, role FROM users WHERE username = ?", (username.strip(),)
            ).fetchone()
        return {"username": row["username"], "role": row["role"]} if row else None

    def list_users(self) -> list[dict]:
        with self.lock, self._connect() as conn:
            rows = conn.execute("SELECT username, role, created_at FROM users ORDER BY id").fetchall()
        return [{"username": r["username"], "role": r["role"], "created_at": r["created_at"]} for r in rows]

    # ---------- 令牌 ----------

    def issue_token(self, username: str) -> tuple[str, str]:
        token = secrets.token_urlsafe(32)
        expires_at = (datetime.now(timezone.utc) + timedelta(hours=self.token_expiry_hours)).isoformat()
        with self.lock, self._connect() as conn:
            conn.execute("DELETE FROM tokens WHERE expires_at < ?", (datetime.now(timezone.utc).isoformat(),))
            conn.execute(
                "INSERT INTO tokens (token, username, expires_at) VALUES (?, ?, ?)",
                (token, username.strip(), expires_at),
            )
            conn.commit()
        return token, expires_at

    def resolve_token(self, token: str) -> dict | None:
        if not token:
            return None
        with self.lock, self._connect() as conn:
            row = conn.execute(
                """SELECT t.username AS token_user, t.expires_at, u.role AS role
                   FROM tokens t JOIN users u ON u.username = t.username
                   WHERE t.token = ?""",
                (token,),
            ).fetchone()
        if row is None:
            return None
        if row["expires_at"] < datetime.now(timezone.utc).isoformat():
            return None
        return {"username": row["token_user"], "role": row["role"]}
