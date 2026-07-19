# EasyShare 系统托盘

> 日期：2026-07-19
> 状态：进行中
> 主题：关闭窗口时最小化到系统托盘，后台持续运行

## 用户问题

- 当前关闭 EasyShare 窗口后，桌面进程退出（Core 保持运行），用户失去了查看状态和管理传输的入口。
- 用户习惯云盘类工具"关窗口不等于退出"：窗口隐藏到托盘，服务继续运行，需要时随时恢复。
- 没有托盘图标时，用户无法快速判断 EasyShare 是否在运行，也无法快捷执行"退出全部服务"。

## 目标

- 点击窗口关闭按钮（X）时，窗口隐藏到系统托盘，Core 和所有服务继续运行。
- 托盘区显示 EasyShare 图标，右键菜单提供：打开主窗口、服务状态提示、退出全部服务。
- 双击托盘图标恢复主窗口。
- 窗口隐藏期间，前端降低状态轮询频率以节省资源。
- "退出全部服务"仍执行完整有序退出流程，并真正关闭应用。

## 非目标

- 本次不实现托盘气泡通知（如"收到新文件"）。
- 本次不实现托盘图标动态状态变化（如传输中显示进度）。
- 本次不实现"关闭时询问"设置项；默认行为固定为最小化到托盘。
- 本次不修改 Core 生命周期；Core 的启停逻辑不变。

## 设计决策

### 1. 使用 getlantern/systray 库

Wails v2 没有内置系统托盘支持。选择 `github.com/getlantern/systray`：
- 纯 Go 实现，Windows 上使用 Win32 Shell_NotifyIcon API
- 在 Windows 上可从非主 goroutine 运行
- API 简洁：SetIcon、AddMenuItem、ClickedCh channel
- 社区广泛使用，与 Wails v2 兼容良好

### 2. 关闭拦截与退出标志

在 `options.App` 中设置 `OnBeforeClose`：
- 若 `quitting` 标志为 false：隐藏窗口，返回 true（阻止关闭）
- 若 `quitting` 标志为 true：允许关闭，执行正常退出流程

`QuitApp()` 方法设置 `quitting = true` 后调用 `runtime.Quit(ctx)`。

### 3. 托盘菜单结构

```
EasyShare 0.1.0
─────────────
打开主窗口
─────────────
服务状态：运行中 / 已停止
─────────────
退出 EasyShare
```

- "打开主窗口"：调用 `runtime.WindowShow(ctx)` + `runtime.WindowSetAlwaysOnTop` 闪烁恢复
- "服务状态"：只读展示，根据最近一次 snapshot 的 core 字段判断
- "退出 EasyShare"：先调用 ShutdownAll 停止 Core，再设置 quitting 标志并退出

### 4. 前端轮询降频

使用浏览器 Page Visibility API（`document.visibilitychange`）：
- 窗口可见时：保持 1 秒轮询
- 窗口隐藏时：切换到 5 秒轮询
- 窗口恢复可见时：立即刷新一次并恢复 1 秒轮询

这比通过 Wails Events 通知更简单可靠，且 WebView2 原生支持 visibility 事件。

### 5. 图标

复用 `build/appicon.png`，通过 `//go:embed` 嵌入。systray 在 Windows 上接受 PNG 字节。

## 实现任务

| 状态 | 任务 |
| --- | --- |
| 已完成 | 添加 getlantern/systray 依赖 |
| 已完成 | 创建 tray.go：托盘初始化、图标、菜单和事件循环 |
| 已完成 | 修改 main.go：添加 OnBeforeClose 拦截 |
| 已完成 | 修改 app.go：添加 quitting 标志、ShowWindow、QuitApp 方法 |
| 已完成 | 修改前端 useEasyShare.ts：visibility 感知轮询降频 |
| 已完成 | 构建验证：go test + npm test + wails build --nsis 全部通过 |
| 待完成 | 手工验收（托盘图标、隐藏/恢复、退出流程） |
| 已完成 | 更新 README、架构文档和进度文档 |

## 兼容与迁移

- 不修改 config.json、Core API 或网络协议。
- 不影响 NSIS 安装包（托盘是桌面进程行为，不涉及 Core）。
- 旧前端与新桌面端可共存：旧前端不感知 visibility，仍 1 秒轮询，功能不受影响。
- `ShutdownAll` 行为不变；退出流程仍是：取消映射 → 停止 WebDAV → 停止 Core → 退出桌面。

## 测试计划

### 手工验收

1. 启动 EasyShare，确认托盘区出现图标。
2. 点击窗口 X 按钮，窗口消失，托盘图标仍在，Core 服务继续运行。
3. 双击托盘图标，窗口恢复。
4. 右键托盘图标，菜单显示正确；点击"打开主窗口"恢复窗口。
5. 点击"退出 EasyShare"，确认有序退出：映射移除、Core 停止、托盘图标消失、进程退出。
6. 窗口隐藏期间，通过任务管理器确认 CPU 占用明显低于窗口可见时。

### 构建验证

```powershell
go test ./...
npm --prefix frontend test
npm --prefix frontend run build
powershell -ExecutionPolicy Bypass -File scripts/build.ps1
```

## 完成记录

（待验收后填写）
