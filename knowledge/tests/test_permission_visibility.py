"""权限感知切片（2b 文件归属 / 2c 权限感知检索）。

覆盖：owner 落 manifest 与索引元数据、令牌用户优先于请求显式 owner、
member 仅见共享+本人文档、admin 全见、未登录行为不变、请求显式 doc_ids
与可见集求交集、无 owner 的存量记录按共享处理、旧任务库自动补列。
"""
from __future__ import annotations

import sqlite3
import time

from fastapi.testclient import TestClient

from app.jobs.store import JobStore
from app.main import create_app
from tests.helpers import FakeStorage, make_services


def _wait_for_terminal(client: TestClient, job_id: str) -> dict:
    for _ in range(200):
        response = client.get(f"/jobs/{job_id}")
        assert response.status_code == 200
        payload = response.json()
        if payload["status"] in {"completed", "failed"}:
            assert payload["status"] == "completed", payload
            return payload
        time.sleep(0.01)
    raise AssertionError("job did not finish in time")


def _process(client: TestClient, file_id: str, content: bytes, token: str | None = None, **extra) -> None:
    """走 /documents/process 入库一个文本对象并等待完成。"""
    headers = {"Authorization": f"Bearer {token}"} if token else {}
    payload = {
        "file_id": file_id,
        "version_id": "v1",
        "object_key": f"source/{file_id}.txt",
        **extra,
    }
    response = client.post("/documents/process", json=payload, headers=headers)
    assert response.status_code == 202, response.text
    _wait_for_terminal(client, response.json()["id"])


def _context_doc_ids(client: TestClient, question: str, token: str | None = None, **payload_extra) -> set[str]:
    headers = {"Authorization": f"Bearer {token}"} if token else {}
    response = client.post(
        "/query",
        json={"question": question, "top_k": 50, **payload_extra},
        headers=headers,
    )
    assert response.status_code == 200, response.text
    return {chunk["doc_id"] for chunk in response.json()["contexts"]}


def _make_client(tmp_path, texts: dict[str, bytes]):
    storage = FakeStorage({f"source/{fid}.txt": body for fid, body in texts.items()})
    services = make_services(tmp_path, storage)
    return create_app(services), services


def _user_token(services, username: str, role: str = "member") -> str:
    services.users.create_user(username, "pw-test", role=role)
    token, _ = services.users.issue_token(username)
    return token


def test_owner_lands_in_manifest_and_index(tmp_path) -> None:
    app, services = _make_client(tmp_path, {"file-a": b"alpha document about quarterly sales"})
    with TestClient(app) as client:
        _process(client, "file-a", b"", owner="alice")

    manifest = services.pipeline.read_manifest("file-a", "v1")
    assert manifest["owner"] == "alice"

    records = services.vector_store.get_doc("file-a")
    assert records and all(record["metadata"]["owner"] == "alice" for record in records)


def test_token_owner_overrides_explicit_request(tmp_path) -> None:
    app, services = _make_client(tmp_path, {"file-a": b"alpha document about quarterly sales"})
    alice = _user_token(services, "alice")
    with TestClient(app) as client:
        # 带令牌的请求试图把归属写成 bob：必须以令牌用户 alice 为准，防伪造
        _process(client, "file-a", b"", token=alice, owner="bob")
    assert services.pipeline.read_manifest("file-a", "v1")["owner"] == "alice"


def test_member_sees_shared_and_own_docs_only(tmp_path) -> None:
    texts = {
        "doc-shared": b"shared handbook about company policy and workflow",
        "doc-alice": b"alice private contract draft with pricing details",
        "doc-bob": b"bob private salary table and hr records",
    }
    app, services = _make_client(tmp_path, texts)
    alice = _user_token(services, "alice")
    bob = _user_token(services, "bob")

    with TestClient(app) as client:
        _process(client, "doc-shared", b"")  # 无令牌内部调用：共享文档
        _process(client, "doc-alice", b"", token=alice)
        _process(client, "doc-bob", b"", token=bob)

        assert _context_doc_ids(client, "document", token=alice) == {"doc-shared", "doc-alice"}
        assert _context_doc_ids(client, "document", token=bob) == {"doc-shared", "doc-bob"}


def test_admin_sees_all_docs(tmp_path) -> None:
    texts = {
        "doc-shared": b"shared handbook about company policy",
        "doc-alice": b"alice private contract draft",
    }
    app, services = _make_client(tmp_path, texts)
    alice = _user_token(services, "alice")
    admin = _user_token(services, "root", role="admin")
    with TestClient(app) as client:
        _process(client, "doc-shared", b"")
        _process(client, "doc-alice", b"", token=alice)

        assert _context_doc_ids(client, "document", token=admin) == {"doc-shared", "doc-alice"}


def test_anonymous_query_unfiltered(tmp_path) -> None:
    texts = {
        "doc-shared": b"shared handbook about company policy",
        "doc-alice": b"alice private contract draft",
    }
    app, services = _make_client(tmp_path, texts)
    alice = _user_token(services, "alice")
    with TestClient(app) as client:
        _process(client, "doc-shared", b"")
        _process(client, "doc-alice", b"", token=alice)

        # 未登录（auth 关闭的本地场景）：行为与权限切片之前一致，全部可见
        assert _context_doc_ids(client, "document") == {"doc-shared", "doc-alice"}


