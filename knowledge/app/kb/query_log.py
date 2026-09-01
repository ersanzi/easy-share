"""查询日志：记录每次检索/生成调用，支撑健康度仪表盘的使用率与盲区分析。

SQLite 存储，与 job_store 同模式。轻量追加，不做复杂索引。
"""
from __future__ import annotations

import json
import sqlite3
import threading
from datetime import datetime, timezone
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
