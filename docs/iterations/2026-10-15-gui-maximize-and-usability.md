# 2026-10-15 GUI 体验统一优化（最大化 + 全局审查整改）

## 背景

用户反馈软件不支持窗口最大化，并要求统一审查 GUI 中其他不合理之处。本轮先做全局只读审查产出问题清单，再分三路并行整改。

## 完成内容

### 1. 窗口最大化/还原（P0 用户直接反馈）

- 无边框窗口（Frameless）此前只有最小化/关闭，无最大化能力。
- 标题栏新增「最大化/还原」按钮（`.window-controls`，最小化与关闭之间；macOS 沿用原生红绿灯自动隐藏），图标随 `WindowIsMaximised()` 状态切换。
- 拖拽区（`.drag-region`）支持双击切换最大化/还原。
- 仅改 `frontend/src/App.vue`，样式复用 `.win-btn`，main.go 窗口选项未动。

### 2. 统一确认对话框（替换原生 window.confirm）

- 新增 `ConfirmDialog.vue`（Teleport + 毛玻璃卡片 + 完整 ARIA + ESC/遮罩关闭 + 初始聚焦「取消」防误触发）与 `useConfirm.ts`（模块级单例 `confirm(opts): Promise<boolean>`，组件树外 composable 可用）。
- 替换全部 5 处 window.confirm（退出服务/删账号/插件权限/两处卸载插件），并为云盘删除新增带文件名的二次确认。
- 新增 5 个单测；修复 v-model 挂载即 open 时初始焦点不触发的真 bug。

### 3. 样式可读性与布局

- `:root` 补齐幽灵变量 `--text/--faint/--accent/--ink`（此前大量引用但未定义，`.crumb-sep` 等处直接失效）。
- 字号阶梯：55 处 <11px 全部抬升，最小 11px（仅 3 处 10px 徽章物理尺寸例外）；正文 13/辅助 12/徽章 11。
- 网盘列表删除写死 `max-height:320px`，改为 `:has()` + flex 自适应填满工作区，大屏不再留白。
- 补齐 `.admin-user-list/.admin-user-row` 缺失样式（此前裸渲染）。
- 删除死代码 `HelloWorld.vue`；合并重复定义的 `.progress-track` 与 `.cloud-toolbar`。
- 860px 窄断点从 `font-size:0`（连带隐藏徽章/图标）改为显式隐藏文字 span，徽章转右上角角标。
- `.plugin-switch` 动画 `left` → `transform: translateX`，补 `:focus-visible`。

### 4. 可访问性

- DevicePicker、会议海报弹窗：补 ESC 关闭 + role="dialog" + 打开时焦点管理。
- ActivityDrawer 关闭后焦点还原到触发按钮。
- 云盘文件夹行、会议条目、转写段落补 `tabindex + keydown.enter` 键盘可达。
- 排序 select / 搜索框补 aria-label；模板/音源 chip 补 `:aria-pressed`。
- 传输列表删除按钮 aria-label 带文件名；登录页聚焦第一个空字段；侧边栏导航全部补 `aria-current="page"`。

## 验证

- `npx vue-tsc --noEmit` ✅ 0 错误
- `npx vitest run` ✅ 8 文件 45 用例全过（含新增 5 项 ConfirmDialog）
- `npm run build` ✅ 通过
- Go 代码零改动，无需 wails build。

## 遗留（记录在案，下一轮候选）

- P2-1 暗色模式全量策略（当前 4 处局部 `prefers-color-scheme` 碎片，需先决策定位）。
- P2-2 圆角收敛为 8/12/16 三档 token。
- P2-5 工作区内容限宽统一（`.workspace > .card` 720px 只覆盖顶层）。
- P2-9 MeetingsPanel.vue 1490 行拆分。
- P1-5 云盘分享复制后保留链接 + 「已复制」反馈；P1-7 PeerList 发送按钮 busy 态；P1-12 KnowledgePanel aria-live 只挂最新一条；P2-7 空态/头像抽公共组件；P2-11 商城卡片窄断点。
