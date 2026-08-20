---
description: 登记一个新迭代主题：生成迭代文档骨架并在 progress.md 挂牌
disable-model-invocation: true
---

# /iterate — 登记新迭代

按项目迭代纪律（`docs/version-iteration.md`）完成开工登记。用户给的主题：$ARGUMENTS

执行步骤：

1. 若主题为空，先问用户本次迭代主题是什么，不要自行假设。
2. 读 `docs/version-iteration.md` 的「版本工作区模板」与 `docs/progress.md` 的「迭代记录」表头格式。
3. 创建 `docs/iterations/<今天日期 YYYY-MM-DD>-<主题短横线 slug>.md`：复制模板骨架，预填「用户问题」（从用户最近对话或主题描述提炼），其余段落留待实现中填写。禁止虚构已完成内容。
4. 更新 `docs/progress.md`：
   - 「最后更新」改为今天；
   - 「迭代记录」表最上方插入一行：`| <日期> | <主题> | 🔄 进行中 |`。
5. 汇报：新建文件路径 + progress.md 已挂牌。同时提醒收尾义务：完成后回填迭代文档「完成记录」、progress.md 该行改「已完成」并视情况更新路线总览与 README；提交与双仓库推送仅在用户明确要求时进行。

边界：本命令不写业务代码；不改 `architecture.md`（除非迭代主题本身是架构变化，此时在迭代文档「设计决策」中标注须同步 `architecture.md` 的条目）。
