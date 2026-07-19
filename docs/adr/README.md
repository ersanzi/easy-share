# EasyShare 架构决策记录

本目录保存影响多个版本、难以通过单次代码 diff 解释的架构决策。当前实现事实仍以 [`../architecture.md`](../architecture.md) 为准。

## 状态含义

- **提议（Proposed）**：推荐方向已经形成，但仍有产品输入、原型或实现验证未完成。
- **接受（Accepted）**：可以作为实现约束，变更需要新的 ADR。
- **已取代（Superseded）**：由后续 ADR 替代，保留用于追溯。
- **拒绝（Rejected）**：经过评估但未采用。

## 索引

| 编号 | 标题 | 状态 |
| --- | --- | --- |
| [0001](0001-file-identity-and-version-model.md) | 文件身份、目录树与版本模型 | 提议 |
| [0002](0002-metadata-object-storage-consistency.md) | 元数据与对象存储一致性 | 提议 |
| [0003](0003-incremental-sync-and-conflicts.md) | 增量同步、游标与冲突语义 | 提议 |
| [0004](0004-local-state-and-task-persistence.md) | 本地状态与任务持久化 | 提议 |
| [0005](0005-cfapi-helper-boundary.md) | CfAPI Helper 与 Go Core 边界 | 提议 |
| [0006](0006-rustfs-self-hosted-object-storage.md) | 自建优先并采用 RustFS 作为对象存储 | 接受（生产受门禁约束） |

## 新 ADR 模板

```markdown
# ADR-NNNN：标题

- 状态：提议
- 日期：YYYY-MM-DD
- 决策者：EasyShare maintainers
- 关联：相关文档或 issue

## 背景
## 决策
## 不变量
## 备选方案
## 影响
## 开放问题
## 验证
```

ADR 只记录重要且长期的取舍；具体迭代任务和验收结果写入 [`../iterations/`](../iterations/README.md)。
