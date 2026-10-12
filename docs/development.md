# EasyShare 开发手册

## 1. 首次准备

```powershell
go version
node --version
npm --version

# Wails 版本与项目保持一致
go install github.com/wailsapp/wails/v2/cmd/wails@v2.14.0

# NSIS 3.x（构建安装包需要）
winget install NSIS.NSIS

go mod download
npm ci --prefix frontend
```

项目 `go.mod` 当前声明 Go 1.25。升级 Go、Wails、Vue 或 Vite 时，应单独作为一次依赖迭代并执行完整验收。

## 2. 日常开发

### 启动桌面开发模式

```powershell
# Wails 桌面端需要能在 build/bin 找到 Core
go build -o build/bin/easyshare-core.exe ./cmd/core

$wails = Join-Path (go env GOPATH) 'bin\wails.exe'
& $wails dev
```

### 只调试 Core

```powershell
go run ./cmd/core
```

如果已有 Core 在运行，新 Core 会识别兼容实例并正常退出。需要替换 Core 二进制时，先在 UI 中点击“退出全部服务”。

### 只调试前端

```powershell
npm --prefix frontend run dev
```

纯 Vite 页面没有完整 Wails Go Bridge，涉及 `core.ts` 的功能应在 `wails dev` 中验证。

## 3. 常用测试

### 全部 Go 测试

```powershell
go test ./...
```

### 前端测试与类型构建

```powershell
npm --prefix frontend test
npm --prefix frontend run build
```

### Windows WebClient 真实映射测试

该测试会临时创建一个真实网络驱动器映射，只能使用确认空闲的盘符：

```powershell
Get-PSDrive -Name Y -ErrorAction SilentlyContinue
net use Y:

$env:EASYSHARE_TEST_DRIVE = 'Y:'
go test ./internal/drive -run '^TestWindowsWebClientDigestIntegration$' -count=1 -v
Remove-Item Env:EASYSHARE_TEST_DRIVE

net use Y:
```

测试应自动取消映射；最后一条命令应报告找不到连接。若盘符已有用途，绝对不要运行该测试。

### RustFS 对象存储一致性测试

普通测试不会依赖 Docker。只有本机 RustFS 已启动且明确设置环境变量时，才运行真实 Multipart、预签名下载和 SHA-256 闭环：

```powershell
Set-Location deploy/rustfs
Copy-Item .env.example .env
# 替换 .env 凭证后启动服务，并在 Console 创建测试 bucket
docker compose up -d
```

完整环境变量和测试命令见 [`../deploy/rustfs/README.md`](../deploy/rustfs/README.md)。不要把开发 HTTP 配置用于公网或生产环境。

### 完整生产流水线

```powershell
powershell -ExecutionPolicy Bypass -File scripts/build.ps1
```

该脚本依次执行 Go 测试、前端测试、前端构建、Core 编译和 Wails production 构建（含 NSIS 安装包）。脚本会自动检测 NSIS 安装路径。产物为 `build/bin/easyshare.exe`、`build/bin/easyshare-core.exe` 和 `build/bin/EasyShare-amd64-installer.exe`。

Windows 正在运行的 EXE 可能被锁定，因此构建前先退出桌面和 Core。

## 4. 按功能定位代码

| 要修改的内容 | 首选入口 | 同步检查 |
| --- | --- | --- |
| Core API | `internal/api/server.go` | `internal/desktop/client.go`、API 测试、架构文档 |
| 启动/退出顺序 | `app.go`、`cmd/core/main.go`、`internal/api/server.go` | `drive_lifecycle_test.go`、前端退出测试 |
| 网络驱动器 | `internal/drive` | Windows 集成测试、故障排查文档 |
| 配置字段 | `internal/config/config.go` | 验证、默认值、兼容迁移、README |
| 设备发现 | `internal/discovery` | UDP 端口、防火墙、双机验收 |
| 文件传输 | `internal/transfer`、`internal/task` | 路径安全、中断、重名和大文件测试 |
| 对象存储数据面 | internal/cloud/objectstore | RustFS adapter、错误分类、opt-in 一致性测试、ADR-0006 |
| Wails Go 方法 | `app.go` | `frontend/src/services/core.ts`、重新生成 bindings |
| 前端状态流 | `frontend/src/composables/useEasyShare.ts` | composable 测试、Core 退出后禁止轮询 |
| UI/样式 | `frontend/src/components`、`style.css` | 组件测试、窗口尺寸、中文显示 |
| 日志 | `internal/logging`、`app.go`、`cmd/core/main.go` | 敏感信息、轮转、troubleshooting 文档 |
| 安装包 | `build/windows/installer/project.nsi`、`wails.json` info 字段 | 双进程部署、卸载清理、版本号、NSIS PATH |

## 5. 生成文件与禁止事项

