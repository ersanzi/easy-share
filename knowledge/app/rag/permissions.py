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

# request.state.user 的形状：{"username": str, "role": str}（auth 中间件注入）
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
    return [doc_id for doc_id, owner in owners.items() if owner is None or owner == username]


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
