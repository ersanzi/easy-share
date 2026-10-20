"""权限感知检索（2c）：按登录用户计算可见文档集合。

可见性规则（最小切片）：
- owner 为 None/缺失的文档是共享文档，所有人可见（存量数据天然是共享语义）；
- owner 非空的文档仅归属用户本人与 admin 可见；
- 未登录（本地 /lab、auth 关闭）不过滤，行为与权限切片之前完全一致。

检索侧的注入点只有一个：/query 在调 retriever 前把可见集与请求显式
doc_ids 求交集。交集为空时必须短路返回空结果——VectorStore.query 对
空列表不过滤（`if doc_ids:`），直接传入会把"无可见文档"放大成"可见全部"。
"""
from __future__ import annotations

from app.kb.store import VectorStore

# request.state.user 的形状：{"username": str, "role": str, "dept": str}（auth 中间件注入；
# dept 为部门 ID 字符串，片 2b 起随登录态下发，参与文档级可见性过滤）
ScopeUser = dict | None


def visible_doc_ids(store: VectorStore, user: ScopeUser) -> list[str] | None:
    """计算用户可见的 doc_id 列表；返回 None 表示不过滤（未登录/admin）。"""
    if user is None:
        return None
    if user.get("role") == "admin":
        return None
    username = user.get("username")
    if not username:
        return None
    owners = store.doc_owners()
    visible_depts_map = store.doc_visible_depts()
    dept = (user.get("dept") or "").strip()

    result: list[str] = []
    for doc_id, owner in owners.items():
        if owner is not None:
            # 私有文档：仅归属用户本人可见
            if owner == username:
                result.append(doc_id)
            continue
        # 共享文档：visible_depts 为空 = 全体可见；非空 = 部门命中才可见
        allowed_depts = visible_depts_map.get(doc_id) or []
        if allowed_depts and dept not in allowed_depts:
            continue
        result.append(doc_id)
    return result


def effective_doc_ids(requested: list[str] | None, allowed: list[str] | None) -> list[str] | None:
    """合并请求显式 doc_ids 与可见集。

    返回 None = 不过滤；返回空列表 = 明确无可见文档（调用方必须短路，不得
    作为 doc_ids 传入检索层）。
    """
    if allowed is None:
        return requested
    if not requested:
        return allowed
    return sorted(set(requested) & set(allowed))


def can_view_document(manifest: dict, user: ScopeUser) -> bool:
    """单文档可见性判据：与 visible_doc_ids 同一语义，用于按 file_id 直取产物的入口。

    产物体（clean.md/document.json/原件）以前只验登录不验文档归属，知道 file_id
    就能读别人文档；阅读页把这条路径变成常规能力，必须在入口处补齐判据。
    """
    if user is None:
        return True
    if user.get("role") == "admin":
        return True
    username = user.get("username")
    if not username:
        return True
    owner = manifest.get("owner")
    if owner is not None:
        return owner == username
    # manifest/索引存的是 CSV（与 es_file.visible_depts 同表示），不能按列表或
    # 子串判断：部门 "1" 不能命中 "10"。
    raw_depts = manifest.get("visible_depts")
    if isinstance(raw_depts, str):
        allowed_depts = [part.strip() for part in raw_depts.split(",") if part.strip()]
    else:
        allowed_depts = [str(part).strip() for part in (raw_depts or []) if str(part).strip()]
    if allowed_depts:
        return (user.get("dept") or "").strip() in allowed_depts
    return True
