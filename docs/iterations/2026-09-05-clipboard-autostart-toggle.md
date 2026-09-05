# 剪切板插件「开机自动记录」开关（批次 3 余项）

- 日期：2026-09-05（同日第三切片）
- 任务来源：`docs/tasks.md` 待办池 P2（progress.md 待开始 #5：插件批次 3 余项）
- 状态：**已完成**（go build/test 全绿；真机开关冒烟归入真机验收欠账，见 §5）

## 1. 边界确认（任务要求先读两份插件迭代文档）

读 [2026-08-31-plugin-system](2026-08-31-plugin-system.md) 与
[2026-09-01-clipboard-flagship-panel](2026-09-01-clipboard-flagship-panel.md) 后确认：

- **录制随应用启动恢复早已存在**：08-31 起 `initPluginSystem` → `syncClipboardSurface`
  就会在插件在场时 `clipSvc.Start()`——"应用一开录制就在"不是缺口。
- **真正的缺口在 OS 层**：`随桌面端退出而停——「开机自启记录」是独立特性`（08-31 文档原话）。
  PC 重启后要恢复记录，前提是 EasyShare 随系统自启；而自启此前只有 NSIS 安装时
  的一次性问句（HKCU Run 键），应用内**既看不到也改不了**——跳过问句的用户永远
  丢失开机记录，且无从知晓因果。
- 结论：特性 = 给剪切板插件一个**一等公民「开机自动记录」开关**，在应用内读写
  OS 自启，把「开机 → 自启 → 录制」链路闭环并可视化。

## 2. 实现

| 位置 | 内容 |
| --- | --- |
| `internal/clipboard/autostart.go`（新） | 平台无关逻辑：Run 键操作抽象为 `runKey` 接口（单测注入 mock，绝不写真注册表）；读/写/删三操作；值名 `EasyShare` 与 NSIS `${INFO_PRODUCTNAME}` **同名同键**——开关覆盖安装器写入，卸载器删除时自然清理 |
| `internal/clipboard/autostart_windows.go`（新） | HKCU `...\CurrentVersion\Run` 实现（x/sys registry，用户级无提权）；值=引号包裹的当前 exe 路径；删除时"值不存在"按 `ERROR_FILE_NOT_FOUND` 精确豁免 |
| `internal/clipboard/autostart_other.go`（新） | 非 Windows stub（`autoStartSupported=false`，UI 隐藏开关）；macOS LaunchAgent 留待 darwin 真机批次 |
| `internal/clipboard/autostart_test.go`（新） | mock 键 3 用例（读空键/引号路径/删除语义） |
| `appplugin.go` | `clipboard.settings` 能力扩展：GET 返回 `autoStart`/`autoStartSupported`（OS 为唯一真相源，不落 settings.json）；POST 接受 `{autoStart}` |
| `plugins/clipboard/*` | 侧栏「设置」区新增开关行（键盘可达 Enter/Space）；不支持平台整行隐藏；manifest **2.0.2 → 2.1.0** |

权限面零新增：复用 `clipboard.read`（与 paused 开关同一语义）。

## 3. 语义约定

- 开关只管「开机是否自启并继续记录」；「运行中是否记录」仍由既有「暂停记录」管——
  两个开关正交，互不覆盖。
- 自启指向 `os.Executable()` 当前路径；安装目录移动后重开开关即自愈。
- 录制恢复不依赖此开关的存储：`AutoStartEnabled()` 每次 GET 现查注册表，
  避免注册表与 settings.json 双真相源漂移。

## 4. 验证

- `go build ./...` 全绿；`go test ./...` 全绿（含新增 3 用例）。
- 插件 JS `node --check` 通过；面板形态（`?panel=1`）整页独立渲染，不受侧栏改动影响。
- 真机冒烟（点开关→查 Run 键→重启验证）归入真机验收欠账——与既有"插件系统真机
  UI 验收"同批做。

## 5. 分发与遗留

- **存量安装**：种子只落首次启动，已装用户拿不到 2.1.0——需 superadmin 走商城发布
  一次（与日常插件更新同链路）；新安装直接种子 2.1.0。
- 批次 3 余项仅剩：AI 周报接知识服务。
- macOS「开机自动记录」待 darwin 批次补 LaunchAgent 实现（UI 已按 supported 隐藏）。
