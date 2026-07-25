# EasyShare 故障排查

## 1. 先收集日志

日志目录：

```text
Windows: %LOCALAPPDATA%\EasyShare\logs
macOS:   ~/Library/Caches/EasyShare/logs
```

Windows 可按 `Win + R` 粘贴对应路径；macOS 可在 Finder 使用“前往文件夹”。复现一次后按时间查看：

| 文件 | 主要内容 |
| --- | --- |
| `desktop.log` | Wails 启动、Core 连接、API 操作、Vue/浏览器异常 |
| `core.log` | Core 生命周期、发现、接收、WebDAV、命名空间注册 |
| `core-process.log` | 子进程 stdout/stderr、正常日志初始化前的 panic |
| `desktop.log.1` / `core.log.1` | 5 MiB 轮转备份 |

日志可用于排障，但不要发送 `config.json`，因为它包含 API Token。

## 2. 「此电脑」入口打不开或内容为空

EasyShare 不再映射盘符。「此电脑」入口直接委托到 WebDAV UNC（共享 `\\127.0.0.1@19080\DavWWWRoot`、网盘 `\\127.0.0.1@19081\DavWWWRoot`）。双击打不开或显示为空时，按顺序检查：

1. 确认正在运行的是最新的 `easyshare.exe` 和配套的 `easyshare-core.exe`，且 `core.log` 出现 WebDAV ready。
2. 确认端口在监听：

   ```powershell
   Get-NetTCPConnection -LocalPort 19080,19081 -State Listen
   ```

3. 确认 Windows WebClient 服务正在运行（解析 WebDAV UNC 必需）：

   ```powershell
   Get-Service WebClient | Format-List Name,Status,StartType
   ```

4. 确认 UNC 可直接访问（无需输入凭据即应列出目录）：

   ```powershell
   Get-ChildItem '\\127.0.0.1@19080\DavWWWRoot'
   ```

5. 仍不行时，用 `SHParseDisplayName` 确认 Shell 能解析该 UNC，详见迭代记录 [`iterations/2026-07-20-thispc-namespace-no-drive-letter.md`](iterations/2026-07-20-thispc-namespace-no-drive-letter.md)。

当前实现仅监听 127.0.0.1 且无认证，不会弹出凭据框。Windows WebClient 会自动剥离 `DavWWWRoot` 前缀（访问 `...\DavWWWRoot\test.txt` 实际请求 `/test.txt`），因此 WebDAV 服务用 `Prefix: "/"` 即可；手工测试时不要自己再加 `/DavWWWRoot`，否则会因路径多套一层返回 409。

## 3. `19079 bind` / 端口被占用

旧日志可能出现：

```text
listen tcp 127.0.0.1:19079: bind: Only one usage of each socket address...
```

新版 Core 会识别同一配置的现有实例并退出重复进程。仍出现时：

```powershell
Get-NetTCPConnection -LocalPort 19079 -ErrorAction SilentlyContinue |
  Select-Object LocalAddress,LocalPort,State,OwningProcess

Get-Process -Id <OwningProcess> |
  Select-Object Id,ProcessName,Path,StartTime
```

- 如果是 EasyShare Core：只启动 `easyshare.exe`，不要手动再运行 Core。
- 如果是其他程序：修改配置中的 API Port，或结束冲突程序后重试。
- 不要连接身份校验失败的未知本地服务；健康检查必须同时匹配 Device ID 和 HMAC proof。

## 4. UI 显示 `core unavailable` 或连接被拒绝

1. 查看 `core.log` 是否有配置错误、监听错误或 panic。
2. 查看 `core-process.log` 是否有启动阶段错误。
3. 确认两个 EXE 位于同一目录：

   ```text
   build/bin/easyshare.exe
   build/bin/easyshare-core.exe
   ```

4. 如果刚点击“退出全部服务”，这是预期的 Core 终止；前端应停在安全退出页且不继续轮询。如果持续出现新错误，检查 `useEasyShare.ts` 的退出状态机。

## 5. 「此电脑」入口显示描述句或错误名称

症状：入口磁贴显示「双击进入 EasyShare 网盘」「双击进入局域网共享」这类描述句，而不是干净的名称「EasyShare 网盘」「EasyShare 共享」。

根因：CLSID 下残留的 `LocalizedString`（或 `System.Category`）会覆盖默认值成为资源管理器显示名。这些是早期实验留下的值，当前代码并不设置，但旧机器上可能残留。

检查：

```powershell
$k = Get-Item 'HKCU:\Software\Classes\CLSID\{E5A1F2B3-C4D5-6E7F-8A9B-0C1D2E3F4A5B}'
$k.GetValue('LocalizedString')   # 非空即为元凶
$k.GetValue('System.Category')
$k.GetValue('')                  # 期望显示的干净名称
```

