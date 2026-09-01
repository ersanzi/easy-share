# EasyShare

EasyShare 是一个面向 Windows 10/11 与 macOS 的局域网文件传输与云盘桌面应用，并正在向企业知识管理平台演进。它由 Wails/Vue 桌面界面、独立 Go Core 进程和 RuoYi 账号控制面（Java）组成，可将本地共享目录映射为「此电脑」入口（macOS 上为 Finder 挂载卷），云盘文件存于 RustFS 对象存储、经控制面按用户隔离授权。macOS 移植说明见 [`docs/macos-port.md`](docs/macos-port.md)。

## 当前基线

当前代码处于 **阶段 4：云端 MVP（0.1.x 开发基线）**——阶段 2 产品体验完善已收尾（2026-07-29），账号控制面 P0–P3 已于 2026-08-31 合入，已具备的能力：

- **局域网互传**：UDP 设备发现 + TCP 流式传输；拖拽即发（文件夹自动打包），多文件发送、另存为、传输历史与实时速度
- **账号与网盘（RuoYi 控制面 + RustFS）**：桌面端登录门禁与登录态贯通；云盘上传/列表/下载/删除/分享经控制面签发短期预签名 URL 直传，对象按 `users/{userId}/` 用户命名空间隔离，客户端不持任何存储静态凭据；管理员可在客户端内建账号、开关注册、分配空间配额（含物理容量池上限）
- **系统集成**：Windows「此电脑」品牌入口（Shell NameSpace 委托 WebDAV）：局域网共享常驻 + 登录后按账号挂载「<昵称> 的网盘」与「EasyShare 共享」两个云端盘（每个文件操作经控制面，配额与授权同样生效）；macOS Finder 挂载卷 + 原生菜单栏；系统托盘（含悬停浮窗，支持拖放上传到个人/共享空间）+ Frameless 窗口
- **可靠性**：Core watchdog 自动恢复、配置热加载、WebSocket 实时事件 + 传输完成系统通知
- **知识问答（桌面「知识」页）**：连接公司私有部署的知识服务器，登录一次即可会话式提问，答案附引用来源（文件名/相似度/入库时间/片段）；登录令牌由 Go Core 网关持有，不进前端
- **知识计算面（Python 服务端）**：RustFS 文档异步解析清洗（TXT/MD/DOCX/PDF/XLSX/PPTX 与图片，含 Office 真实格式预检、可选 PaddleOCR 扫描件识别）、来源感知切块、版本化索引和检索质量评测；`/lab` 支持上传观察与检索问答（引用可溯源）；权限感知检索——文档归属落库（共享文档所有人可见、个人文档仅本人与管理员），`/query` 按登录用户裁剪检索范围；公司部署有一键向导（`knowledge/scripts/deploy.ps1`，含账号/防火墙/自启）
- **分发**：Windows NSIS 安装包 + 开机自启；macOS `.app`/DMG 构建脚本与 CI

逐项完成情况、迭代记录与待开始优先级见 [`docs/progress.md`](docs/progress.md)（唯一真相源）。开始新迭代前先读 [`docs/version-iteration.md`](docs/version-iteration.md)。

长期产品方向是演进为企业知识管理平台：在文件采集与云盘存储之上，构建解析清洗、知识库（RAG）、AI 写作辅助，并通过 WPS 插件交付。Python 文档处理闭环已经落地，运行与 API 说明见 [knowledge/README.md](knowledge/README.md)，总体架构见 [docs/knowledge-platform.md](docs/knowledge-platform.md)。原网络云盘与内网协同方向见 [docs/product-vision.md](docs/product-vision.md)。

> `/lab` 是为了测试方便提供的本地可视化实验台，不是 EasyShare 客户端功能，不代表最终产品 UI，也不具备生产认证、多租户或 RBAC。

## 仓库结构

