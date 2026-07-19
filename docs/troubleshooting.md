# EasyShare 故障排查

## 1. 先收集日志

日志目录：

```text
%LOCALAPPDATA%\EasyShare\logs
```

按 `Win + R` 粘贴上述路径即可打开。复现一次后按时间查看：

| 文件 | 主要内容 |
| --- | --- |
| `desktop.log` | Wails 启动、Core 连接、API 操作、Vue/浏览器异常 |
| `core.log` | Core 生命周期、发现、接收、WebDAV、`net use` 完整错误 |
| `core-process.log` | 子进程 stdout/stderr、正常日志初始化前的 panic |
| `desktop.log.1` / `core.log.1` | 5 MiB 轮转备份 |

日志可用于排障，但不要发送 `config.json`，因为它包含 API Token 和 WebDAV 密码。

## 2. 网络驱动器错误 67

典型日志：

```text
System error 67 has occurred.
The network name cannot be found.
```

按顺序检查：

1. 确认正在运行的是最新的 `easyshare.exe` 和配套的 `easyshare-core.exe`。
2. 在 `core.log` 中确认：

   ```text
   WebDAV ready at http://127.0.0.1:19080 with Digest authentication
   ```

3. 检查 Windows WebClient：

   ```powershell
   Get-Service WebClient | Format-List Name,Status,StartType
   ```

4. 确认目标 UNC 为：

   ```text
   \\127.0.0.1@19080\DavWWWRoot
   ```

当前实现使用 Digest Authentication，兼容 Windows 默认 `BasicAuthLevel=1`。不要把系统注册表改成 `BasicAuthLevel=2`，也不要退回 HTTP Basic Auth。

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

## 5. 盘符被占用或无法取消映射

检查盘符：

```powershell
net use Z:
Get-PSDrive -Name Z -ErrorAction SilentlyContinue
```

EasyShare 启动时会自动尝试连接一次。它会复用远端地址完全匹配的自身映射，但不会覆盖已有的其他映射，也不会删除远端地址不匹配的映射。若 UI 显示盘符占用，请释放该盘符后点击“重新连接”，或在配置中改用空闲盘符；状态轮询不会反复自动重试。

手工清理前先确认输出确实指向：

```text
\\127.0.0.1@19080\DavWWWRoot
```

确认后才可执行：

```powershell
net use Z: /delete /y
```

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