def test_requested_doc_ids_intersected_with_visibility(tmp_path) -> None:
    texts = {
        "doc-alice": b"alice private contract draft",
        "doc-bob": b"bob private salary table",
    }
    app, services = _make_client(tmp_path, texts)
    alice = _user_token(services, "alice")
    bob = _user_token(services, "bob")
    with TestClient(app) as client:
        _process(client, "doc-alice", b"", token=alice)
        _process(client, "doc-bob", b"", token=bob)

        # alice 显式请求 bob 的文档：交集为空 → 空结果（不是"不过滤"放大成全见）
        assert _context_doc_ids(client, "contract", token=alice, doc_ids=["doc-bob"]) == set()
        # alice 显式请求自己的文档：正常返回
        assert _context_doc_ids(client, "contract", token=alice, doc_ids=["doc-alice"]) == {"doc-alice"}


def test_legacy_index_records_without_owner_are_shared(tmp_path) -> None:
    app, services = _make_client(tmp_path, {"doc-old": b"legacy record without owner field"})
    # 直接向向量库塞一条无 owner 的存量记录（模拟权限切片之前入库的数据）
    services.vector_store.replace_doc(
        "doc-legacy",
        [
            {
                "id": "doc-legacy:0",
                "doc_id": "doc-legacy",
                "text": "legacy salary records ingested before ownership",
                "metadata": {"filename": "legacy.txt"},
                "embedding": services.embedder.embed(["legacy salary records ingested before ownership"])[0],
            }
        ],
    )
    alice = _user_token(services, "alice")
    with TestClient(app) as client:
        assert "doc-legacy" in _context_doc_ids(client, "legacy salary")


def test_job_store_migrates_legacy_db(tmp_path) -> None:
    db_path = str(tmp_path / "legacy-jobs.db")
    connection = sqlite3.connect(db_path)
    connection.execute(
        """
        CREATE TABLE processing_jobs (
            id TEXT PRIMARY KEY,
            file_id TEXT NOT NULL,
            version_id TEXT NOT NULL,
            object_key TEXT NOT NULL,
            filename TEXT NOT NULL,
            status TEXT NOT NULL,
            stage TEXT NOT NULL,
            progress INTEGER NOT NULL DEFAULT 0,
            retry_count INTEGER NOT NULL DEFAULT 0,
            error_code TEXT,
            error_message TEXT,
            result_json TEXT,
            created_at TEXT NOT NULL,
            updated_at TEXT NOT NULL,
            started_at TEXT,
            finished_at TEXT
        )
        """
    )
    connection.execute(
        "INSERT INTO processing_jobs (id, file_id, version_id, object_key, filename, status, stage,"
        " progress, retry_count, created_at, updated_at)"
        " VALUES ('j1', 'f', 'v', 'k', 'old.txt', 'completed', 'completed', 100, 0, '2026-01-01', '2026-01-01')"
    )
    connection.commit()
    connection.close()

    store = JobStore(db_path)  # 打开即触发补列迁移
    assert store.get("j1").owner is None
    job, created = store.create_or_get(
        file_id="f2", version_id="v", object_key="k2", filename="new.txt", owner="alice"
    )
    assert created and job.owner == "alice"
    store.close()


def test_dept_visibility_filters_shared_docs(tmp_path) -> None:
    """片 2b：共享文档声明 visible_depts 后，仅所属部门成员与 admin 可见。"""
    texts = {
        "doc-research": b"confidential research benchmark and roadmap details",
        "doc-open": b"open company handbook about onboarding workflow",
    }
    app, services = _make_client(tmp_path, texts)

    def make_user(username: str, dept: str = "") -> str:
        services.users.create_user(username, "pw-test", dept=dept)
        token, _ = services.users.issue_token(username)
        return token

    research_user = make_user("alice", dept="research")
    sales_user = make_user("bob", dept="sales")
    no_dept_user = make_user("carol")
    admin_token = _user_token(services, "admin", role="admin")

    with TestClient(app) as client:
        _process(
            client, "doc-research", b"",
            visible_depts=["research"],
        )
        _process(client, "doc-open", b"")

        seen_research = _context_doc_ids(client, "document", token=research_user)
        assert seen_research == {"doc-research", "doc-open"}

        seen_sales = _context_doc_ids(client, "document", token=sales_user)
        assert seen_sales == {"doc-open"}

        seen_no_dept = _context_doc_ids(client, "document", token=no_dept_user)
        assert seen_no_dept == {"doc-open"}

        seen_admin = _context_doc_ids(client, "document", token=admin_token)
        assert seen_admin == {"doc-research", "doc-open"}


def test_visible_depts_lands_in_metadata_and_manifest(tmp_path) -> None:
    app, services = _make_client(tmp_path, {"file-a": b"alpha document about quarterly sales"})
    with TestClient(app) as client:
        _process(client, "file-a", b"", visible_depts=["research"])

    manifest = services.pipeline.read_manifest("file-a", "v1")
    assert manifest["visible_depts"] == "research"

    records = services.vector_store.get_doc("file-a")
    assert records and all(record["metadata"]["visible_depts"] == "research" for record in records)
    assert services.vector_store.doc_visible_depts().get("file-a") == ["research"]


def test_auth_store_round_trips_dept(tmp_path) -> None:
    services_users = None
    from app.auth.store import UserStore

    store = UserStore(str(tmp_path / "auth.db"))
    store.create_user("alice", "pw", dept="research")
    assert store.get_user("alice")["dept"] == "research"

    # 旧库（无 dept 列）自动补列
    import sqlite3
    legacy = str(tmp_path / "legacy.db")
    conn = sqlite3.connect(legacy)
    conn.execute("CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL, salt TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'member', created_at TEXT NOT NULL)")
    conn.commit()
    conn.close()

    store2 = UserStore(legacy)
    store2.create_user("bob", "pw")
    assert store2.get_user("bob")["dept"] == ""
    store2.set_user_dept("bob", "sales")
    assert store2.get_user("bob")["dept"] == "sales"
