# AGENTS.md — EasyShare 项目开发指导

> 本文件是项目开发的通用指导，适用于任何参与本项目的人类开发者或 AI 助手。
> 本文只保留**别处没有**的约定与坑；架构事实、构建细节、迭代流程分别以
> [`docs/architecture.md`](docs/architecture.md)、[`docs/development.md`](docs/development.md)、[`docs/version-iteration.md`](docs/version-iteration.md) 为准。
> 最后更新：2026-07-26

## 产品定位与设计哲学

EasyShare 是面向普通消费者的 Windows 文件传输与云盘工具（对标百度网盘 / AirDrop），并正演进为企业知识管理平台（见 [`docs/knowledge-platform.md`](docs/knowledge-platform.md)）。核心原则：

- **开箱即用**：用户不应看到端口、密钥、地址等基础设施细节。服务连接参数用编译期常量（`internal/cloud/defaults.go`），不暴露配置界面。
- **Windows 深度集成**：不仅限于应用内功能，要在资源管理器「此电脑」可见（Shell NameSpace），像 WPS 云盘一样融入系统。
- **macOS 简约视觉**：圆角卡片、毛玻璃、SF 排版风格。不接受 Windows Fluent 原生风，不接受补丁式中间态修复，要求从架构层面解决根因。
- **技术参数自动推断**：不让用户做不理解的技术选择（如 queryMode、fieldKey）。字段命名按角色/用途，不按实现位置。

## 架构与流程速查（详情见权威文档）

- **架构**：Wails 桌面端 + Go Core 双进程（HTTP/WebSocket 127.0.0.1:19080）+ Python 知识服务（FastAPI，独立进程）。进程图、端口、API 清单、生命周期 → [`docs/architecture.md`](docs/architecture.md)
- **迭代流程**：先在 `docs/progress.md`「进行中」登记主题 → 建 `docs/iterations/YYYY-MM-DD-主题.md` → 实现与验证 → 更新 progress/README → 用户明确要求时才提交推送。模板与 DoD → [`docs/version-iteration.md`](docs/version-iteration.md)
- **构建/测试命令**：完整清单 → [`docs/development.md`](docs/development.md)；全量流水线 `powershell -ExecutionPolicy Bypass -File scripts/build.ps1`；Python 侧 `knowledge/.venv/Scripts/python.exe -m pytest -m "not integration"`
- **文档规矩**：`docs/progress.md` 是进度与路线唯一真相源；架构/端口/API 变化必须同步 `architecture.md`；排障经验写入 `troubleshooting.md` 与迭代记录（"省的下次还有问题"）

**注意**：构建前须退出正在运行的 easyshare.exe，否则 Windows 锁定文件报错。

## 代码约定

### Go

- 新增代码使用中文注释
- Entity/Model 放在对应 internal 包，不建独立 module
- API 路由在 `internal/api/server.go`，handler 与 service 分层
- 错误处理：向上 wrap 时附加上下文（`fmt.Errorf("cloud upload: %w", err)`）
- `task.Store` 添加 `persist()` 调用时必须用显式 `mutex.Unlock()` 替代 defer（否则 persist 内部 RLock 死锁）

### 前端（Vue 3 + TypeScript）

- 状态管理集中在 `frontend/src/composables/useEasyShare.ts`
- API 调用统一走 `frontend/src/services/core.ts`（封装 Wails 绑定）
- 类型定义在 `frontend/src/types/core.ts`
- 样式写在 `frontend/src/style.css`（单文件，按功能分段注释）
- 组件放 `frontend/src/components/`，单文件 .vue

### Wails 绑定级联（重要）

新增/修改 Go 导出方法后必须：
1. `wails generate module`（或 `wails build`）重生成 TS 绑定
2. 同步更新 `frontend/src/types/core.ts`（结构体新字段）
3. 同步更新 `frontend/src/services/core.ts`（新 API 封装）
4. 否则 vue-tsc 报 TS2305/TS2339

## 关键坑与排障

| 问题 | 原因与解法 |
| --- | --- |
| 拖放无反应 | 必须前端 JS `OnFileDrop(cb, false)` 注册 DOM 监听，Go 端 `runtime.OnFileDrop` 只订阅事件不注册监听器 |
| 托盘图标不显示 | Windows SetIcon 需 ICO 字节，PNG 无效。用 `build/windows/icon.ico` |
| NSIS "Bad text encoding" | project.nsi 需 UTF-8 BOM（`printf '\xEF\xBB\xBF'`） |
| wails build 文件锁定 | 先 `Stop-Process -Name easyshare` |
| AWS SDK PutObject 非 seekable 流 | 须加 `v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware` |
| 此电脑入口显示名异常 | 删除 CLSID 下 LocalizedString/System.Category/TileInfo 等劫持显示名的旧值 |
| WebClient 剥离 DavWWWRoot 前缀 | webdav.Handler 用 `Prefix:"/"` 即正确，不要设 `/DavWWWRoot` |
| Explorer 缓存顽固 | 改注册表后须 Stop-Process explorer 重启，清 iconcache/thumbcache |
| macOS 链接重复 `AppDelegate` | Wails 与 getlantern/systray 的 Darwin 实现都会定义/接管 AppDelegate；macOS 不得导入 systray，必须使用只创建 `NSStatusItem` 的 `tray_native_darwin.m`，且不可用 linker suppress 掩盖 |

## 架构边界

- UI 不直接依赖 WebDAV；文件入口是 Core 提供的一种能力
- Core API 只监听 127.0.0.1，版本化路由
- 云端凭据不暴露给前端或 Shell 扩展
- 传输任务模型后续要统一表示上传/下载/同步/局域网直传
- `DriveMapped` 状态不扩展为所有云盘状态的总开关

## 提交规范

```
<type>: <中文简述>

<可选正文，说明 why 而非 what>
```

type 取值：feat / fix / refactor / chore / docs / test

示例：`feat: 拖拽发送 — 原生文件拖放 + 设备选择浮层`

### 双仓库推送

项目同时维护两个远程仓库，提交代码后**两个都要推送**：

- `origin` → Gitee（https://gitee.com/liilaifeng/easy-share.git）
- `github` → GitHub（ssh://git@ssh.github.com:443/ersanzi/easy-share.git）

```bash
git push origin dev
git push github dev
```

GitHub 仓库承载 macOS CI 构建（GitHub Actions），Gitee 为国内主仓库。tag 推送同理：`git push origin v1.0.0 && git push github v1.0.0`。

## 环境备忘

- Windows 10/11，bash 环境（Git Bash）
- heredoc (`go run - <<'EOF'`) 不生效，须写临时 .go 文件
- `copy` 命令不可用，用 `cp`
- Bash 工具用 `dir_path` 参数指定工作目录，不用 `cd /d`
- MinGW 8.1.0 在 `D:\Develop\mingw64`（Shell 扩展编译用）
- Docker v24.0.6 可用；RustFS 本地 127.0.0.1:9000，bucket "easyshare"
