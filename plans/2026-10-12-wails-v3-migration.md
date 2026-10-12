# Wails v2 → v3 迁移评估与迁移预案

> 2026-10-12 桌面端选型重评估的沉淀。用户拍板两条结论：
> ① **不切 Tauri**（已评估，理由与重评估条件见 §8）；② **Wails v3 迁移后续肯定要做**——本文记录"为什么、什么时候、怎么做"。
> 状态：**已拍板·待触发**，触发条件见 §7；触发前不动主线代码。

## §1 背景

2026-10-12 选型重评估的三个子问题与结论：

1. **现有技术栈是什么**：Wails v2.14（Go 1.25）+ 系统 WebView2 + Vue 3/TS；Go 核心跑传输/WebDAV/S3/剪贴板/插件/听记采集，壳层管窗口/托盘/绑定。
2. **要不要换 Tauri**：不换（§8）。
3. **Wails v3 beta 值不值得跟**：beta 期不上主线，但特性方向与产品演化方向（双进程 → 服务形态）难得一致，定为**后续必做**。

## §2 v3 现状（2026-10-12 查证）

- 2026-08-02 官方宣布进入 Beta，当前迭代到 v3.0.0-beta.28——beta 期 API 仍在小步变动。
- 官方口径："beta software with a stable desktop API"，已有团队生产使用；正式 GA 时间未公布。
- 官方迁移指南明确迁移是 **"a real port"**（真正移植，非版本号变更），beta 期仅英文文档是真相源，翻译暂停。
- v2 仍是稳定线持续收 fix（v2.14 之后还有小版本），不弃养。

## §3 为什么现在不迁

1. 主线处于「等公司部署 IP」冻结期，重构壳层与部署准备完全背离。
2. beta.28 的迭代节奏说明 API 还会动；正式迁移等收敛。
3. 开发流程摩擦：AGENTS.md/troubleshooting 里的 v2 坑表（OnFileDrop、托盘 ICO、CLI 双位置、文件锁定）全部对应 v2 已知行为；v3 上每个任务都要现查英文文档，排障表约半数要重写。
4. 官方自己建议：现有 v2 应用保持 v2，直到新应用就绪。

## §4 特性 ↔ 项目映射（迁移动机，按价值排序）

| v3 能力 | 对应 EasyShare 的什么 | 价值判断 |
| --- | --- | --- |
| **Server build 模式**（同一套应用/服务代码跑无窗口服务形态） | `cmd/core` 双进程是手搓的；v3 把桌面/无头两形态做成框架能力 | **最有战略价值**——与私有知识服务器方向（knowledge-platform.md）天然契合，长期可统一桌面与服务的代码形态 |
| **一等公民多窗口** | `panel_windows.go` / `search_surface.go` / `internal/winui`（~1000 行）等手写面板 | **部分可替**——窗口管理简化；macOS 毛玻璃原生代码（`panel_darwin.m`）v3 给不了，仍保留 |
| **Services + 静态分析生成绑定**（保留注释与参数名） | AGENTS.md「绑定级联」痛点（TS2305/TS2339） | 生成质量提升，但 types/core.ts / services/core.ts 手动同步纪律不变 |
| 内置系统托盘 | `tray_windows.go` + `tray_native_darwin.m` | **价值有限**——内置托盘只覆盖基础菜单；带 hover 预览的自定义托盘仍需原生代码 |
| Taskfile 构建系统 | `scripts/build.ps1` 全量流水线 + NSIS + GitHub Actions | **是成本不是收益**——构建链重接一遍 |

## §5 迁移范围与成本预估（2026-10-12 实测行数）

| 层 | 规模 | 迁移时命运 |
| --- | --- | --- |
| `internal/` 19 包（transfer/cloud/task/api/clipboard/drive/spacedav/plugin/winui/knowledge 等） | **15,582 行** | 不动 |
| 会议听记 malgo 采集链路 | 不大但刚验收 | 不动 |
| 根目录壳层（app 绑定胶水/托盘/面板） | 6,364 行（其中原生 .m/.win32 代码部分可平移） | **重写主体**——Wails 耦合的窗口/事件/绑定/对话框代码 |
| 前端 | — | runtime 调用 API 更名 + 绑定重新生成 |
| 构建/CI | — | build.ps1 对接 wails3/Taskfile、NSIS 流程重验、双平台 workflow 调整 |

