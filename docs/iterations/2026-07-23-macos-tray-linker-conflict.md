# 迭代：修复 macOS 菜单栏重复 AppDelegate 链接失败

> 日期：2026-07-23
> 状态：已完成（待 Mac 真机复验）
> 主题：修复 `darwin/universal` 桌面端链接阶段的 Objective-C 重复符号

## 用户问题

用户在 Apple Silicon Mac 上执行 `bash scripts/build-mac.sh`：Core、Wails 绑定生成和前端构建均成功，但桌面端编译在 x86_64 链接阶段失败：

```text
duplicate symbol '_OBJC_METACLASS_$_AppDelegate'
duplicate symbol '_OBJC_CLASS_$_AppDelegate'
ld: 2 duplicate symbols
clang: error: linker command failed with exit code 1
```

绑定生成阶段反复出现的 `Not found: time.Time` 是 Wails v2.13.0 的生成器提示，不是本次构建中止原因；真正的失败点是 clang/ld 的重复 Objective-C 类。

## 根因

- Wails v2.13.0 的 macOS 后端定义并使用 `AppDelegate` 管理 `NSApplication` 与主窗口。
- `github.com/getlantern/systray` v1.2.2 的 Darwin 实现也定义 `AppDelegate`，并调用 `setDelegate:` 与 `NSApp run`，假设自己拥有应用事件循环。
- EasyShare 的跨平台 `tray.go` 在 macOS 也导入了 `getlantern/systray`，两套实现因此进入同一最终二进制。
- Objective-C 类名在进程内是全局符号；即使改过链接参数，两份 `AppDelegate` 也不能安全共存。覆盖或忽略重复符号还会导致其中一方接管 delegate，产生窗口生命周期失效等运行期问题。

## 目标

1. macOS 桌面端不再链接 `getlantern/systray` 的 Darwin 实现。
2. 保留菜单栏图标、打开主窗口、服务状态和退出应用四项现有能力。
3. 不替换、不修改 Wails 的 `NSApplicationDelegate`，只把 `NSStatusItem` 挂到 Wails 已有的 AppKit 事件循环。
4. Windows 托盘行为保持不变，继续使用已经验证的 `getlantern/systray`。
5. 保持 `darwin/arm64`、`darwin/amd64` 和 `darwin/universal` 三种构建目标一致。

## 技术决策

### 1. 托盘实现按平台完整拆分

原先只按平台拆分图标，`startTray` 仍是跨平台代码，所以 Darwin 必然导入 systray。本次把完整 Windows 实现放入带 `//go:build windows` 的文件，确保 Darwin 依赖图中不再出现 systray。

### 2. macOS 使用最小 AppKit bridge

新增 EasyShare 自有的 Objective-C bridge：

- 用唯一类名 `EasyShareTrayController`，不定义 `AppDelegate`。
- 在主队列创建 `NSStatusItem` 和 `NSMenu`。
- 菜单动作通过 cgo 回调现有 Go 方法。
- 状态文字更新调度到 AppKit 主队列。
- 不调用 `setDelegate:`、`[NSApp run]` 或 `terminate:`，应用生命周期仍完全由 Wails 管理。

这比通过链接器掩盖重复符号更安全，也避免 fork 第三方依赖。

### 3. universal 桌面端必须捆绑 universal Core

原脚本先按宿主架构构建一次 Core，再构建 universal `.app`。在 Apple Silicon Mac 上，这会把只有 arm64 slice 的 `easyshare-core` 放进同时支持 arm64/x86_64 的应用包；应用经 Rosetta 以 x86_64 启动时将无法拉起 Core。

`scripts/build-mac.sh` 现在按 `WAILS_PLATFORM` 构建 Core：

- `darwin/universal`：分别构建 arm64、amd64，再用 `lipo -create` 合成 universal 二进制；
- `darwin/arm64`、`darwin/amd64`：只构建目标 slice；
- 其他值立即报错，避免桌面端和 Core 架构不一致。

## 代码影响

