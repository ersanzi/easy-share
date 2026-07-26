# 2026-07-27 任务中心接收文件夹跟随设置修复

## 用户问题

任务中心的“打开接收文件夹”没有稳定遵循设置页保存的接收目录。用户修改并保存接收目录后，按钮仍可能打开桌面端启动时加载的默认目录，造成设置与实际动作不一致。

## 目标与非目标

### 目标

- 每次点击“打开接收文件夹”时，以当前持久化配置中的 `receiveDir` 为准。
- 配置读取失败时明确返回错误并写入桌面日志，不静默打开旧目录。
- 增加自动化回归测试，覆盖“内存缓存为旧值、磁盘配置为新值”的场景。
- 保持 Windows Explorer 与 macOS Finder 的现有平台实现不变。

### 非目标

- 不增加任意路径打开接口，避免前端直接控制系统文件管理器路径。
- 不改变单条已完成任务的“打开文件”行为。
- 不改变“另存”任务的实际保存位置；本按钮仍代表设置中的默认接收目录。

## 根因与技术决策

调用链为：

```text
TransferList.vue
  → core.openReceiveFolder()
  → App.OpenReceiveFolder()
  → fsutil.OpenFolder()
```

`App.OpenReceiveFolder()` 原先直接读取 `a.config.ReceiveDir`。该字段是桌面端进程内的配置快照，打开动作本身没有保证使用当前持久化设置，因此可能与设置页展示及 Core 热加载后的值不一致。

修复采用点击时调用 `config.Load(a.configPath)` 的方式读取当前配置，再把其中的 `ReceiveDir` 交给 `fsutil.OpenFolder`。`config.Load` 与 `config.Save` 共用配置包互斥锁，可避免读取到配置原子替换过程中的中间状态。读取或校验失败时不回退旧缓存，防止悄悄打开错误目录。

该修改不改变 `OpenReceiveFolder()` 的 Wails 导出签名，因此不需要调整前端服务封装或手工修改生成绑定。

## 代码影响

- `app.go`：打开接收目录前重新加载当前配置，并保留统一错误记录。
- `app_settings_test.go`：验证磁盘新配置优先于桌面端旧缓存，并验证当前配置损坏时不会调用系统打开动作。
- `docs/troubleshooting.md`：补充接收目录不一致时的配置与日志检查方法。

## 自动化回归测试

新增两个根包测试：

1. `TestOpenReceiveFolderUsesLatestPersistedSetting`：先让桌面端缓存旧目录，再把新目录写入同一配置文件，断言打开动作收到的是新目录。
2. `TestOpenReceiveFolderRejectsInvalidCurrentConfig`：写入无法通过校验的当前配置，断言返回带接收目录上下文的错误，且不会调用系统目录打开函数。

## 验证结果

2026-07-27 锁定最新提交 `ed25e70`，并保留本次新增的 `app_settings_test.go` 后，在当前 Windows 工作区执行完整流水线：

| 验证命令 | 结果 |
| --- | --- |
| `go build ./...` | 通过 |
| `go test ./...` | 通过，包含上述 2 个新增回归测试 |
| `npm --prefix frontend run build` | 通过，`vue-tsc` 与 Vite 构建成功 |
| `npm --prefix frontend test` | 通过，5 个测试文件、19 个测试 |
| `wails build` | 通过，生成 Windows `easyshare.exe` |
| `go build -o build/bin/easyshare-core.exe ./cmd/core` | 通过，生成 Core 可执行文件 |

验证开始与结束时的 HEAD 均为 `ed25e708ef31d8cfe971d652c0f602490bb326e3`，期间没有再发生并行提交切换；最新首页改动、接收目录修复和新增回归测试共同通过了上述流水线。

## 手工验收

仍需在最新 Windows 客户端中完成一次交互验收：

1. 准备两个已存在且可访问的目录 A、B。
2. 在设置页把接收目录改为 A 并点击“保存设置”。
3. 切换到任务中心，点击“打开接收文件夹”，确认 Explorer 打开 A。
4. 返回设置页，把接收目录改为 B 并再次保存。
5. 不重启客户端，立即回到任务中心重复点击，确认 Explorer 打开 B 而不是 A 或默认下载目录。

macOS 后续真机复验时使用同一流程，预期 Finder 打开当前设置目录。

## 排障方法

1. 先确认设置页点击了“保存设置”，而不是只在目录选择器中选中后离开。
2. Windows 可只读取配置中的目录字段核对持久化值（完整配置含服务令牌，不要外发）：

   ```powershell
   (Get-Content "$env:LOCALAPPDATA\EasyShare\config.json" -Raw | ConvertFrom-Json).receiveDir
   ```

3. 运行定向回归测试：

   ```powershell
   go test . -run OpenReceiveFolder -v
   ```

4. 若仍打开旧目录，确认运行的是包含本修复的最新 `easyshare.exe`，并查看 `desktop.log` 中 `open receive folder` 对应错误。
5. 若配置损坏或目录已删除/无权限，返回设置页选择一个存在且可访问的目录并重新保存；不要通过回退进程内旧缓存掩盖问题。

## 回滚方式

恢复 `OpenReceiveFolder()` 直接读取内存配置即可回滚；无数据迁移、API 路由或前端绑定变化。

## 完成记录

- 代码修复与两条自动化回归测试已完成。
- Go、前端、Wails 桌面端与 Core 全套隔离构建验证已通过。
- Windows/macOS 文件管理器的真实交互仍需对应平台手工验收。
