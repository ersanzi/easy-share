"""观察期周报：把 QueryLog 窗口聚合渲染为可直接发给人看的中文文本。

用途：公司部署两周观察期（company-rollout-guide）的周期决策输入——
使用率是否起来、同事在问什么、哪些问题查不到（盲区）、生成质量如何。
纯格式化模块，聚合数据来自 QueryLog.windowed_stats；空数据有兜底文案。
"""
from __future__ import annotations

from datetime import datetime, timedelta, timezone

from app.kb.query_log import QueryLog

# 报告面向国内同事，时间统一按 UTC+8 显示（created_at 落库为 UTC）
_REPORT_TZ = timezone(timedelta(hours=8))


def _fmt_time(iso: str) -> str:
    return datetime.fromisoformat(iso).astimezone(_REPORT_TZ).strftime("%Y-%m-%d %H:%M")


def _pct(part: int, total: int) -> str:
    return f"{part / total * 100:.1f}%" if total else "—"


def build_weekly_report(log: QueryLog, days: int = 7, now: datetime | None = None) -> str:
    """生成周报文本。now 可注入用于测试；days 为观察窗口天数。"""
    stats = log.windowed_stats(days=days, now=now)

    lines: list[str] = []
    lines.append("=" * 58)
    lines.append(f"EasyShare 知识服务观察周报（最近 {days} 天）")
    lines.append("=" * 58)
    lines.append(f"观察窗口：{_fmt_time(stats['since'])} ~ {_fmt_time(stats['until'])}（UTC+8）")
    lines.append("")

    # 一、使用率
    lines.append("一、使用率")
    if stats["total"] == 0:
        lines.append("  窗口内无任何查询记录。若服务已部署，请确认同事是否在用 /query、")
        lines.append("  watch_dirs 是否有文档入库；连续两周无记录 = 发芽未启动。")
        lines.append("")
        lines.append("（本周报由 scripts/weekly_report.py 生成，数据源 query_log.db）")
        return "\n".join(lines)
    lines.append(
        f"  查询总数 {stats['total']}（检索 {stats['retrieval']} / 生成 {stats['generation']}），"
        f"日均 {stats['total'] / days:.1f} 次，有查询的天数 {stats['active_days']}/{days}"
    )
    if stats["strategy_usage"]:
        parts = [
            f"{item['strategy']} {item['count']} 次（{_pct(item['count'], stats['total'])}）"
            for item in stats["strategy_usage"]
        ]
        lines.append(f"  检索策略分布：{'、'.join(parts)}")
    lines.append("")

    # 二、检索命中
    lines.append("二、检索命中")
    if stats["avg_results"] is not None:
        lines.append(
            f"  平均每查询返回 {stats['avg_results']} 条结果，平均 top 分数 {stats['avg_top_score']}"
        )
    lines.append(
        f"  零结果查询 {stats['zero_result']} 次；窗口内被引用文档去重 {stats['cited_doc_count']} 个"
    )
    if stats["most_cited"]:
        lines.append("  高频引用文档 Top5：")
        for index, item in enumerate(stats["most_cited"], 1):
            lines.append(f"    {index}. {item['file_id']}（被引 {item['count']} 次）")
    lines.append("")

    # 三、盲区查询
    lines.append("三、盲区查询（零结果或低分，同事问了但知识库答不上）")
    lines.append(f"  窗口内盲区查询共 {stats['blind_total']} 次（占 {_pct(stats['blind_total'], stats['total'])}）")
    if stats["blind_spots"]:
        lines.append("  最近去重问题（最多 10 条）：")
        for index, spot in enumerate(stats["blind_spots"], 1):
            # 向量检索恒返回 top_k 条（零分也计数），盲区标签按分数而非条数表达
            detail = f"低分 {spot['top_score']}" if spot["result_count"] else "零结果"
            lines.append(f"    {index}. [{detail}] {spot['question']}")
    else:
        lines.append("  无盲区查询。")
    lines.append("")

    # 四、生成质量
    gen = stats["generation_scored"]
    lines.append("四、生成质量")
    if gen["generation_total"] == 0:
        lines.append("  窗口内无生成调用（未配置 LLM 或同事只用检索）。")
    elif gen["scored"] == 0:
        lines.append(f"  生成 {gen['generation_total']} 次，均无忠实度评分（生产链路默认不做逐句审计）。")
    else:
        lines.append(
            f"  生成 {gen['generation_total']} 次（含忠实度评分 {gen['scored']} 次）："
            f"平均忠实度 {gen['avg_faithfulness']}，平均无依据比例 {gen['avg_unsupported']}，"
            f"平均答案长度 {gen['avg_answer_length']} 字"
        )
    lines.append("")

    # 五、观察决策提示（只陈述事实触发的简单规则，不下结论）
    lines.append("五、观察提示")
    hints: list[str] = []
    if stats["total"] / days < 3:
        hints.append("日均查询不足 3 次：使用率偏低，观察结论需结合推广情况看。")
    if stats["total"] and stats["blind_total"] / stats["total"] > 0.3:
        hints.append("盲区查询占比超 30%：建议把第三节的 question 对照知识库补文档。")
    if gen["scored"] and (gen["avg_faithfulness"] or 1) < 0.7:
        hints.append("平均忠实度低于 0.7：建议在驾驶舱抽查生成审计定位引用不实回答。")
    if not hints:
        hints.append("各项指标无异常信号。")
    for hint in hints:
        lines.append(f"  - {hint}")
    lines.append("")
    lines.append("（本周报由 scripts/weekly_report.py 生成，数据源 query_log.db）")
    return "\n".join(lines)
