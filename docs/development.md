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

### 活栈测试（真实控制面 + PostgreSQL + RustFS）★ **发现契约类缺陷的唯一手段**

`go test ./...` 默认**跳过**活栈用例（未设环境变量即 `Skip`）。它们覆盖「单测永远看不见」的那一层：
真实雪花 ID、真实 JSON 契约、真实对象存储。**本仓 KI-18 ~ KI-26 全部只能在这里暴露**。

覆盖面：`internal/drive`（CAS/分片/回收站/move/单请求重试/**跨用户权限边界**/**配额闸门**）、`internal/account`（空间与配额接口）、
`internal/spacedav`（「此电脑」挂载盘的读写/改名/Range——该包历史上出过 KI-9/12/13 三个高危缺陷，
此前零活栈覆盖）。**新写活栈用例时**：清理一律用 `t.Cleanup` 且要一路走到 `RecyclePurge`
（只 `RemoveAll` 是软删，行会留在回收站——见 KI-25）。

```powershell
# 前置：PG + Redis + RustFS 起好（deploy/ruoyi-db、deploy/rustfs），控制面在 8090
docker ps --format "{{.Names}}	{{.Status}}" | Select-String "ruoyi|rustfs"

$env:EASYSHARE_LIVE_DRIVE='1'
$env:EASYSHARE_LIVE_BASE='http://127.0.0.1:8090'
$env:EASYSHARE_LIVE_USER_A='admin'; $env:EASYSHARE_LIVE_PASS_A='admin123'
# 权限用例另外用到两个真实账号（本机 dev 库自带）：
#   test / test1，密码均为 666666
# 需要的环境装配（只在首次做一次）：
#   admin 对共享空间 write、test 对共享空间 read、test1 **不授权**
#   test / test1 各开 1 GiB 个人空间、共享空间配容量
# 装配走生产同链路接口（App/account 的 SetPersonalQuota / SetSharedQuota / GrantShared）
go test ./internal/drive/ ./internal/account/ ./internal/spacedav/ -run "TestLive" -count=1 -v
```

> **为什么必须有这一层**：单测里的 `fileId`、`sessionId` 是**自己编的小数字**，
> 而真实控制面签发的是 19 位雪花 ID（超 JS 安全整数、被序列化成字符串）。
> 于是「单测全绿」与「真实可用」之间曾经隔着**六个**缺陷（KI-18…KI-23），
> 其中三个能静默丢数据。判据同样是**看输出里有没有 `--- PASS: TestLive...`**。

### 前端测试与类型构建

```powershell
npm --prefix frontend test
npm --prefix frontend run build
```

### Java 控制面测试（platform-drive）

```powershell
cd platform-drive
mvn -o clean test        # 84 个用例
```

> ⚠️ **必须带 `clean`，且必须确认输出里有 `Tests run: N`**。两个坑都踩过：
>
> 1. 上游 RuoYi 父 POM（`platform/pom.xml:78`）设了 `<maven.test.skip>true</maven.test.skip>`，
>    使得 `mvn test` 打印 `Tests are skipped.` 却仍返回 **`BUILD SUCCESS`**——
>    看起来通过，实际一个测试都没编译、没执行。模块 POM 已覆盖为 `false`（KI-8）。
> 2. 不带 `clean` 时**增量构建会给出陈旧结果**：改了源码但结论对不上，
>    对着旧 class 下判断。本轮因此误判过两次。
>
> **判据不是退出码，是 `Tests run: N`。** 任何层看不到具体用例数，都要怀疑它没真跑。

### Python 知识服务测试

```powershell
cd knowledge
.venv/Scripts/python.exe -m pytest -m "not integration"    # 180 passed, 1 skipped
```

> 集成测试依赖 RustFS（`-m "not integration"` 排除），需要真实对象存储时单独跑。

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

该脚本依次执行文档证据引用检查（含检查器自检）、Go 测试、前端测试、前端构建、Core 编译和 Wails production 构建（含 NSIS 安装包）。脚本会自动检测 NSIS 安装路径。产物为 `build/bin/easyshare.exe`、`build/bin/easyshare-core.exe` 和 `build/bin/EasyShare-amd64-installer.exe`。

前端两道闸门藏在本机命令里，不单独列出：`npm test`（vitest）内含设计 token 守卫 `frontend/src/__tests__/designTokens.test.ts`，裸色值、纯灰中性色、文字档掉到 WCAG AA 以下都会在这里变红；`npm run build` 先跑 `vue-tsc --noEmit`，Wails 绑定没同步（TS2305/TS2339）也在这里变红。

文档证据引用的口径是：`docs/` 下凡写成 `文件路径` · `符号` 的证据，文件必须存在、符号必须能在该文件里命中；顺带查链接方括号里是不是空文本（空文字链接渲染出来是看不见的空串，只有图片允许空 alt）。检查器自身的正确性由 `node scripts/diag/check-doc-refs.mjs --self-test` 兜底（注入坏引用与空链接必须被抓到，六类该跳过的必须跳过）。

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
5. 运行 `node scripts/diag/check-doc-refs.mjs --self-test` 与 `node scripts/diag/check-doc-refs.mjs docs`。
6. **改过 `engine/` 就跑 `python scripts/check_skill_sync.py`**（不一致时加 `--apply` 重新生成副本）。
   `skills/minutes-from-transcript/scripts` 是 `engine/minutecraft` 的复制版（给别人整目录复制走），
   两处并存必然漂移。**这一步按用户要求只在本地做，不进 CI**（2026-10-20）：
   它拦的是「忘了同步副本」，与当次提交的改动质量无关，放进 CI 会在前端/文档都没问题时
   单独变红、把人引向错误方向——实际已经发生过两次。忘记跑不会红，所以改引擎时先跑它。
7. 运行 `scripts/build.ps1`。
8. 涉及 Windows 集成时执行手工验收清单。
9. 更新架构、排障或迭代文档。
10. 检查日志和 diff 中没有 Token、密码、个人文件路径或测试残留映射。

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

- `dev` 推送：触发 Knowledge Tests（Python 知识面测试，按 `knowledge/**` 路径过滤）与 Quality Gates（`quality-gates.yml`：文档证据引用 + 设计 token 守卫 + `vue-tsc` 类型检查，不设路径过滤）；桌面构建只在打 `v*` tag 时跑，日常验证靠本地 `scripts/build.ps1`。
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

