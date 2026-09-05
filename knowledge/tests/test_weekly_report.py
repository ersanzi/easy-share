"""观察期周报：窗口聚合与文本渲染。

覆盖任务验收三条：脚本聚合逻辑可跑、空数据兜底、窗口过滤正确。
"""
from __future__ import annotations

from datetime import UTC, datetime, timedelta

from app.kb.query_log import QueryLog
from app.kb.weekly_report import build_weekly_report


def _log(tmp_path, entries: int = 0) -> QueryLog:
    return QueryLog(str(tmp_path / "query_log.db"))


def test_empty_log_produces_fallback_report(tmp_path) -> None:
    log = _log(tmp_path)

    report = build_weekly_report(log, days=7)

    assert "无任何查询记录" in report
    assert "观察周报" in report


def test_report_summarizes_window(tmp_path) -> None:
    log = _log(tmp_path)
    log.log_retrieval("住宿报销上限是多少？", "hybrid", 5, 5, 0.62, ["travel-reimburse"])
    log.log_retrieval("报销制度里完全查不到的词", "hybrid", 5, 0, 0.0, [])
    log.log_generation(
        "住宿报销上限是多少？", "hybrid_rerank", 5, 5, 0.62, ["travel-reimburse"],
        answer_length=320, faithfulness_avg=0.95, unsupported_ratio=0.0,
    )

    report = build_weekly_report(log, days=7)

    assert "查询总数 3（检索 2 / 生成 1）" in report
    assert "hybrid 2 次" in report and "hybrid_rerank 1 次" in report
    assert "零结果查询 1 次" in report
    assert "travel-reimburse（被引 2 次）" in report
    assert "报销制度里完全查不到的词" in report, "盲区问题应出现在报告里"
    assert "平均忠实度 0.95" in report
    assert "有查询的天数 1/7" in report


def test_window_excludes_older_entries(tmp_path) -> None:
    log = _log(tmp_path)
    log.log_retrieval("旧查询", "hybrid", 5, 5, 0.5, ["old-doc"])

    # 把 now 推到 10 天后：刚才的记录落在 7 天窗口之外
    future = datetime.now(UTC) + timedelta(days=10)
    stats = log.windowed_stats(days=7, now=future)
    report = build_weekly_report(log, days=7, now=future)

    assert stats["total"] == 0
    assert "旧查询" not in report
    assert "无任何查询记录" in report


def test_blind_spot_dedup_and_thresholds(tmp_path) -> None:
    log = _log(tmp_path)
    for _ in range(3):
        log.log_retrieval("同一个查不到的问题", "hybrid", 5, 0, 0.0, [])
    log.log_retrieval("正常查询占位", "vector", 5, 4, 0.55, ["some-doc"])

    stats = log.windowed_stats(days=7)
    report = build_weekly_report(log, days=7)

    assert stats["blind_total"] == 3
    assert "盲区查询共 3 次" in report
    assert "占 75.0%" in report
    assert "盲区查询占比超 30%" in report, "盲区占比超阈值应触发提示"
    # 同一问题去重只出现一次
    assert report.count("同一个查不到的问题") == 1
