# 迭代：拖拽发送（原生文件拖放 + 设备选择浮层）

> 日期：2026-07-20
> 阶段：阶段 2 — 产品体验完善
> 状态：已完成（已通过构建与启动验证，待真机双设备手工验收）

## 用户问题

此前发送文件必须先进入「附近设备」页、点「发送」、再走系统文件对话框挑选文件，路径太长。用户希望像 AirDrop / 百度网盘那样，直接把文件从资源管理器拖进 EasyShare 窗口就能发出去。

早期进度文档曾记录「Wails v2.13.0 无 OnFileDrop，需升级框架或走 WebView2 JS 桥接」。经核对 Wails v2.13.0 源码，这一判断有误——v2.13.0 已内置原生文件拖放支持，无需升级或自行桥接 COM。

## 目标

- 把文件拖入窗口即触发发送流程，无需打开文件对话框。
- 拖入后弹出设备选择浮层，列出在线设备，点选即发。
- 仅发送文件；拖入的文件夹被忽略，并给出明确提示。
- 体验连贯：浮层风格与现有 macOS 简约卡片一致。

## 技术决策

### 1. 用 Wails 原生拖放，而非自桥接 WebView2

Wails v2.13.0 提供：

- `options.DragAndDrop{ EnableFileDrop: true }`：开启窗口级文件拖放。
- `runtime.OnFileDrop(ctx, func(x, y int, paths []string))`：回调返回**真实绝对路径**（Windows 下由 WebView2 `ICoreWebView2File.GetPath()` 取得），不是浏览器里拿不到的伪 File 对象。

因此整条链路无需 JS 端处理 drop 事件，Go 端直接拿到路径，规避了 WebView 默认把拖入文件当导航/下载的问题。

> ⚠️ 关键坑（首版踩中）：**Go 端 `runtime.OnFileDrop` 只是 `EventsOn("wails:file-drop")` 订阅事件，它不会注册 WebView 的 `dragover`/`drop` DOM 监听器。** 真正注册 DOM 监听、调用 `ResolveFilePaths` 把文件路径解析出来再 post 给 Go 的，是**前端 JS 的 `OnFileDrop`**（`wailsjs/runtime/runtime`）。如果只在 Go 端调用 `runtime.OnFileDrop`，DOM 监听器从未注册，拖放完全无反应。正确做法是前端调用 JS `OnFileDrop(callback, useDropTarget)`。

### 2. 前端注册监听拿路径，Go 端只负责过滤目录

发送管线完全是「基于路径」的（`App.SendFile(peerID, path)` → Core `POST /api/transfers` → TCP 发送），所以拖放只需把路径交给前端、由前端决定发给谁。最终链路：

- 前端 `useEasyShare` 在 `onMounted` 调用 JS `OnFileDrop(handleFilesDropped, false)`：注册 WebView 的 DOM 拖放监听，`useDropTarget=false` 表示整个窗口都是拖放区（无需任何 CSS 标记），回调直接拿到真实路径数组。
- 回调里调用 Go 绑定方法 `ProcessDroppedFiles(paths)`：Go 端对每个路径 `os.Stat`，目录计入 `skippedDirs`、文件收集进 `files`，返回 `FilesDroppedEvent{Files, SkippedDirs}`。
- 前端把返回结果写入 `droppedFiles` / `skippedDirs`，触发设备选择浮层。

> 之所以保留 Go 端过滤：浏览器/WebView 拿到的只是路径字符串，无法判断是文件还是目录，只有 Go 能 `os.Stat`。

### 3. 前端：composable 管状态，浮层只负责展示与选择

- `useEasyShare` 里调用 JS `OnFileDrop(handleFilesDropped, false)` 注册监听、拿到路径后走 Go 过滤，卸载时 `OnFileDropOff()`。
- 新增 `DevicePicker.vue` 浮层：展示文件清单 + 忽略文件夹提示 + 在线设备列表；点设备触发 `sendDropped(peerId)`，循环 `core.send` 逐文件发送；发送中显示 spinner 并禁用交互。
- 浮层在 `App.vue` 顶层用 `v-if="droppedFiles.length"` 条件渲染，`position: fixed` 覆盖全窗口。

## 代码影响

