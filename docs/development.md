# EasyShare 开发手册

## 1. 首次准备

```powershell
go version
node --version
npm --version

# Wails 版本与项目保持一致
go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0

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

该脚本应是提交或交付前的最终检查。Windows 正在运行的 EXE 可能被锁定，因此构建前先退出桌面和 Core。

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

