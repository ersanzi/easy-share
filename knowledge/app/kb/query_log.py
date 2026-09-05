"""查询日志：记录每次检索/生成调用，支撑健康度仪表盘的使用率与盲区分析。

SQLite 存储，与 job_store 同模式。轻量追加，不做复杂索引。
"""
from __future__ import annotations

import json
import sqlite3
import threading
from datetime import datetime, timedelta, timezone
from pathlib import Path


class QueryLog:
    """追加式查询日志，提供聚合统计。"""

    def __init__(self, path: str) -> None:
        self.path = path
        self.lock = threading.RLock()
        Path(path).parent.mkdir(parents=True, exist_ok=True)
        self._init_db()

    def _connect(self) -> sqlite3.Connection:
        conn = sqlite3.connect(self.path, check_same_thread=False)
        conn.row_factory = sqlite3.Row
        return conn

    def _init_db(self) -> None:
        with self.lock, self._connect() as conn:
            conn.execute("""
                CREATE TABLE IF NOT EXISTS queries (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    created_at TEXT NOT NULL,
                    kind TEXT NOT NULL,
                    question TEXT NOT NULL,
                    strategy TEXT,
                    top_k INTEGER,
                    result_count INTEGER,
                    top_score REAL,
                    file_ids_hit TEXT,
                    answer_length INTEGER,
                    faithfulness_avg REAL,
                    unsupported_ratio REAL
                )
            """)
            conn.execute("CREATE INDEX IF NOT EXISTS idx_queries_created ON queries(created_at)")
            conn.commit()

    def log_retrieval(
        self,
        question: str,
        strategy: str,
        top_k: int,
        result_count: int,
        top_score: float,
        file_ids_hit: list[str],
    ) -> None:
        """记录一次检索调用。"""
        with self.lock, self._connect() as conn:
            conn.execute(
                """INSERT INTO queries
                   (created_at, kind, question, strategy, top_k, result_count, top_score, file_ids_hit)
                   VALUES (?, ?, ?, ?, ?, ?, ?, ?)""",
                (
                    datetime.now(timezone.utc).isoformat(),
                    "retrieval",
                    question,
                    strategy,
                    top_k,
                    result_count,
                    round(top_score, 4),
                    json.dumps(file_ids_hit, ensure_ascii=False),
                ),
            )
            conn.commit()

    def log_generation(
        self,
        question: str,
        strategy: str,
        top_k: int,
        result_count: int,
        top_score: float,
        file_ids_hit: list[str],
        answer_length: int,
        faithfulness_avg: float | None,
        unsupported_ratio: float | None,
    ) -> None:
        """记录一次生成调用。忠实度指标可传 None（生产链路不做逐句分析，仅驾驶舱审计计算）。"""
        with self.lock, self._connect() as conn:
            conn.execute(
                """INSERT INTO queries
                   (created_at, kind, question, strategy, top_k, result_count, top_score,
                    file_ids_hit, answer_length, faithfulness_avg, unsupported_ratio)
                   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                (
                    datetime.now(timezone.utc).isoformat(),
                    "generation",
                    question,
                    strategy,
                    top_k,
                    result_count,
                    round(top_score, 4),
                    json.dumps(file_ids_hit, ensure_ascii=False),
                    answer_length,
                    faithfulness_avg if faithfulness_avg is None else round(faithfulness_avg, 3),
                    unsupported_ratio if unsupported_ratio is None else round(unsupported_ratio, 3),
                ),
            )
            conn.commit()

    def stats(self, days: int = 30) -> dict:
        """聚合统计：总查询数、高频命中文档、从未命中文档、低分查询（盲区）。"""
        with self.lock, self._connect() as conn:
            # 总查询数
            total = conn.execute("SELECT COUNT(*) FROM queries").fetchone()[0]

            # 最近 N 天
            recent = conn.execute(
                "SELECT COUNT(*) FROM queries WHERE created_at >= datetime('now', ?)",
                (f"-{days} days",),
            ).fetchone()[0]

            # 文档命中频次
            rows = conn.execute("SELECT file_ids_hit FROM queries WHERE file_ids_hit IS NOT NULL").fetchall()
            hit_counts: dict[str, int] = {}
            for row in rows:
                try:
                    ids = json.loads(row["file_ids_hit"])
                    for fid in ids:
                        hit_counts[fid] = hit_counts.get(fid, 0) + 1
                except (json.JSONDecodeError, TypeError):
                    pass

            most_cited = sorted(hit_counts.items(), key=lambda x: x[1], reverse=True)[:10]

            # 盲区：result_count == 0 或 top_score 极低的查询
            blind_rows = conn.execute(
                "SELECT question, result_count, top_score FROM queries WHERE result_count = 0 OR top_score < 0.1 ORDER BY created_at DESC LIMIT 50"
            ).fetchall()
            blind_spots = [
                {"question": r["question"], "result_count": r["result_count"], "top_score": r["top_score"]}
                for r in blind_rows
            ]

            # 生成质量统计
            gen_stats = conn.execute(
                """SELECT COUNT(*) as cnt, AVG(faithfulness_avg) as avg_faith, AVG(unsupported_ratio) as avg_unsup
                   FROM queries WHERE kind = 'generation' AND faithfulness_avg IS NOT NULL"""
            ).fetchone()

            return {
                "total_queries": total,
                "recent_queries": recent,
                "most_cited_docs": [{"file_id": fid, "count": cnt} for fid, cnt in most_cited],
                "blind_spots": blind_spots,
                "generation": {
                    "total": gen_stats["cnt"] if gen_stats else 0,
                    "avg_faithfulness": round(gen_stats["avg_faith"], 3) if gen_stats and gen_stats["avg_faith"] else None,
                    "avg_unsupported_ratio": round(gen_stats["avg_unsup"], 3) if gen_stats and gen_stats["avg_unsup"] else None,
                },
            }

    def windowed_stats(self, days: int, now: datetime | None = None) -> dict:
        """观察窗口聚合（周报用）：所有指标严格限定在最近 days 天内。

        与 stats()（驾驶舱全时段/混合口径）刻意分开，互不影响。
        created_at 由本类以 UTC ISO 格式写入，窗口边界用同一格式的字符串比较。
        """
        now = now or datetime.now(timezone.utc)
        since = (now - timedelta(days=days)).isoformat()
        with self.lock, self._connect() as conn:
            kind_rows = conn.execute(
                "SELECT kind, COUNT(*) AS cnt FROM queries WHERE created_at >= ? GROUP BY kind",
                (since,),
            ).fetchall()
            by_kind = {row["kind"]: row["cnt"] for row in kind_rows}
            total = sum(by_kind.values())

            day_rows = conn.execute(
                """SELECT COUNT(DISTINCT substr(created_at, 1, 10)) AS active_days
                   FROM queries WHERE created_at >= ?""",
                (since,),
            ).fetchone()

            quality = conn.execute(
                """SELECT AVG(result_count) AS avg_results, AVG(top_score) AS avg_top_score,
                          SUM(CASE WHEN result_count = 0 THEN 1 ELSE 0 END) AS zero_result
                   FROM queries WHERE created_at >= ?""",
                (since,),
            ).fetchone()

            strategy_rows = conn.execute(
                """SELECT COALESCE(strategy, 'unknown') AS strategy, COUNT(*) AS cnt
                   FROM queries WHERE created_at >= ? GROUP BY strategy ORDER BY cnt DESC""",
                (since,),
            ).fetchall()

            # 引用文档频次与去重数（窗口内）
            hit_rows = conn.execute(
                "SELECT file_ids_hit FROM queries WHERE created_at >= ? AND file_ids_hit IS NOT NULL",
                (since,),
            ).fetchall()
            hit_counts: dict[str, int] = {}
            for row in hit_rows:
                try:
                    for fid in json.loads(row["file_ids_hit"]):
                        hit_counts[fid] = hit_counts.get(fid, 0) + 1
                except (json.JSONDecodeError, TypeError):
                    pass
            most_cited = sorted(hit_counts.items(), key=lambda x: x[1], reverse=True)[:5]

            # 盲区：零结果或极低分（与 stats() 同口径），窗口内按问题去重
            blind_rows = conn.execute(
                """SELECT question, result_count, top_score, MAX(created_at) AS latest
                   FROM queries
                   WHERE created_at >= ? AND (result_count = 0 OR top_score < 0.1)
                   GROUP BY question ORDER BY latest DESC LIMIT 10""",
                (since,),
            ).fetchall()
            blind_total = conn.execute(
                """SELECT COUNT(*) FROM queries
                   WHERE created_at >= ? AND (result_count = 0 OR top_score < 0.1)""",
                (since,),
            ).fetchone()[0]

            gen = conn.execute(
                """SELECT COUNT(*) AS cnt,
                          AVG(faithfulness_avg) AS avg_faith,
                          AVG(unsupported_ratio) AS avg_unsup,
                          AVG(answer_length) AS avg_answer
                   FROM queries
                   WHERE created_at >= ? AND kind = 'generation' AND faithfulness_avg IS NOT NULL""",
                (since,),
            ).fetchone()
            gen_total = conn.execute(
                "SELECT COUNT(*) FROM queries WHERE created_at >= ? AND kind = 'generation'",
                (since,),
            ).fetchone()[0]

            return {
                "days": days,
                "since": since,
                "until": now.isoformat(),
                "total": total,
                "retrieval": by_kind.get("retrieval", 0),
                "generation": by_kind.get("generation", 0),
                "active_days": day_rows["active_days"] if day_rows else 0,
                "avg_results": round(quality["avg_results"], 2) if quality and quality["avg_results"] is not None else None,
                "avg_top_score": round(quality["avg_top_score"], 3) if quality and quality["avg_top_score"] is not None else None,
                "zero_result": quality["zero_result"] or 0 if quality else 0,
                "strategy_usage": [
                    {"strategy": row["strategy"], "count": row["cnt"]} for row in strategy_rows
                ],
                "most_cited": [{"file_id": fid, "count": cnt} for fid, cnt in most_cited],
                "cited_doc_count": len(hit_counts),
                "blind_spots": [
                    {"question": r["question"], "result_count": r["result_count"], "top_score": r["top_score"]}
                    for r in blind_rows
                ],
                "blind_total": blind_total or 0,
                "generation_scored": {
                    "scored": gen["cnt"] if gen else 0,
                    "generation_total": gen_total,
                    "avg_faithfulness": round(gen["avg_faith"], 3) if gen and gen["avg_faith"] is not None else None,
                    "avg_unsupported": round(gen["avg_unsup"], 3) if gen and gen["avg_unsup"] is not None else None,
                    "avg_answer_length": round(gen["avg_answer"]) if gen and gen["avg_answer"] is not None else None,
                },
            }