修复：重启 EasyShare 即可——注册命名空间时 `clearStaleDisplayOverrides` 会删除 `LocalizedString`、`System.Category`、`TileInfo`、`System.ItemTypeText` 这些会劫持显示名的旧值。也可手工删除后重启 Explorer：

```powershell
$base = 'HKCU:\Software\Classes\CLSID\{E5A1F2B3-C4D5-6E7F-8A9B-0C1D2E3F4A5B}'
Remove-ItemProperty -Path $base -Name LocalizedString,System.Category -ErrorAction SilentlyContinue
Stop-Process -Name explorer -Force; Start-Sleep 2; Start-Process explorer
```

注意：Explorer 对命名空间元数据缓存很顽固，改注册表后必须重启 `explorer` 才刷新，仅 `SHChangeNotify` 往往不够。`InfoTip` 只作鼠标悬停提示，不影响显示名，可保留描述句。

## 6. 设备无法互相发现

- 两台设备应位于同一可信局域网。
- Windows 防火墙专用网络需要允许 UDP `9527` 和 TCP `9528`。
- 检查企业网络是否禁用广播或客户端互访。
- `core.log` 中确认 discovery 和 receiver 没有立即退出。
- 默认发现窗口按七秒内出现对端验收。

## 7. 文件传输失败

排查顺序：

1. 接收目录是否存在并可写。
2. TCP `9528` 是否被防火墙拦截。
3. 发送文件是否在发起后被移动、删除或锁定。
4. 磁盘空间是否足够。
5. `core.log`、任务状态和对端日志是否记录同一时间的错误。

当前传输面向可信 LAN，不要在不可信网络中发送敏感文件。

## 8. 构建失败或 EXE 无法覆盖

Windows 会锁定正在运行的可执行文件。先在 UI 点击“退出全部服务”，再关闭窗口，然后检查：

```powershell
Get-Process | Where-Object ProcessName -Like 'easyshare*'
```

确认无进程后重新运行：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/build.ps1
```

`go test -race` 在部分 Windows/MinGW 工具链中可能因运行时 DLL 报 `0xc0000139`；这属于环境限制，不能替代普通测试和真实 Windows 集成验收。

## 9. macOS 构建报重复 `AppDelegate`

典型错误：

```text
duplicate symbol '_OBJC_METACLASS_$_AppDelegate'
duplicate symbol '_OBJC_CLASS_$_AppDelegate'
ld: 2 duplicate symbols
clang: error: linker command failed with exit code 1
```

根因不是 Xcode Command Line Tools 未安装，也不是 Go 1.26 本身。Wails v2.13.0 的 macOS 后端和 `getlantern/systray` v1.2.2 的 Darwin 实现都定义全局 Objective-C 类 `AppDelegate`；后者还会设置 `NSApp.delegate` 并启动自己的事件循环。二者进入同一桌面二进制后必然冲突。

当前实现中 systray 只允许进入 Windows 构建，macOS 使用 `tray_native_darwin.m` 创建 `NSStatusItem`。在 Mac 仓库根目录检查：

```bash
go list -deps . | grep getlantern/systray
# 预期无输出

