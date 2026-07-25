# EasyShare

EasyShare 是一个面向 Windows 10/11 与 macOS 的局域网文件传输与云盘桌面应用，并正在向企业知识管理平台演进。它由 Wails/Vue 桌面界面和独立 Go Core 进程组成，可将本地共享目录映射为「此电脑」入口（macOS 上为 Finder 挂载卷），并接入 RustFS 云盘。macOS 移植说明见 [`docs/macos-port.md`](docs/macos-port.md)。

## 当前基线

当前代码处于 **阶段 2：产品体验完善（0.1.x 开发基线）**：

- UDP 局域网设备发现 + TCP 流式文件发送/接收
- 拖拽发送：文件/文件夹拖入窗口即弹设备选择浮层，点选即发（文件夹自动打包传输）
- 网盘功能（RustFS）：文件/文件夹上传、列表、下载、删除、分享链接，实时进度，拖拽上传，以及图片/PDF/限量 UTF-8 文本在线预览
- 系统文件入口：Windows Shell NameSpace 委托 WebDAV UNC；macOS Finder 挂载 WebDAV 卷
- 系统托盘/菜单栏（Windows systray、macOS 原生 AppKit）+ Frameless 无边框窗口 + macOS 简约风格 UI
- 设置页、传输历史、另存为、多文件发送、速度高亮
- Core 异常恢复（watchdog + 配置热加载）
- Windows NSIS 安装包 + 开机自启动；macOS `.app`/DMG 构建脚本

后续版本开始前请先阅读 [`docs/version-iteration.md`](docs/version-iteration.md)。

长期产品方向是演进为企业知识管理平台：在文件采集与云盘存储之上，构建解析清洗、知识库（RAG）、AI 写作辅助，并通过 WPS 插件交付；详见 [`docs/knowledge-platform.md`](docs/knowledge-platform.md)。原网络云盘与内网协同方向见 [`docs/product-vision.md`](docs/product-vision.md)。

## 环境要求

- Windows 10/11，或 macOS（构建 `.app` 需要 Xcode Command Line Tools）
- Go 1.25（以 `go.mod` 为准）
- Node.js 与 npm
- Wails CLI 2.13.0
- NSIS 3.x（仅 Windows 安装包需要，`winget install NSIS.NSIS`）
- Microsoft WebView2 Runtime（仅 Windows）
- Windows `WebClient` 服务（仅 Windows「此电脑」入口需要）

安装 Wails CLI：

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0
```

安装依赖：

```powershell
go mod download
npm ci --prefix frontend
```

## 开发运行

先构建 Core，再启动 Wails 开发模式：

```powershell
go build -o build/bin/easyshare-core.exe ./cmd/core
$wails = Join-Path (go env GOPATH) 'bin\wails.exe'
& $wails dev
```

只运行 Core：

```powershell
go run ./cmd/core
```

正常使用时只启动 `easyshare.exe`，不要手动同时启动 `easyshare-core.exe`；桌面进程会自动探测和启动 Core。

## 测试与生产构建

Windows 完整流水线：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/build.ps1
```

流水线依次执行 Go 测试、前端测试、TypeScript/Vite 构建、Core 编译和 Wails production 构建（含 NSIS 安装包）。产物为：

```text
build/bin/easyshare.exe
build/bin/easyshare-core.exe
build/bin/EasyShare-amd64-installer.exe
```

构建前应先退出正在运行的 EasyShare，否则 Windows 可能锁定可执行文件。

macOS 在 Mac 或 GitHub Actions macOS runner 上构建：

```bash
bash scripts/build-mac.sh
```

默认产出 universal `.app`/DMG，并用 `lipo` 合成同时包含 arm64 与 x86_64 的 Core：

```text
build/bin/easyshare-core
build/bin/easyshare.app
build/bin/EasyShare.dmg
```

仓库的 `.github/workflows/build-mac.yml` 有三种主要使用方式：

1. 推送 `dev`：自动构建，完成后在该次 Actions 运行的 **Artifacts** 下载 `EasyShare.dmg`、`EasyShare.app.zip` 和 `SHA256SUMS.txt`；
2. `workflow_dispatch`：在 Actions 页面手动运行，并选择 `darwin/universal`、`darwin/arm64` 或 `darwin/amd64`；
3. 推送 `v*` tag：自动构建并创建 GitHub Release，发布同一组安装包与校验文件。

日常开发流程是先同时推送 Gitee 与 GitHub：

```bash
git push origin dev
git push github dev
```

随后等待 GitHub Actions 自动构建，并从 Artifacts 下载 DMG。发布版本时把 tag 同时推送到两个仓库；GitHub 上的 tag 构建成功后会自动创建 Release。macOS 详细构建与排障见 [`docs/macos-port.md`](docs/macos-port.md)。

版本阶段约定：`test` 仅用于流水线冒烟，`preview` 用于功能仍在完善且尚未完成全部真机验收的公开预览，`beta` 用于主要功能稳定后的扩大测试，`rc` 用于正式版候选。所有带 `-` 的 SemVer 预发布 tag（如 `v0.1.0-preview.1`）都会自动标记为 GitHub Prerelease；当前阶段使用 `preview`。

## 运行数据