| 文件 | 变更 |
| --- | --- |
| `main.go` | `options.App` 增加 `DragAndDrop: &options.DragAndDrop{EnableFileDrop: true}` |
| `app.go` | 新增绑定方法 `ProcessDroppedFiles(paths) FilesDroppedEvent`（`os.Stat` 过滤目录、返回文件列表与忽略数）；**不**在 Go 端注册 `runtime.OnFileDrop`（无效） |
| `frontend/src/composables/useEasyShare.ts` | 引入 JS `OnFileDrop/OnFileDropOff`；`onMounted` 注册监听、回调走 Go 过滤；新增 `droppedFiles`/`skippedDirs`/`dropSending` 状态、`handleFilesDropped`/`sendDropped`/`cancelDrop` 并暴露 |
| `frontend/src/types/core.ts` | 新增 `DroppedFiles` 接口 |
| `frontend/src/services/core.ts` | 新增 `processDroppedFiles` 绑定 |
| `frontend/src/components/DevicePicker.vue` | 新增设备选择浮层组件 |
| `frontend/src/App.vue` | 引入并条件渲染 `DevicePicker`，绑定 `pick`/`cancel` |
| `frontend/src/style.css` | 新增「拖拽发送：设备选择浮层」整段样式（遮罩、卡片、文件行、设备行、发送中） |

## 测试与验收

构建链路全部通过：

- `go build ./...` 编译通过。
- `npm run build`（含 `vue-tsc --noEmit` 类型检查）通过。
- `wails build` 产出 `build/bin/easyshare.exe`。
- `go build -o build/bin/easyshare-core.exe ./cmd/core` 产出 Core。
- 启动 `easyshare.exe`：成功连接 Core、命名空间注册、托盘就绪，`OnFileDrop` 注册无报错。

**待真机手工验收**（需两台同局域网设备）：

1. 双设备均运行 EasyShare，确认「附近设备」能看到对方。
2. 从资源管理器拖一个或多个文件进窗口 → 应弹出设备选择浮层，列出文件与在线设备。
3. 点选一台设备 → 文件开始发送，对方收到接收请求；浮层关闭。
4. 拖入含文件夹的混合选择 → 文件夹被忽略，浮层显示「已忽略 N 个文件夹」。
5. 无在线设备时拖入 → 浮层显示「暂无在线设备」空态。

> 说明：WebView2 的文件拖放源自 OLE `DoDragDrop` 文件数据对象，无法用 UI 自动化合成鼠标拖拽触发，故最终拖放动作需人工验证。

## 关键排障方法

- **拖入完全无反应（本次根因）**：检查是不是只在 Go 端调了 `runtime.OnFileDrop`。它只是 `EventsOn("wails:file-drop")`，**不会注册 WebView 的 DOM `drop` 监听器**，所以永远不会触发。必须由**前端** `import { OnFileDrop } from '.../wailsjs/runtime/runtime'` 并调用 `OnFileDrop(cb, useDropTarget)` 来注册 DOM 监听。
- **确认链路是否通**：拖入后看 `desktop.log` 是否出现 `file drop: N file(s), M dir(s) skipped`。有 → Go 绑定 `ProcessDroppedFiles` 被调到，问题在前端状态/浮层渲染；无 → DOM 监听没注册（回到上一条）或 `EnableFileDrop` 未生效。
- **`EnableFileDrop` 未生效**：确认 `main.go` 有 `DragAndDrop: &options.DragAndDrop{EnableFileDrop: true}` 且重新 `wails build`（它会设置 `window.wails.flags.enableWailsDragAndDrop = true`）。
- **只有特定区域能拖放**：`OnFileDrop(cb, useDropTarget)` 第二参为 `true` 时，只有带 CSS `--wails-drop-target: drop` 的元素才是拖放区；传 `false` 则整窗可拖放（本项目用 false）。
- **路径拿不到 / 只有文件名**：真实绝对路径来自 JS `OnFileDrop` 回调的 `paths`（WebView2 `ICoreWebView2File.GetPath()` 解析），不要用浏览器 `drop` 事件的 `DataTransfer`（WebView 里拿不到真实路径）。
- **发送失败**：浮层只负责选人，实际发送复用既有 `core.send(peerID, path)` 管线，排障同普通发送（看 Core 日志与对方接收状态）。

## 完成记录

- 2026-07-20：完成编码、构建与启动验证；progress.md 已更新（含修正「v2.13.0 无 OnFileDrop」的过时结论）。
- 2026-07-20：用户实测首版拖拽无效。定位根因——仅用 Go `runtime.OnFileDrop` 不会注册 WebView DOM 监听器。改为前端 JS `OnFileDrop(cb, false)` 注册监听 + Go 绑定 `ProcessDroppedFiles` 过滤目录，重新 `wails generate module` + 构建通过，待用户复测。
