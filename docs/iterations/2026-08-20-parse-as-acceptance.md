# 解析即验收：处理结果内嵌单文档透视 + 入库时间展示

> 获批想法 2（2026-08-20 批准）：处理完成页内嵌单文档透视（切块地图 + 清洗 Diff），黑盒变白盒；顺带展示入库时间与解析器来源。
> 开工：2026-08-20。

## 用户问题

用户上传文档后只能看到"处理完成"，看不到 AI 是怎么读这份文档的——切了哪些块、清洗删改了什么、谁解析的。企业知识平台最大的销售阻力是信任，把处理结果从黑盒变白盒是建立信任最便宜的手段（驾驶舱组件已全部现成）。

## 目标

- /lab 处理完成视图提供"透视本文档"入口，内嵌单文档透视：切块地图、清洗 Diff、manifest 关键信息（解析器来源/路由、入库时间、块数/字符数）。
- /lab 问答结果 contexts 展示入库时间（ingested_at），回答引用可判断新旧。

## 非目标

- 不动驾驶舱（/debug）既有功能，只做 /lab 的内嵌与联动。
- 不做 WPS 插件端（里程碑 3）。
- 不改管线与 API 语义（只读消费已透出的字段）。

## 设计决策

- **预选链接而非 iframe 内嵌**：驾驶舱整页含四个 Tab，iframe 塞进 lab 卡片信息过载；cockpit.js 新增 `?doc=<file_id>` 预选参数，/lab 的「驾驶舱透视」一步直达已选文档的单文档透视（切块地图/清洗 Diff/统计）。
- **验收摘要条**消费 manifest 已有字段（`parsing.provider/backend/fallback_reason`、`processed_at`、`blocks/chunks/characters/warnings`），与驾驶舱同源，不新增端点。
- 引用片段展示 `ingested_at`（文档时间 chip），衔接上一迭代的时效闭环。
- 纯前端改动（lab.html/lab.js/lab.css + cockpit.js），无 API 语义变化。

## 完成记录

### 已完成（2026-08-20）

- `debug/cockpit.js`：初始化支持 `?doc=` 预选并自动加载单文档透视。
- `lab/index.html`：产物检查器面板新增「驾驶舱透视 ↗」入口与验收摘要条容器。
- `lab/lab.js`：`loadAcceptance`/`renderAcceptanceSummary`（选中已完成任务时拉 manifest 渲染解析器/入库时间/结构块/切块/字符/警告 chips；未完成隐藏）；引用片段新增文档时间标注；任务选中与刷新时联动加载。
- `lab/lab.css`：摘要 chip、透视链接、引用时间样式（沿用既有 design token）。
- 验证：`node --check` 两个 JS 通过；全量 `pytest -m "not integration"` **89 passed, 1 skipped**。

### 已知限制与后续工作

- /lab 为本地开发辅助界面（不进产品交付），浏览器端交互以人工冒烟为准：跑起服务后上传文档 → 完成后看摘要条 → 点透视跳转 → 提问看引用时间。
- 三条业务想法全部落地（体检报告入口待 WPS 前）。