| 内容 | 默认位置 |
| --- | --- |
| 配置 | `%LOCALAPPDATA%\EasyShare\config.json` |
| 日志 | `%LOCALAPPDATA%\EasyShare\logs` |
| 接收文件 | `%USERPROFILE%\Downloads\EasyShare` |
| WebDAV 共享目录 | `%USERPROFILE%\EasyShare` |

`config.json` 包含 API Token 和 WebDAV 密码，不要提交或直接分享。排查问题时只需提供日志目录中的文件。

## 文档

- [`docs/README.md`](docs/README.md)：文档导航和资料状态
- [`docs/architecture.md`](docs/architecture.md)：当前架构、端口、API 和关键流程
- [`docs/product-vision.md`](docs/product-vision.md)：网络云盘、Windows 原生入口与内网协同的长期方向
- [docs/cloudreve-benchmark.md](docs/cloudreve-benchmark.md)：Cloudreve 能力对标、EasyShare 差距与迁移优先级
- [`docs/development.md`](docs/development.md)：开发环境、改动入口、测试方法
- [`docs/macos-port.md`](docs/macos-port.md)：macOS 平台差异、构建、Finder 集成与排障
- [`docs/version-iteration.md`](docs/version-iteration.md)：下一版本规划和交付流程
- [`docs/iterations/README.md`](docs/iterations/README.md)：逐版本目标、决策和验收记录
- [`docs/troubleshooting.md`](docs/troubleshooting.md)：日志与常见 Windows 故障
- [`docs/testing/windows-mvp-checklist.md`](docs/testing/windows-mvp-checklist.md)：Windows 手工验收清单

## 开发路线

EasyShare 采用小步迭代、逐步交付的策略。每个阶段只聚焦一个清晰主题，验收通过后再进入下一阶段。

### 阶段 0：局域网可用（已完成）

- UDP 设备发现、TCP 流式传输、WebDAV 网络驱动器
- 启动即自动映射盘符，双击进入共享空间
- 文件日志、有序退出、重复 Core 检测
- 生产构建流水线

### 阶段 1：可分发、可日常使用（已完成）

- NSIS 安装包：一键安装/卸载，注册到"应用和功能"
- 安装时自动部署 `easyshare.exe` + `easyshare-core.exe`
- 可选开机自启动
- 卸载时清理进程、网络映射和残留数据
- 统一版本号（单一版本源）

### 阶段 2：产品体验完善（当前）

- 系统托盘 + Frameless 无边框窗口
- 设置页、传输历史、另存为、多文件发送、速度高亮
- Core 异常恢复（watchdog + 配置热加载）
- 网盘功能（RustFS）：上传/列表/下载/删除/分享，实时进度
- 网盘在线预览：图片、PDF、限量 UTF-8 文本，内容访问使用短期 HMAC 票据
- 「此电脑」品牌入口（Shell NameSpace，去盘符）
- 拖拽发送（原生文件拖放 + 设备选择浮层）
- 文件夹传输：局域网自动打包/安全解压，网盘保留目录结构并支持拖拽上传
- macOS 平台抽象、Finder WebDAV 入口、原生 AppKit 菜单栏与 universal 构建脚本（GitHub Actions 自动打包已通过，待真机运行验收）
- 下一步：网盘增强（稳定文件身份与轻量目录层、可恢复分片上传、统一任务模型、回收站、文件事件流、缩略图，以及 Windows 按需文件原型）

### 阶段 3：安全加固

- 局域网设备配对与信任列表
- 文件传输认证与加密（TLS）
- Token/密码轮换，Windows 凭据保护

### 阶段 4：云端 MVP

- 账号与设备登录
- 文件元数据 API + RustFS 对象存储
- 应用内上传、下载、目录浏览
- 持久化传输任务（SQLite）

### 阶段 5：同步与原生入口

- 同步文件夹：双向同步、冲突处理、断点续传
- Windows CfAPI Sync Root：占位文件、按需下载
- macOS FileProvider 扩展：Finder 侧边栏品牌入口、占位文件、按需下载（CfAPI 的 macOS 对等）
- 品牌图标、同步状态、右键菜单

### 阶段 6：云端与内网融合

- 可信设备配对、端到端加密
- 同内容优先局域网获取
- 网络切换与离线策略

> 每个阶段的具体目标、设计决策和验收记录见 [`docs/iterations/`](docs/iterations/README.md)。
> 当前开发进度见 [`docs/progress.md`](docs/progress.md)。

## 当前限制

- Windows 为主要支持平台；首次 Mac 构建暴露的 Wails/systray `AppDelegate` 链接冲突已从架构上修复，修复后的 `.app`/DMG 与菜单栏行为仍待 Mac 真机复验。
- Windows「此电脑」入口依赖 WebClient 服务；macOS 采用 Finder 挂载 WebDAV 卷。
- 局域网发现和文件传输面向可信网络，尚无设备配对和传输加密。
- 网盘上传暂不支持断点续传；在线预览当前支持图片、PDF 和最多 1 MiB 的 UTF-8 文本，暂不支持 Office、音视频、SVG 等高级或主动内容格式。
- 暂无自动升级；macOS GitHub Actions 支持 master/PR、手动和 tag 构建，产出 DMG、`.app.zip` 与 SHA-256 校验文件；Windows 仍依赖本地 `scripts/build.ps1` 全量验证。
