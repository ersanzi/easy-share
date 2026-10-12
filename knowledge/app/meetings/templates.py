"""听记总结模板：不同场景用不同的纪要结构（对标钉钉听记模板系统）。

模板本质是「总结阶段的结构化 prompt」——转写/说话人/待办/导图/导出链路全部复用。
内置模板与钉钉对齐常用场景；非默认模板的总结输出 sections 通用结构。
"""

BUILTIN_TEMPLATES: list[dict] = [
    {
        "id": "meeting-minutes",
        "name": "会议纪要",
        "description": "智能总结会议内容：纪要/要点/待办（默认）",
        "sections": "纪要（按议题归纳的连贯段落）；要点（每条一句话，保留数据）；待办单独由 todos 字段输出",
    },
    {
        "id": "weekly",
        "name": "团队周会",
        "description": "适用于常规会议、团队评审",
        "sections": "本周进展（各成员/模块推进）；数据与指标（如有）；问题与风险；下周计划",
    },
    {
        "id": "daily-standup",
        "name": "每日站会",
        "description": "适用于简短的每日会议",
        "sections": "昨日完成；今日计划；阻塞与协助请求；其他通知",
    },
    {
        "id": "interview",
        "name": "面试报告",
        "description": "适用于企业招聘的结构化评估",
        "sections": "候选人背景；专业能力评估；沟通与表达；综合评价；录用建议",
    },
    {
        "id": "interview-candidate",
        "name": "访谈记录",
        "description": "适用于客户访谈、用户调研",
        "sections": "访谈对象与背景；关键反馈（原话要点）；痛点与诉求；机会与后续动作",
    },
    {
        "id": "training",
        "name": "培训笔记",
        "description": "适用于培训、讲座、分享会",
        "sections": "核心概念；要点详解；案例与实践；学习行动",
    },
    {
        "id": "brainstorm",
        "name": "头脑风暴会",
        "description": "适用于问题解决、头脑风暴",
        "sections": "讨论主题；想法清单（去重归类）；可行性初评；后续行动",
    },
]

DEFAULT_TEMPLATE_ID = "meeting-minutes"


def find_template(template_id: str) -> dict | None:
    """按 id 找内置模板；未知/空返回 None（回退默认）。"""
    for template in BUILTIN_TEMPLATES:
        if template["id"] == template_id:
            return template
    return None
