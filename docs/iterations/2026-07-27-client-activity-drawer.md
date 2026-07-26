# 2026-07-27 客户端全局活动抽屉与统一任务中心前端一期

## 用户问题

客户端已有局域网传输页和网盘页内上传进度，但任务反馈分散、只能在特定页面查看，也无法用同一套语义展示云上传、云下载及后续同步/文档处理。用户认可参考 WPS 的统一文件工作台方向，并要求提交设计后开始实施。

## 目标与非目标

### 目标

- 在任意页面提供全局活动入口和右侧活动抽屉。
- 统一展示局域网发送、局域网接收、云上传、云下载四类任务。
- 支持 `queued`、`paused`、`waiting_network`、`cancelled` 等统一状态文案和视觉反馈。
- 将原“传输任务”页升级为统一任务中心，同时保留批次、接收确认、打开文件、删除历史等已有能力。
- 继续以 Core 快照为可靠真相源，以 `transfer.updated` 事件作为快速更新路径。
- 兼容旧持久任务中缺失 `kind` 的数据。

### 非目标

- 本轮不实现暂停、恢复、重试、取消 API；后端未提供能力时不展示无效按钮。
- 不引入新的前端任务状态仓库，不让 Vue 成为任务真相源。
- 不实现首页最近文件、全局拖放目的地或 CfAPI/FileProvider；这些分别属于 C1.3、C1.4 和 C5。
- 不更换 Wails、Vue、Go Core 或现有事件/快照架构。

## 技术决策

### 1. 展示语义集中管理

前端增加任务展示辅助模块，集中维护类型推断、状态文案、进度、优先级和排序。活动抽屉与完整任务页共用同一套函数，避免同一任务在两个入口显示不同语义。

### 2. 旧任务兼容

`kind` 在前端契约中先保持可选：缺少 `kind` 时根据 `direction=send/receive` 推断为 `lan_send/lan_receive`。未知状态保留可读回退，不因旧 `history.json` 阻塞整个列表。

### 3. 活动排序

默认顺序固定为：

1. 正在进行：`running`、`accepted`、`queued`；
2. 需要处理：`pending`、`paused`、`waiting_network`、`failed`；
3. 最近完成：`completed`、`rejected`、`cancelled`。

同一分组按 `updatedAt`（回退 `createdAt`）倒序。抽屉只展示优先级最高的最近 8 项，完整任务中心保留全部历史和批次分组。

### 4. 不伪造操作能力

当前统一任务契约只有创建/更新基础接口，尚未形成面向 UI 的 `canPause/canRetry/canCancel` 能力。活动抽屉只提供关闭与“查看全部”，已有局域网接收确认和终态记录操作继续留在任务中心。

### 5. 云上传只保留 Core 任务反馈

云上传进度迁移到 Core 后，移除 `CloudPanel` 自己维护的临时上传队列和旧 `cloud-upload-progress` 订阅，避免两套任务状态。云上传完成时由全局任务变化触发网盘列表刷新。

## 代码影响

- `frontend/src/types/core.ts`：统一任务类型与状态契约。
- `frontend/src/utils/tasks.ts`：任务展示、排序和兼容推断。
- `frontend/src/components/ActivityDrawer.vue`：全局活动抽屉。
- `frontend/src/App.vue`：活动入口、数量摘要和任务中心导航。
- `frontend/src/components/TransferList.vue`：四类任务和扩展状态展示。
- `frontend/src/components/CloudPanel.vue`：移除页面内临时上传队列。
- `frontend/src/style.css`：活动抽屉与统一任务状态样式。
- `frontend/src/components/__tests__/`：活动抽屉与任务中心回归测试。

## 测试计划

```powershell
npm --prefix frontend test
npm --prefix frontend run build
go build ./...
go test ./...
wails build
go build -o build/bin/easyshare-core.exe ./cmd/core
```

重点覆盖：

- 活动任务按优先级和更新时间排序，最多展示 8 项；
- 云上传/云下载使用面向用户的文案；
- 等待网络、暂停、取消和失败状态可读；
- “查看全部”、关闭、背景点击和 Escape 行为；
- 旧任务缺少 `kind` 时仍按局域网方向正确渲染；
- `pending + lan_receive` 的接收/另存/拒绝操作不回归。

## 排障方法

1. **抽屉任务不更新**：先检查 Core `/api/tasks` 快照，再检查 `core-event` 中是否收到 `transfer.updated`；事件只负责快速路径，5 秒快照轮询必须能校准。
2. **旧任务显示类型错误**：检查持久记录是否缺少 `kind`，确认兼容推断只根据 `direction` 映射到局域网发送/接收，不把空方向猜成云任务。
3. **云上传完成但网盘列表没刷新**：确认完成事件的 `kind=cloud_upload`、`status=completed` 和 `updatedAt` 已变化，再检查 `CloudPanel.refresh()` 是否在网盘页挂载时被调用。
4. **进度超过 100% 或出现 NaN**：前端只做显示夹取；根因应检查 Core 的 `transferredBytes <= totalBytes` 校验，不在 UI 隐藏非法持久状态。
5. **新增 Go 字段前端不可见**：运行 `wails generate module` 或 `wails build`，并同步 `types/core.ts`；手写类型只用于应用层稳定契约，不能替代绑定更新。
6. **抽屉遮挡或无法关闭**：检查 z-index、背景 `@click.self`、Escape 监听是否随组件卸载，以及 macOS 52px 窗口控制条适配。

## 回滚方式

- 移除 `ActivityDrawer` 和 App 全局入口即可恢复原导航，不影响 Core 任务存储。
- `TransferTask.kind` 为可选附加字段，旧前端可忽略；无数据库迁移。
- 如统一云上传链路需要单独回滚，可暂时恢复旧上传进度事件，但不得同时让两套状态长期并存。

## 完成记录

待实现与验证完成后补充。