# 此电脑品牌入口：去盘符 + 命名空间显示名修复

> 日期：2026-07-20
> 状态：已完成并通过真实 Windows 验收
> 主题：让「此电脑」里的 EasyShare 入口像 WPS 云盘一样无盘符、品牌化，并修复入口显示成描述句的问题

## 用户问题

- 「此电脑」里的 EasyShare 映射和 WPS 云盘出入很大：既有品牌命名空间入口，又额外暴露了 `Y:`/`Z:` 两个裸网络驱动器盘符，不像消费级产品。
- WPS 云盘在「此电脑」里只有一个品牌入口，没有盘符概念。
- 后续验收又发现：入口磁贴显示的是「双击进入 EasyShare 网盘」「双击进入局域网共享」这种描述句，而不是干净的名称「EasyShare 网盘」「EasyShare 共享」，和 WPS/百度网盘的观感不一致。

## 目标

- 「此电脑」里只保留品牌命名空间入口（EasyShare 网盘 / EasyShare 共享），不再出现任何盘符。
- 双击入口直接进入对应的 WebDAV 空间（网盘 = 云端文件，共享 = 局域网共享根目录），无需映射盘符。
- 入口磁贴显示干净名称，描述句仅作为鼠标悬停提示。
- 仅监听回环地址（127.0.0.1），无需 Digest 认证，避免弹出凭据框。

## 设计决策

### 1. 用 Shell File System Folder 委托替代盘符映射

命名空间条目通过注册表注册到 `MyComputer\NameSpace\{CLSID}`，并用 Shell File System Folder 处理器（`{0E5AAE11-A475-4c5b-AB00-C66DE400274E}`）把入口委托到一个文件系统路径：

```text
HKCU\Software\Classes\CLSID\{CLSID}\Instance
    CLSID = {0E5AAE11-A475-4c5b-AB00-C66DE400274E}
HKCU\Software\Classes\CLSID\{CLSID}\Instance\InitPropertyBag
    TargetFolderPath = \\127.0.0.1@PORT\DavWWWRoot
```

`TargetFolderPath` 直接填 WebDAV UNC，双击即可打开，无需 `net use` 映射盘符。这是纯注册表方案，不需要自写 Shell 扩展 DLL。

### 2. WebDAV 仅回环 + 去 Digest 认证

WebDAV 服务只绑定 `127.0.0.1`，本机以外的进程无法访问，因此移除 Digest 认证（`internal/drive/service.go`、`cloud_service.go`），`NewService(root)` / `NewCloudService(fs)` 不再接收凭据。这样资源管理器访问 UNC 时不会弹凭据框。

### 3. 端口与 UNC 约定

- 局域网共享 WebDAV：`config.WebDAVPort`（默认 19080）。
- 网盘 WebDAV：`WebDAVPort + 1`（默认 19081，偏移量 `cloudWebDAVPortOffset = 1`）。
- UNC 由 `namespace.WebDAVUNC(port)` 生成：`\\127.0.0.1@<port>\DavWWWRoot`。

## 关键排障方法（本次重点沉淀）

这一节记录「下次再遇到命名空间入口异常时怎么查」，是本次最有价值的经验。

### 现象 A：入口显示成描述句而不是名称

根因：CLSID 下残留的 `LocalizedString` 会覆盖默认值成为资源管理器显示名；`System.Category` 同理。这些是早期实验留下的值，当前代码并不设置它们，但旧机器上会残留。

确认方法（PowerShell）：

```powershell
$k = Get-Item 'HKCU:\Software\Classes\CLSID\{F6B2A3C4-D5E6-7F8A-9B0C-1D2E3F4A5B6C}'
$k.GetValue('LocalizedString')   # 非空即为元凶
$k.GetValue('System.Category')
$k.GetValue('')                  # 这才是期望显示的名称
```

修复：注册命名空间时删除会劫持显示名的旧值。代码里收敛为 `clearStaleDisplayOverrides`，删除 `LocalizedString`、`System.Category`、`TileInfo`、`System.ItemTypeText`。`InfoTip` 只作悬停提示，不影响显示名，可保留描述句。

### 现象 B：怀疑双击打不开 / 委托没生效

注意：用 `Shell.Application` 读委托文件夹窗口时，`.Document.Folder.Self.Path` 返回的是 CLSID 路径（如 `::{20D04FE0...}\::{F6B2A3C4...}`），这是正常现象，不代表委托失败。要看内容判断：

