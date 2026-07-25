# EasyShare 文档导航

这里记录**当前实现**、**迭代流程**和**历史设计**。代码行为与文档冲突时，以代码和测试为准，并在同一次改动中修正文档。

## 开发必读

| 文档 | 用途 | 维护时机 |
| --- | --- | --- |
| [`../README.md`](../README.md) | 项目入口、快速运行、产物位置 | 环境或入口变化时 |
| [`architecture.md`](architecture.md) | 进程、模块、端口、API、生命周期 | 架构或协议变化时 |
| [`progress.md`](progress.md) | 进度唯一真相源、迭代记录表 | 每次迭代开始和结束时 |
| [`product-vision.md`](product-vision.md) | WPS 式网络云盘与内网协同的长期方向 | 产品边界或路线变化时 |
| [`knowledge-platform.md`](knowledge-platform.md) | 知识平台架构、选型与里程碑 | 知识平台方向或选型变化时 |
| [`macos-port.md`](macos-port.md) | macOS 移植状态、平台抽象与 CI | 跨平台结构变化时 |
| [cloudreve-benchmark.md](cloudreve-benchmark.md) | Cloudreve 对标、能力差距与可迁移设计 | 对标基线或优先级变化时 |
| [`technical-selection.md`](technical-selection.md) | 云盘演进的技术选型基线、阶段门禁与推荐组合 | 技术路线或首选方案变化时 |
| [`technology-evaluation.md`](technology-evaluation.md) | “成熟但先进”的评估门禁、产品雷达和对象存储候选 | 依赖状态变化或生产选型复审时 |
| [`adr/`](adr/README.md) | 跨版本架构决策、备选方案和验证要求 | 重要决策提出、接受或被取代时 |
| [`development.md`](development.md) | 本地开发、测试、代码定位 | 工具链或目录变化时 |
| [`version-iteration.md`](version-iteration.md) | 下一版本从规划到发布的操作步骤 | 流程变化时 |
| [`iterations/`](iterations/README.md) | 每个版本的目标、决策和验收记录 | 每次迭代开始和结束时 |
| [../deploy/rustfs/](../deploy/rustfs/README.md) | 固定版本的 RustFS 本地开发与一致性测试环境 | RustFS 版本或启动方式变化时 |
| [`troubleshooting.md`](troubleshooting.md) | 日志读取和常见故障 | 每次解决新运行问题后 |
| [`testing/knowledge-service-checklist.md`](testing/knowledge-service-checklist.md) | 知识服务手工验收 | 知识平台功能变化时 |

## 历史资料

以下文档用于解释项目形成过程，不应直接作为新功能实现规范：

- [`archive/initial-concept.md`](archive/initial-concept.md)：更早的概念草案，包含未实现设想
- [`archive/windows-mvp-checklist.md`](archive/windows-mvp-checklist.md)：早期 Windows MVP 验收清单（Z 盘/Digest 时代，已被 NameSpace 替代）
- [`archive/knowledge-platform-roadmap.md`](archive/knowledge-platform-roadmap.md)：知识平台早期任务分解（选型已更新，以 knowledge-platform.md 为准）

## 文档维护约定

1. 当前事实写入 `architecture.md` 或 `development.md`，不要继续修改历史计划来伪装现状。
2. 新版本开始时，按 `version-iteration.md` 的模板在 `iterations/` 新建记录，先写目标、非目标和验收标准，再开始编码。
3. API、端口、配置字段、退出顺序或日志位置变化时，必须同步更新相关文档。
4. 新发现的环境问题在修复后记录到 `troubleshooting.md`。
5. 发布前按 Windows 验收清单逐项检查，并记录未完成项。