- `frontend/wailsjs` 由 Wails 生成；增加或删除 `App` 导出方法后运行 `wails dev` 或 `wails build`，不要手改绑定。
- `frontend/dist`、`build/bin` 是构建产物，不作为源码维护。
- 不要提交 `%LOCALAPPDATA%\EasyShare\config.json`，其中包含 Token 和密码。
- 不要通过修改 `BasicAuthLevel=2` 来“修复”映射；当前 Digest 认证就是为了兼容默认策略。
- 不要把 HTTP URL 直接传给 `net use`；必须使用 `\\host@port\DavWWWRoot`。
- 不要在 `ShutdownAll` 之后调用 refresh 或其他 Core API。
- 不要让取消映射逻辑删除无法确认归 EasyShare 所有的盘符。

## 6. 提交前检查

1. 更新或新增对应测试。
2. `gofmt` 所有 Go 文件。
3. 运行 `go test ./...`。
4. 运行前端测试和构建。
5. 运行 `scripts/build.ps1`。
6. 涉及 Windows 集成时执行手工验收清单。
7. 更新架构、排障或迭代文档。
8. 检查日志和 diff 中没有 Token、密码、个人文件路径或测试残留映射。

## 7. 发布、CI 与运行数据

### macOS 构建

在 Mac 或 GitHub Actions macOS runner 上：

```bash
bash scripts/build-mac.sh
```

默认产出 universal `.app`/DMG，并用 `lipo` 合成同时包含 arm64 与 x86_64 的 Core：

```text
build/bin/easyshare-core
build/bin/easyshare.app
build/bin/EasyShare.dmg
```

`.github/workflows/build-mac.yml` 两种使用方式：

1. 推送 `v*` tag：自动构建并创建 GitHub Release，发布 `EasyShare.dmg`、`EasyShare.app.zip` 和 `SHA256SUMS.txt`；推送 `master`/PR 不再触发构建；
2. `workflow_dispatch`：Actions 页面手动运行，可选 `darwin/universal`、`darwin/arm64` 或 `darwin/amd64`。

macOS 详细构建与排障见 [`macos-port.md`](macos-port.md)。

### 双仓库推送与版本约定

提交代码后两个远程都要推（GitHub 承载 macOS CI，Gitee 为国内主仓库）：

```bash
git push origin dev
git push github dev
```

版本阶段约定：`test` 仅用于流水线冒烟，`preview` 用于功能仍在完善且尚未完成全部真机验收的公开预览，`beta` 用于主要功能稳定后的扩大测试，`rc` 用于正式版候选。所有带 `-` 的 SemVer 预发布 tag（如 `v0.1.0-preview.1`）自动标记为 GitHub Prerelease。发布正式版：改三处版本号（`internal/version/version.go`、`wails.json`、`frontend/package.json`）→ `scripts/build.ps1` → `scripts/publish-release.ps1` 上传控制面。

### CI 触发规则

- `dev` 推送：仅触发 Knowledge Tests（Python 知识面测试，按 `knowledge/**` 路径过滤）；桌面构建只在打 `v*` tag 时跑，日常验证靠本地 `scripts/build.ps1`。
- Windows / macOS CI（`build-windows.yml` / `build-mac.yml`）：仅 `v*` tag 推送自动触发，另有手动触发逃生通道；`master` 推送与 PR 不再触发。
- Rolling Release（`rolling-release.yml`）：跟随上述 tag 构建跑，双平台对同一 tag 成功后删除重建 `latest` Release；手动触发的构建不重建 latest。注意 tag 触发的 run `head_branch` 是 tag 名，本工作流不能再用 `branches` 过滤，改用 job 级 `if` 判定 `v*` 前缀。

### 服务端联调（控制面栈）

要联调登录与云盘，需先起服务端栈：`docker compose -f deploy/ruoyi-db/compose.yaml up -d`（PostgreSQL + Redis），再按 [`../deploy/ruoyi-db/README.md`](../deploy/ruoyi-db/README.md) 启动 RuoYi admin 与 RustFS。不启动控制面时桌面端可正常使用局域网传输，「知识」页也可连独立部署的知识服务器。只做桌面端/知识服务开发不需要 Java；开发控制面（`platform-drive/`）额外需要 JDK 21、Maven 与 RuoYi 源码工程（`platform/`，已 gitignore，准备步骤见 deploy/ruoyi-db README）。

### 运行数据位置

| 内容 | 默认位置 |
| --- | --- |
| 配置 | `%LOCALAPPDATA%\EasyShare\config.json` |
| 日志 | `%LOCALAPPDATA%\EasyShare\logs` |
| 接收文件 | `%USERPROFILE%\Downloads\EasyShare` |
| WebDAV 共享目录 | `%USERPROFILE%\EasyShare` |

`config.json` 包含 API Token 和 WebDAV 密码，不要提交或直接分享；排查问题时只需提供日志目录中的文件。