```powershell
$shell = New-Object -ComObject Shell.Application
foreach ($w in $shell.Windows()) {
    $items = $w.Document.Folder.Items()
    # 看 $items.Count 和逐项 Name,判断是否真的列出了 WebDAV 内容
}
```

往 WebDAV 根目录放一个文件再数窗口条目，是最可靠的「委托是否生效」验收：

```powershell
$body = [Text.Encoding]::UTF8.GetBytes('test')
Invoke-WebRequest -Uri 'http://127.0.0.1:19080/test.txt' -Method Put -Body $body
# 再看命名空间窗口是否出现 test.txt
```

### 现象 C：WebDAV PUT 返回 409 / UNC 列目录为空

Windows WebClient 会自动剥离 `DavWWWRoot` 前缀：访问 `\\127.0.0.1@19080\DavWWWRoot\test.txt` 实际发出 `PUT /test.txt`（不含 DavWWWRoot）。所以 `webdav.Handler` 用 `Prefix: "/"` 就是对的，不要设成 `/DavWWWRoot`，否则路径会多套一层导致 409。手工用 curl/Invoke-WebRequest 测试时也别自己加 `/DavWWWRoot`。

验证 Shell 能否解析 UNC（确认 WebClient 正常）：

```powershell
# SHParseDisplayName / SHCreateItemFromParsingName 返回 hr=0 即可解析
```

### 现象 D：改了注册表但「此电脑」不刷新

Explorer 对命名空间元数据缓存很顽固，`SHChangeNotify` 往往不够。需要重启 Explorer：

```powershell
Stop-Process -Name explorer -Force; Start-Sleep 2; Start-Process explorer
```

## 数据、API 与代码影响

- `internal/namespace/namespace_windows.go`：`DefaultEntries(iconPath, cloudPort, sharePort)` 改为按端口生成 UNC；新增 `WebDAVUNC(port)`；清理函数更名 `clearStaleDisplayOverrides` 并增加删除 `LocalizedString`/`System.Category`。
- `internal/drive/service.go`、`cloud_service.go`：去 Digest 认证，构造函数不再收凭据。
- `internal/api/server.go`：移除 `DriveMapped`、`driveMapper`、`/api/drive/map`、`/api/drive/unmap`；新增 `StartLANDrive()`。
- `cmd/core/main.go`：`ConfigureDrive(drive.NewService(root))`，启动后 `StartLANDrive()`。
- `app.go`：`registerNamespace()` 按端口注册；移除 `MapDrive`/`UnmapDrive`/`cleanStaleMapping`；`SaveSettings` 去掉盘符参数。
- `internal/config/config.go`：移除 `DriveLetter` 字段及校验（旧 config.json 仍可加载，Go 忽略未知字段）。
- 前端：`core.ts`/`useEasyShare.ts`/`DrivePanel.vue`/`SettingsPanel.vue`/`App.vue` 去除盘符与映射相关状态、按钮、表单项。
- 删除死代码：`internal/drive/digest.go`、`mapper_windows.go` 及其测试。

## 测试与验收

- `go build ./...`、`go vet`、`go test ./...`：通过。
- `npm --prefix frontend run build`、`npm --prefix frontend test`：通过。
- `wails build`：生成 `build/bin/easyshare.exe`。
- 真实 Windows 验收（2026-07-20）：
  - 注册表 `TargetFolderPath` 为 UNC（`\\127.0.0.1@19081\DavWWWRoot` / `@19080`），`net use` 无盘符，19080/19081 正常监听。
  - 往共享根放测试文件后，命名空间窗口列出该文件，确认委托生效；测试后已清理。
  - 清除 `LocalizedString`/`System.Category` 并重启 Explorer 后，「此电脑」显示干净名称「EasyShare 网盘」「EasyShare 共享」，与 WPS/百度网盘并排，无盘符。

## 完成记录

- 已完成：去盘符、WebDAV UNC 命名空间入口、去 Digest 认证、显示名修复（清旧值）、前后端与测试同步、真实 Windows 验收。
- 已知约束：纯注册表命名空间入口，不具备 WPS 式容量条、右键菜单、占位文件、按需下载；这些需要真正的 Shell 扩展 DLL 或 CfAPI，属后续方向。
- 文档同步：本迭代记录、`troubleshooting.md` 命名空间排障节、`progress.md`。
