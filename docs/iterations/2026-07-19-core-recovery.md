# Core 异常恢复

日期：2026-07-19
阶段：阶段 2 — 产品体验完善

## 目标

让 EasyShare 在 Core 进程崩溃或异常退出后能自动恢复，同时清理上次崩溃遗留的网络驱动器映射，避免用户看到"死"盘符。

## 问题分析

当前架构的薄弱点：

1. **无 watchdog**：`EnsureCore` 是一次性启动器，Core 崩溃后无人重启
2. **残留映射**：Core 崩溃时 WebDAV 服务消失，但 `net use Z:` 映射可能残留，资源管理器中显示为不可访问的盘符
3. **配置热加载缺失**：设置页保存后调用 `/api/config/reload`，但 Core 尚未实现该端点

## 设计决策

### 1. 启动时残留映射清理

在桌面端 `Startup` 中，EnsureCore 之前：
- 调用 `net use <letter>` 检测是否存在指向 EasyShare WebDAV 地址的映射
- 若存在则 `net use <letter> /delete /y` 清理
- 清理失败不阻塞启动（仅记录日志）

这确保每次启动都是干净状态，Core 启动后由前端自动重新映射。

### 2. Core 健康 Watchdog

在 `app.go` 中启动后台 goroutine：
- 每 5 秒调用 `CoreHealthy` 探测
- 连续 3 次失败（15 秒无响应）判定 Core 已死
- 自动调用 `EnsureCore` 重启，重建 Client
- 重启成功后更新托盘状态
- `quitting` 标志为 true 时停止 watchdog

### 3. /api/config/reload 端点

Core API 新增 `POST /api/config/reload`（Bearer 认证）：
- 重新从磁盘加载 config.json
- 更新 DeviceName（影响 discovery 广播）
- 不重启 WebDAV/Discovery/Transfer（端口变更需重启 Core，当前不支持）
- 返回 200 表示已重载

## 实现任务

| 状态 | 任务 |
| --- | --- |
| 待完成 | app.go：启动时清理残留映射 |
| 待完成 | app.go：watchdog goroutine（健康探测 + 自动重启） |
| 待完成 | api/server.go：/api/config/reload 端点 |
| 待完成 | 构建验证 |
