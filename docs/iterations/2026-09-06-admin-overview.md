# 管理员汇总页 — AdminPanel 概览 Tab（用户拍板三项之三）

- 日期：2026-09-06（同日第五片）
- 状态：**已完成**（三栈回归全绿：Java 37 / go 全量 / 知识服务 144 / vitest 40 + vue-tsc 干净；真机随下轮打包）

## 1. 形态与数据源

AdminPanel 新增「概览」Tab（管理页首屏右上角切换；默认 Tab 保持「账号」不变），
六张卡片一屏看全局：

| 卡片 | 数据源 |
| --- | --- |
| 账号总数 | RuoYi 用户列表 total（既有） |
| 近 30 天查询（含累计/盲区次数） | 知识服务新 `GET /stats`（QueryLog 聚合） |
| 知识文档数 + LLM 配置态 | vector_store.count() + generator 状态 |
| 生成质量（平均忠实度/次数） | QueryLog 生成聚合 |
| 共享空间用量/配额 | SpaceUsage（既有） |
| 容量承诺 vs 物理可用（超配标红） | CapacityService（既有） |

设计取舍：`/stats` 只出**聚合计数与文档 ID 热度**，不返回盲区问题明细——问题文本
的可见性沿用驾驶舱口径（回环 only），概览数字走桌面端管理页可能跨网段。

## 2. 实现

- 知识服务 `GET /stats?days=`（days 1-90 截断；QueryLog.stats 既有聚合的透传 + 文档数）；
- Core 网关 `GET /api/knowledge/stats`（登录态鉴权 + 令牌转发）；
- `internal/knowledge` Stats 客户端 + `internal/desktop` KnowledgeStats + `app.go`
  AdminKnowledgeStats 绑定 + Wails 级联；
- AdminPanel：概览 Tab + 卡片网格（暗色模式适配）；Tab 切换测试改为按文本找
  （新增 Tab 导致索引漂移的教训记入测试注释）。

**踩坑**：Wails 绑定生成器不支持匿名嵌套 struct——KnowledgeStats 的生成质量
字段必须命名类型（GenerationQuality/CitedDoc），否则 models.ts 直接生成坏 TS。

## 3. 遗留

- 查询趋势折线/按账号检索排行等进阶可视化：观察期看真实需要再加（数据都在 query_log）。