结论：动的是约 10–15% 的代码面（壳层 Wails 耦合部分 + 前端 runtime + 构建链），核心逻辑零改动。

## §6 Spike 清单（正式迁移前必做的四件验证）

在独立分支上完成，产出事实后再排迁移计划：

1. **壳层最小移植**：窗口 + 托盘 + 文件拖放（OnFileDrop 语义变化确认）在 v3 跑通。
2. **macOS CI 托盘验证**：用现有 GitHub Actions macOS workflow 验证 v3 Darwin 构建——确认 AppDelegate 冲突是否消失、`tray_native_darwin.m` 能否删除或简化。
3. **server build 对比 `cmd/core`**（关键实验）：v3 无头形态能否替代/统一现有 Core 进程部署方式；能统一则迁移收益上调，不能则按纯桌面框架升级看待。
4. **绑定重生成**：v3 services 生成 TS 绑定后 vue-tsc 全绿，评估 types/core.ts、services/core.ts 的同步工作量变化。

## §7 触发条件（满足其一开始正式迁移）

1. **v3 进入 RC/GA**（首选）；
2. **公司部署里程碑落地后出现空窗期**（若 v3 当时仍未 GA，以 spike 结论决定是否提前）；
3. 提前触发例外：**Wails v2 停止维护/安全漏洞无人修**——此时直接启动迁移，接受 beta 成本。

期间动作：v2 线跟进小版本（2.14 之后仍有修复）；情报扫描轴③继续跟踪 v3 迭代（现有人肉规则已覆盖）。

## §8 Tauri 替代方案结论（2026-10-12 已评估：不切换）

代码事实：核心逻辑 15.6k 行 Go vs 壳层 6.4k 行，Tauri 能替换的只是最薄一层。两条切换路径都不划算：

- **全量 Rust 重写**：传输/WebDAV/S3/剪贴板/插件全重写；刚验收的会议听记 malgo 采集要换 cpal 重验——刚测稳的代码推倒重来，纯负风险。
- **Go core 做 sidecar**：Rust 壳 + Go 核 + HTTP 桥双语言双工具链；"端到端 Go + 绑定自动生成"的类型安全优势没了；构建/调试/CI 全复杂化，收益只剩壳 exe 几 MB（交付主体是 core exe，量级不变）。

逐条对照 v2 痛点：Tauri 真正解决的只有 macOS 托盘冲突（已用 NSStatusItem 原生方案解决且代码可平移）；WebView2 依赖照旧；TS 类型手动同步比 wails generate 更繁琐；工具链坑换样不消失。

**Tauri 重评估条件**（满足其一才回头看）：Wails v2 停止维护且 v3 不可用；移动端原生客户端进路线图；签名自动更新成硬需求且自研 `internal/update` 不敷使用；低配机内存/体积成为实测投诉。

## §9 迁移期已知风险预案

- WebView2 依赖问题照旧（v3 Windows 同为 WebView2），LTSC/精简系统 bootstrapper 兜底逻辑平移。
- 绑定级联纪律不变：新 Services 导出后仍须重生成 + 同步 types/core.ts、services/core.ts。
- 双进程架构（19079/19080）不因 v3 改变；`task.Store` persist 锁约定、显式 Unlock 等约定平移。
- AGENTS.md 坑表预计约半数条目要按 v3 重写；迁移迭代里必须同步更新，"省的下次还有问题"。
- v2 的 OnFileDrop 坑（前端注册监听）在 v3 的 API 语义需在 spike 中确认。

## §10 参考

- [Wails v3 Beta 官方博客](https://v3.wails.io/blog/wails-v3-beta)（2026-08-02）
- [v3 文档](https://v3.wails.io/) · [v2→v3 迁移指南](https://v3.wails.io/migration/v2-to-v3/)
- 上游版本跟踪：情报扫描轴③（docs/intelligence/ 各 digest，2026-09-03 起持续记录 v3 beta 迭代）