| 目录 | 用途 |
| --- | --- |
| `frontend/` | Wails 桌面端 UI（Vue 3 + TypeScript）；`wailsjs/` 为 Wails 自动生成绑定，勿手改 |
| `internal/` | Go 内部包（Core 与桌面端共用），关键几个见下，完整清单见 [`docs/architecture.md`](docs/architecture.md) §2 |
| `internal/api` | Core HTTP API 路由 + WebSocket 事件流 |
| `internal/account` | 账号控制面客户端：登录态、用户/空间/配额管理（ADR-0007） |
| `internal/drive` | WebDAV 共享服务 + 控制面云盘客户端（预签名 URL 上传/下载/列表） |
| `internal/spacedav` | 空间 WebDAV 文件系统（P4）：建在 drive 客户端之上，把个人/共享空间暴露给资源管理器 |
| `internal/knowledge` | 知识服务网关（登录/问答代理，令牌只存 Core 侧不进前端） |
| `internal/cloud` | 网盘视图类型与预览辅助（`File`/`Preview`/预览分类）；Core 直连 S3 的旧网盘层已删（KI-5 已关闭），`objectstore/` 存储抽象仍在服役 |
| `internal/winui` | Win32 窗口几何工具（托盘悬浮窗定位/多显示器适配） |
| `cmd/core` | `easyshare-core` 后台进程入口（组装、信号、优雅退出） |
| `platform-drive/` | RuoYi 控制面内的云盘存储授权模块（Java/Maven）：预签名 URL 签发、空间/配额/池上限。父 POM 指向 gitignore 的 `platform/`（RuoYi 源码工程），编译前提见 AGENTS.md 坑表 |
| `knowledge/` | Python 知识服务（FastAPI：解析/清洗/切块/索引/RAG/评测），运行说明见 [knowledge/README.md](knowledge/README.md) |
| `deploy/` | 服务端部署编排：`rustfs/`（对象存储 + .env 凭据）、`ruoyi-db/`（控制面 PG16 + Redis + 启动脚本） |
| `scripts/` | 构建与验证脚本：`build.ps1` 全量流水线、`verify-drive-isolation.sh` 隔离验收、各类排障脚本 |
| `docs/` | 全部文档：`architecture.md`（架构事实）、`progress.md`（进度唯一真相源）、`adr/`（架构决策）、`iterations/`（迭代记录）、`known-issues.md`（已知缺陷）；导航见 [`docs/README.md`](docs/README.md) |
| `build/` | 构建产物（`bin/`）与打包资产（图标、NSIS 安装脚本） |
| `.zcode/commands/` | ZCode 原生命令（`/iterate` 开工登记、`/verify` 三层轻量回归） |

仓库根目录的 `main.go`、`app.go`、`spacemount.go`、`tray_windows.go`/`tray_hover_windows.go`/`tray_darwin.go` 是 Wails 桌面端壳代码（窗口、托盘、悬浮窗、Core 进程管理、前端桥接）。

## 环境要求

- Windows 10/11，或 macOS（构建 `.app` 需要 Xcode Command Line Tools）
- Go 1.25（以 `go.mod` 为准）
- Node.js 与 npm
- Wails CLI 2.13.0
- NSIS 3.x（仅 Windows 安装包需要，`winget install NSIS.NSIS`）
- Microsoft WebView2 Runtime（仅 Windows）
- Windows `WebClient` 服务（仅 Windows「此电脑」入口需要）
- Docker（运行 RustFS / 控制面的 PostgreSQL 与 Redis）

仅开发**账号控制面**（`platform-drive/`）时额外需要：JDK 21、Maven、RuoYi-Vue-Plus 6.0 源码工程（放在 `platform/`，已 gitignore，准备步骤见 [`deploy/ruoyi-db/README.md`](deploy/ruoyi-db/README.md)）。只做桌面端/知识服务开发不需要 Java。

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

要联调登录与云盘（账号控制面），还需先起服务端栈：`docker compose -f deploy/ruoyi-db/compose.yaml up -d`（PostgreSQL + Redis），再按 [`deploy/ruoyi-db/README.md`](deploy/ruoyi-db/README.md) 启动 RuoYi admin 与 RustFS；不启动控制面时桌面端可正常使用局域网传输，「知识」页也可连独立部署的知识服务器。

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

