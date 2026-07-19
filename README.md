# EasyShare

EasyShare 是一个面向 Windows 10/11 的局域网文件传输桌面应用。它由 Wails/Vue 桌面界面和独立 Go Core 进程组成，并可将本地共享目录映射为 Windows 网络驱动器。

## 当前基线

当前代码处于 **Windows MVP（0.1.x 开发基线）**：

- UDP 局域网设备发现
- TCP 流式文件发送、接收、接受和拒绝
- 本地 WebDAV 服务
- 启动桌面端后自动连接 `Z:` 网络驱动器，并支持安全复用和取消映射
- Apple/macOS 风格的 Vue 3 单页界面
- 桌面端、Core、前端异常的文件日志
- 重复 Core 检测和有序退出流程

后续版本开始前请先阅读 [`docs/version-iteration.md`](docs/version-iteration.md)。

长期产品方向是演进为带账号、容量、上传下载、同步文件夹和 Windows 原生入口的网络云盘，并保留局域网发现与直传；详见 [`docs/product-vision.md`](docs/product-vision.md)。

## 环境要求

- Windows 10/11
- Go 1.25（以 `go.mod` 为准）
- Node.js 与 npm
- Wails CLI 2.13.0
- Microsoft WebView2 Runtime
- Windows `WebClient` 服务（网络驱动器功能需要）

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

完整流水线：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/build.ps1
```

流水线依次执行 Go 测试、前端测试、TypeScript/Vite 构建、Core 编译和 Wails production 构建。产物为：

```text
build/bin/easyshare.exe
build/bin/easyshare-core.exe
```

构建前应先退出正在运行的 EasyShare，否则 Windows 可能锁定可执行文件。

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
- [`docs/development.md`](docs/development.md)：开发环境、改动入口、测试方法
- [`docs/version-iteration.md`](docs/version-iteration.md)：下一版本规划和交付流程
- [`docs/iterations/README.md`](docs/iterations/README.md)：逐版本目标、决策和验收记录
- [`docs/troubleshooting.md`](docs/troubleshooting.md)：日志与常见 Windows 故障
- [`docs/testing/windows-mvp-checklist.md`](docs/testing/windows-mvp-checklist.md)：Windows 手工验收清单

## 当前限制

- 仅正式支持 Windows；网络驱动器映射依赖 Windows WebClient。
- 局域网发现和文件传输面向可信网络，尚无设备配对和传输加密。
- peers、tasks 等运行状态主要保存在内存中，Core 重启后不会恢复。
- 暂无安装器发布、自动升级、开机启动和完整设置页面。