| 文件 | 实际变更 |
| --- | --- |
| `tray.go` | 删除无平台标签、会让 Darwin 导入 systray 的实现 |
| `tray_windows.go` | 承载 Windows 图标及完整 systray 菜单逻辑，行为保持不变 |
| `tray_darwin.go` | 承载 macOS cgo 回调、状态 channel 同步和 PNG 嵌入 |
| `tray_native_darwin.h/.m` | 新增不接管 AppDelegate/事件循环的 AppKit 状态栏 bridge |
| `tray_platform_test.go` | 防止 Darwin 再次导入 systray，禁止 bridge 接管应用 delegate |
| `scripts/build-mac.sh` | 按目标平台构建 Core；universal 使用 `lipo` 合并双架构 |
| `frontend/src/composables/__tests__/useEasyShare.test.ts` | 补齐既有拖放逻辑需要的 Wails `OnFileDrop`/`OnFileDropOff` 测试桩 |
| `docs/macos-port.md`、`docs/troubleshooting.md` | 更新平台结构、构建诊断和排障说明 |
| `README.md`、`docs/progress.md`、`AGENTS.md` | 同步路线图、架构约束和真实验证状态 |

不修改 Core API、前端类型、配置格式或网络协议。

## 验证结果

Windows 开发机已按项目完整顺序执行并通过：

```powershell
go build ./...
go test ./...
npm --prefix frontend run build
npm --prefix frontend test
wails build
go build -o build/bin/easyshare-core.exe ./cmd/core
```

其中前端测试最初暴露既有测试桩缺少 `OnFileDrop`/`OnFileDropOff`，补齐 mock 后 3 个测试文件、7 个测试全部通过；没有修改生产拖放逻辑。Windows `wails build` 同样会输出 `Not found: time.Time`，但随后成功完成链接，进一步确认该消息不是致命错误。

额外静态/交叉验证已通过：

- Darwin arm64 与 amd64 的依赖图均不包含 `github.com/getlantern/systray`；
- 两个架构均选中 `tray_darwin.go` 和 `tray_native_darwin.m`；
- Darwin arm64、amd64 Core 均可用 `CGO_ENABLED=0` 交叉编译；
- cgo 代码生成可识别 Objective-C bridge 与 Go 导出回调；
- `scripts/build-mac.sh` 通过 Bash 语法检查；
- `git diff --check` 通过。

Windows 无法最终链接 Cocoa/AppKit，因此仍需在 macOS 对本次修复执行最终复验：

```bash
go list -deps . | grep getlantern/systray
# 预期无输出；grep 返回 1 在这里代表检查通过

bash scripts/build-mac.sh
```

随后确认：

1. `build/bin/easyshare.app` 与 `build/bin/EasyShare.dmg` 正常产出；
2. `lipo -info build/bin/easyshare-core` 同时显示 `x86_64 arm64`；
3. 菜单栏图标可见，“打开主窗口”、服务状态和“退出 EasyShare”可用；
4. 关闭窗口后应用隐藏但 Core 继续运行；
5. Finder WebDAV 挂载与局域网传输没有新增回归。

## 排障方法（省的下次还有问题）

- 日志先看最后一个 `ERROR` 前的 clang/ld 输出；绑定生成的 `Not found: time.Time` 不等同于构建失败。
- 若再次出现 `duplicate ... AppDelegate`，运行 `go list -deps . | grep -E 'systray|wails'`，确认 Darwin 依赖图是否误引入了拥有应用 delegate 的托盘库。
- 不要用 `-Wl,-multiply_defined,suppress`、重命名 Wails 私有源码或覆盖 `NSApp.delegate` 绕过；这些方案可能链接成功，但窗口关闭、退出和 Wails 回调会在运行时失效。
- AppKit UI 只能在主线程操作；bridge 新增菜单或状态变化时必须继续使用 main queue。
- universal 构建会分别链接 arm64 与 x86_64；只要任一架构失败，最终 `.app` 都不会生成。可先用 `WAILS_PLATFORM=darwin/arm64` 缩短真机排障，再回到 universal 验收。

## 完成记录

已从架构层消除冲突：macOS 最终二进制只保留 Wails 的 `AppDelegate`，EasyShare 托盘作为 `NSStatusItem` 挂入现有 AppKit 生命周期；Windows 继续使用 systray。修复同时保证 universal `.app` 内的 Core 也是 universal。

用户日志中的 Go 1.26 和已安装的 Xcode Command Line Tools 都不是本次重复符号的根因，降级 Go 或重复安装 Xcode 无法解决两个 Objective-C 类同名的问题。为保持可复现性仍建议使用 `go.mod` 声明的 Go 版本，但本次正确修复是平台隔离而不是工具链降级。

当前状态为“代码与 Windows/交叉验证完成，待 Mac 真机重新构建及运行验收”。
