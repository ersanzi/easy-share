# EasyShare 文档导航

这里记录**当前实现**、**迭代流程**和**历史设计**。代码行为与文档冲突时，以代码和测试为准，并在同一次改动中修正文档。

## 按读者路径

### 🚀 新人上手（按顺序读）

1. [`../README.md`](../README.md) — 项目定位、快速开始、构建与产物
2. [`architecture.md`](architecture.md) — 进程、端口、API、生命周期（**当前事实以此为准**）
3. [`development.md`](development.md) — 本地开发、测试、按功能定位代码
4. [`../agents.md`](../agents.md) — 设计哲学、代码约定、关键坑（人类与 AI 协作者通用）

### 📍 现在到哪了

| 文档 | 用途 |
| --- | --- |
| [`progress.md`](progress.md) | **进度与路线唯一真相源**：两条主线的阶段/里程碑状态、已完成、待开始优先级 |
| [`iterations/`](iterations/README.md) | 每个版本的目标、决策和验收记录（逐迭代一文件） |
| [`known-issues.md`](known-issues.md) | 已确认但未修复的实现缺陷登记表（现象、根因、影响、修复方向） |

### 🧭 方向与决策（为什么这么做）

| 文档 | 用途 | 维护时机 |
| --- | --- | --- |
| [`product-vision.md`](product-vision.md) | 主线一：桌面文件产品 → 网络云盘与内网协同 | 产品边界或路线变化时 |
| [`knowledge-platform.md`](knowledge-platform.md) | 主线二：企业知识平台架构、选型与里程碑 | 知识平台方向或选型变化时 |
| [`cloudreve-benchmark.md`](cloudreve-benchmark.md) | Cloudreve 对标、能力差距与可迁移设计 | 对标基线或优先级变化时 |
| [`technical-selection.md`](technical-selection.md) | 云盘演进的技术选型基线与阶段门禁 | 技术路线或首选方案变化时 |
| [`technology-evaluation.md`](technology-evaluation.md) | "成熟但先进"评估门禁、产品雷达和对象存储候选 | 依赖状态变化或生产选型复审时 |
| [`adr/`](adr/README.md) | 跨版本架构决策、备选方案和验证要求 | 重要决策提出、接受或被取代时 |

### 🔁 迭代与流程

| 文档 | 用途 |
| --- | --- |
| [`version-iteration.md`](version-iteration.md) | 迭代固定流程、版本工作区模板、Definition of Done（纯流程，不记事实） |

### 🧰 专题参考

| 文档 | 用途 |
| --- | --- |
| [`../knowledge/README.md`](../knowledge/README.md) | Python 知识服务：架构边界、API、测试、评测、Local Lab |
| [`macos-port.md`](macos-port.md) | macOS 平台差异、构建、Finder 集成与排障 |
| [`troubleshooting.md`](troubleshooting.md) | 日志读取和常见故障（Windows/macOS/Python 管线） |
| [`testing/knowledge-service-checklist.md`](testing/knowledge-service-checklist.md) | 知识服务手工验收清单 |
| [`../deploy/rustfs/`](../deploy/rustfs/README.md) | 固定版本的 RustFS 本地开发与一致性测试环境 |

## 历史资料

以下文档用于解释项目形成过程，不应直接作为新功能实现规范：

- [`archive/initial-concept.md`](archive/initial-concept.md)：更早的概念草案，包含未实现设想
- [`archive/windows-mvp-checklist.md`](archive/windows-mvp-checklist.md)：早期 Windows MVP 验收清单（Z 盘/Digest 时代，已被 NameSpace 替代）
- [`archive/knowledge-platform-roadmap.md`](archive/knowledge-platform-roadmap.md)：知识平台早期任务分解（选型已更新，以 knowledge-platform.md 为准）

## 文档维护约定

1. **单一真相源**：当前事实 → `architecture.md`；进度与路线 → `progress.md`；其余文档引用而不复制，避免多处漂移。
2. 新版本开始时，按 `version-iteration.md` 的模板在 `iterations/` 新建记录，先写目标、非目标和验收标准，再开始编码。
3. API、端口、配置字段、退出顺序或日志位置变化时，必须在同一次改动中同步相关文档。
4. 新发现的环境问题在修复后记录到 `troubleshooting.md`。
5. 已确认但未修复的实现缺陷登记到 `known-issues.md`；该表只登记现象、根因、影响和修复方向，实现方案仍写在迭代记录或 ADR 中。
6. 文档过时后移入 `archive/` 并在本页登记，不要原地修改历史计划来伪装现状。