bash scripts/build-mac.sh
lipo -info build/bin/easyshare-core
# universal 构建应同时包含 x86_64 与 arm64
```

若第一条命令仍输出 systray，先确认已拉取 2026-07-23 的平台托盘拆分，并检查是否有新的无构建标签 Go 文件再次导入该库。不要使用 `-Wl,-multiply_defined,suppress`、覆盖 `NSApp.delegate` 或让托盘 bridge 调用 `[NSApp run]`：这些绕过方式即使链接成功，也会破坏 Wails 窗口与退出生命周期。

Wails 生成绑定时出现以下消息通常不是失败：

```text
Not found: time.Time
```

它在成功的 Windows 构建中也会出现。应继续向下查看最终的 `Done` 或真正的 Go/Vite/clang `ERROR`；本次构建中止点是后面的 `duplicate symbol`。

完整 macOS 构建、Finder 挂载与菜单栏排障见 [`macos-port.md`](macos-port.md)。

## 10. 网盘在线预览失败

先区分“列表能力提示”和“实际预览元数据”：S3 `ListObjects` 不一定返回可靠的 `Content-Type`，列表只用于低成本显示入口；打开预览时 Core 会通过 `HeadObject` 校正。如果列表显示“预览”但打开失败，检查 `core.log` 中的 `cloud preview`/`preview head` 错误，并核对对象实际 `Content-Type` 与大小。

图片/PDF 内容 URL 返回 `401 preview_ticket_invalid` 时，表示五分钟票据已过期、参数被篡改或 Core API Token 已轮换。关闭预览后重新打开以获取新票据，不要缓存旧 URL，也**不能把长期 API Token 拼入 URL**。WebView 的 `<img>`/`<iframe>` 请求无法像 Wails API 调用一样附加 Bearer Header，这正是使用短期 HMAC 票据的原因。

常见格式行为：

- SVG 被拒绝是安全设计。SVG 可包含脚本、外链和其他主动内容，应下载后由系统应用打开。
- 文本只预览前 1 MiB 且必须为 UTF-8；其他编码降级为“不支持”，不会猜测编码或通过 HTML 渲染。
- Windows 的 `mime.TypeByExtension` 可能受注册表和第三方软件影响，代码必须保留内置扩展名回退，并以 `HeadObject` 结果作为实际预览校正。

PDF 空白时按以下顺序排查：

1. 从预览描述中复制临时内容 URL，立即请求并确认不是 401/415。
2. 检查响应是否为 `Content-Type: application/pdf`、`Content-Disposition: inline`，且 `Content-Length` 与对象大小一致。
3. 确认 URL 指向 `127.0.0.1` 的 Core 端口，而不是 Wails 页面相对地址或 RustFS 管理地址。
4. 检查当前 WebView2/PDF 查看器是否可用；先用小型标准 PDF 排除文件损坏。
5. 不要通过关闭认证、延长长期 Token 或把对象存储凭据暴露给前端来绕过问题。

Go 导出方法或结构体字段变化后若前端报 `TS2305`/`TS2339`，执行：

```powershell
wails generate module
npm --prefix frontend run build
```

并确认 `frontend/src/types/core.ts`、`frontend/src/services/core.ts` 与 `frontend/wailsjs/go/` 同步更新。

## 11. Cloudreve 对标研究与上游源码核验

研究上游项目时，`git clone --depth 1` 可能因网络代理、TLS 连接或 GitHub 链路重置而反复失败。不要因此改用来源不明的转载文章，也不要只记录会变化的 `master`。

推荐流程：

1. 通过 GitHub API 确认默认分支和当前 commit SHA。
2. 使用 `/git/trees/{sha}?recursive=1` 获取该固定提交的源码树。
3. 通过 raw 内容地址按固定 SHA 下载关键源码文件。
4. 在迭代文档中记录 commit SHA、提交标题和研究日期。
5. 用官方产品文档确认用户行为，再用源码确认内部边界。

对标记录必须区分四种状态：

- 上游已经实现；
- EasyShare 已经实现；
- EasyShare 建议做独立原型；
- EasyShare 后续路线。

例如 Cloudreve 桌面客户端支持 Windows Cloud Files API，并不代表 EasyShare 当前已具备占位文件和按需下载。EasyShare 当前入口仍是 Shell NameSpace + WebDAV；CfAPI 只作为经过验收后才可能迁移的独立原型。

若官方概念文档与源码命名不同，以固定 commit 源码确认数据关系，以文档解释用户语义。涉及 File/Entity、Upload Session、事件续传和任务状态时，不能只凭 README 功能列表推断架构。

## 12. Python 文档处理任务异常

### 12.1 任务一直停在 `queued`

1. 必须在 `knowledge/` 目录以单个 Uvicorn worker 启动：`uvicorn app.main:app --workers 1`。
2. 检查 FastAPI lifespan 是否执行、`JOB_WORKERS` 是否大于 0。
3. 查看 `GET /health` 的 `jobs` 计数和 `GET /jobs/{jobId}` 的 `stage/error_message`。
4. 当前 SQLite + 线程池只支持单进程；不要启动多个 Uvicorn worker 共享 `jobs.db` 和 JSON 向量库。

### 12.2 Office 文件失败或 PDF 提示 OCR

- 确认文件扩展名与真实格式一致，并安装 `python-docx`、`openpyxl`、`python-pptx`、`pypdf`。
- 损坏 Office 文件会显式失败，不会把 ZIP/XML 二进制内容当文本。
- 当前 PDF 只支持文本层；扫描 PDF/图片需等待 PaddleOCR 阶段，出现 OCR 提示属于预期保护。

### 12.3 任务失败但查询命中了新版本

`manifest.json` 是完成标志。当前实现会在 manifest 写入失败时恢复该 `file_id` 的旧索引，并对同一文件加进程内锁，避免并发版本互相覆盖。若仍出现不一致：

1. 查询任务是否确实为 `completed`；
2. 检查 `derived/{fileId}/{versionId}/manifest.json` 是否存在；
3. 检查 `VECTOR_STORE_PATH` 是否被多个进程共享；
4. 对失败任务使用 `/jobs/{jobId}/retry`，不要直接修改 SQLite。

### 12.4 测试时出现 `__pycache__` 或 SQLite 文件冲突

并发测试时设置 `PYTHONDONTWRITEBYTECODE=1`，并为每个进程使用独立的 `JOB_STORE_PATH`、`VECTOR_STORE_PATH`。语法检查使用 `compile(source, filename, "exec")`，不要并发运行 `compileall` 写同一个 `__pycache__`。