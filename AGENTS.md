# AGENTS.md — EasyShare 项目开发指导

> 本文件是项目开发的通用指导，适用于任何参与本项目的人类开发者或 AI 助手。
> 最后更新：2026-07-21

## 产品定位与设计哲学

EasyShare 是面向普通消费者的 Windows 文件传输与云盘工具，对标百度网盘 / AirDrop 的体验水准。核心原则：

- **开箱即用**：用户不应看到端口、密钥、地址等基础设施细节。服务连接参数用编译期常量（`internal/cloud/defaults.go`），不暴露配置界面。
- **Windows 深度集成**：不仅限于应用内功能，要在资源管理器「此电脑」可见（Shell NameSpace），像 WPS 云盘一样融入系统。
- **macOS 简约视觉**：圆角卡片、毛玻璃、SF 排版风格。不接受 Windows Fluent 原生风，不接受补丁式中间态修复，要求从架构层面解决根因。
- **技术参数自动推断**：不让用户做不理解的技术选择（如 queryMode、fieldKey）。字段命名按角色/用途，不按实现位置。

## 技术栈

- **桌面端**：Wails v2.13.0 + Vue 3 + TypeScript（单窗口，Frameless）
- **后台服务**：Go 1.25 独立进程（easyshare-core.exe），HTTP API 仅监听 127.0.0.1
- **对象存储**：RustFS（S3 兼容），AWS SDK v2
- **安装包**：NSIS 3.x（project.nsi 需 UTF-8 BOM）
- **Shell 扩展**：MinGW g++ 编译 COM DLL（build/shellext/）

## 双进程架构

```
easyshare.exe (Wails 桌面端)
  ├── 自动探测/启动 easyshare-core.exe
  ├── 通过 HTTP 127.0.0.1:19080 与 Core 通信
  ├── 系统托盘（getlantern/systray，ICO 图标）
  └── Shell NameSpace 注册（此电脑入口）

easyshare-core.exe (后台服务)
  ├── UDP 设备发现 :9527
  ├── TCP 文件传输 :9528
  ├── WebDAV :19080（仅回环，无认证）
  ├── 云盘 API /api/cloud/*
  └── 云盘驱动器 WebDAV :19081（映射「此电脑」）
```

## 迭代工作流

每次迭代严格遵循以下流程：

1. **更新 `docs/progress.md`**：在「进行中」写入本次主题
2. **创建迭代文档** `docs/iterations/YYYY-MM-DD-主题.md`：记录用户问题、目标、技术决策、代码影响、排障方法
3. **实现代码**
4. **验证**：`go build ./...` → `go test ./...` → `npm run build`（含 vue-tsc）→ `wails build` → `go build -o build/bin/easyshare-core.exe ./cmd/core`
5. **更新文档**：progress.md 标记完成、迭代记录表追加行、README 路线图同步
6. **提交推送**（用户明确要求时才提交）

### 文档维护规则

- `docs/progress.md` 是进度唯一真相源，每次迭代开始和结束必须更新
- `docs/iterations/` 每个迭代一个文件，包含排障方法（"省的下次还有问题"）
- `README.md` 路线图与 progress.md 保持一致，不能过时
- 重要修改后把诊断方法/排障流程写入 iterations + troubleshooting

## 构建命令

```powershell
# 完整构建顺序（不可跳步）
go build ./...                                          # 编译检查
go test ./...                                           # Go 测试
npm --prefix frontend run build                         # 前端构建（含 vue-tsc 类型检查）
npm --prefix frontend test                              # 前端测试
wails build                                             # 桌面端 → build/bin/easyshare.exe
go build -o build/bin/easyshare-core.exe ./cmd/core     # Core → build/bin/easyshare-core.exe

# 安装包（可选）
wails build --nsis

# 全量流水线
powershell -ExecutionPolicy Bypass -File scripts/build.ps1
```

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

## 环境备忘

- Windows 10/11，bash 环境（Git Bash）
- heredoc (`go run - <<'EOF'`) 不生效，须写临时 .go 文件
- `copy` 命令不可用，用 `cp`
- Bash 工具用 `dir_path` 参数指定工作目录，不用 `cd /d`
- MinGW 8.1.0 在 `D:\Develop\mingw64`（Shell 扩展编译用）
- Docker v24.0.6 可用；RustFS 本地 127.0.0.1:9000，bucket "easyshare"