1. PR 到 `master` 或推送 `master`：自动构建，完成后在该次 Actions 运行的 **Artifacts** 下载 `EasyShare.dmg`、`EasyShare.app.zip` 和 `SHA256SUMS.txt`；
2. `workflow_dispatch`：在 Actions 页面手动运行，并选择 `darwin/universal`、`darwin/arm64` 或 `darwin/amd64`；
3. 推送 `v*` tag：自动构建并创建 GitHub Release，发布同一组安装包与校验文件。

日常开发流程是先同时推送 Gitee 与 GitHub：

```bash
git push origin dev
git push github dev
```

推送 `dev` 会自动运行「Knowledge Tests」（Python 知识面测试，按 `knowledge/**` 路径过滤）；桌面端 DMG/安装包构建在 PR、`master`、tag 或手动触发时进行。发布版本时把 tag 同时推送到两个仓库；GitHub 上的 tag 构建成功后会自动创建 Release。macOS 详细构建与排障见 [`docs/macos-port.md`](docs/macos-port.md)。

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

完整导航（按读者路径分组）见 [`docs/README.md`](docs/README.md)。最常用的三份：

- [`docs/architecture.md`](docs/architecture.md)：当前架构、端口、API 和关键流程（当前事实以此为准）
- [`docs/progress.md`](docs/progress.md)：两条主线的路线总览、进度与迭代记录（唯一真相源）
- [`docs/development.md`](docs/development.md)：开发环境、改动入口、测试方法

## 开发路线

EasyShare 采用小步迭代策略：两条产品主线（桌面文件产品 阶段 0-6、企业知识平台 里程碑 0-3）的阶段划分、当前状态与待开始优先级，统一维护在 [`docs/progress.md`](docs/progress.md) 的「路线总览」。方向依据：

- 桌面文件产品 → 网络云盘：[`docs/product-vision.md`](docs/product-vision.md)，能力对标 [`docs/cloudreve-benchmark.md`](docs/cloudreve-benchmark.md)
- 企业知识平台（解析→RAG→WPS 插件）：[`docs/knowledge-platform.md`](docs/knowledge-platform.md)

> 每个阶段的具体目标、设计决策和验收记录见 [`docs/iterations/`](docs/iterations/README.md)。

## 当前限制

- Windows 为主要支持平台；首次 Mac 构建暴露的 Wails/systray `AppDelegate` 链接冲突已从架构上修复，修复后的 `.app`/DMG 与菜单栏行为仍待 Mac 真机复验。
- Windows「此电脑」入口依赖 WebClient 服务；macOS 采用 Finder 挂载 WebDAV 卷。按账号命名空间挂载的云端盘（个人/共享，19082/19083）已随控制面批次落地，配额收回/授权撤销时条目自动卸载；**真实鼠标交互验收仍欠**（登录 → 上传 → 换账号看列表、悬浮窗悬停/固定/拖放）。
- 账号控制面的隔离结论来自控制面活栈验收（9/9）与 Go 客户端集成测试；桌面端「登录 → 上传 → 换账号看列表」真机链路待真实操作补验。
- 局域网发现和文件传输面向可信网络，尚无设备配对和传输加密。
- 网盘上传暂不支持断点续传；在线预览当前支持图片、PDF 和最多 1 MiB 的 UTF-8 文本，暂不支持 Office、音视频、SVG 等高级或主动内容格式。
- 在线升级已上线（0.1.1，2026-08-31）：升级源为 RuoYi 控制面，Windows 支持「检查 → 下载（SHA256 校验）→ 重启并更新 → 静默安装 → 自动重启」全自动；macOS 仅检测并引导下载（产物未签名，自动替换留后续）；GitHub Releases 仍是开发侧分发渠道，未接入客户端升级检查。发布命令见 `scripts/publish-release.ps1`，排障见 [`docs/troubleshooting.md`](docs/troubleshooting.md) 第 13 节。macOS 与 Windows GitHub Actions 均支持 master/PR、手动和 tag 构建并产出安装包与 SHA-256 校验文件；`dev` 推送仅触发 Python 知识面测试（Knowledge Tests），Windows 日常验证仍以本地 `scripts/build.ps1` 为准。
