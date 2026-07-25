# EasyShare Windows MVP 验收清单

> 本清单用于 Windows 10/11 发布前的手工验收。自动化测试和构建命令见 [开发指南](../development.md)，故障定位见 [排障指南](../troubleshooting.md)。

## 验收前准备

- 使用普通用户启动 EasyShare，不要单独运行 `easyshare-core.exe`。
- 确认测试机的 Windows `WebClient` 服务可用。
- 确认 `Z:` 未被本地磁盘、网络共享或其他程序占用。
- 保留默认 Windows WebDAV 策略 `BasicAuthLevel=1`；EasyShare 使用 Digest 认证，不要求修改为不安全的 `BasicAuthLevel=2`。
- 如需测试设备发现和文件传输，准备两台处于同一可信局域网的 Windows 设备。

## 构建与启动

- 运行 `powershell -ExecutionPolicy Bypass -File scripts/build.ps1`。
- 确认 `build/bin/easyshare.exe` 和 `build/bin/easyshare-core.exe` 均存在。
- 启动 `easyshare.exe`，首次启动时允许 Windows 防火墙中的专用网络访问。
- 确认 Core API 监听 `127.0.0.1:19079`，且程序没有弹出额外的 Core 控制台窗口。
- 检查 `%LOCALAPPDATA%\EasyShare\logs\desktop.log` 和 `core.log`，确认没有启动失败或持续重试错误。

## 设备发现与传输

- 在两台 Windows 10/11 设备上启动 EasyShare。
- 确认双方在七秒内显示对方设备。
- 选择单个文件并发送，接收方应看到待确认任务。
- 分别验证接受和拒绝操作。
- 验证大文件传输时内存不会随文件大小线性增长。
- 验证同名文件生成 `名称 (1).扩展名`，不覆盖已有文件。
- 传输中断开网络，确认任务显示失败而不是完成。

## WebDAV 与 Z 盘

- 在 `Z:` 空闲且未映射时双击启动 `easyshare.exe`，确认无需点击按钮即可自动启动 WebDAV 并出现 `Z:`。
- 确认 WebDAV 监听 `127.0.0.1:19080`，认证方式为 Digest。
- 确认映射目标使用 Windows WebClient 路径 `\\127.0.0.1@19080\DavWWWRoot`，而不是 HTTP URL。
- 在“此电脑”中双击 `Z:` 进入，完成新建、写入、读取、重命名和删除文件的验证。
- 保持自身映射再次启动桌面端，确认复用现有映射，不报盘符占用。
- 自动映射失败后等待多个轮询周期，确认不会反复执行映射；释放盘符并点击“重新连接”后成功。
- 确认默认共享目录 `%USERPROFILE%\EasyShare` 中的内容与 `Z:` 一致。
- `Z:` 已被其他程序占用时，EasyShare 应显示错误且不替换原映射。
- 取消映射后确认资源管理器中的 `Z:` 消失，等待多个轮询周期仍不自动恢复，日志中没有反复映射/取消映射错误。

## 进程生命周期

- 关闭 EasyShare 窗口，确认 `easyshare-core.exe` 仍在运行。
- 再次启动 `easyshare.exe`，确认连接现有兼容 Core，而不是重复启动。
- 快速连续启动两个桌面端，确认启动竞争不会产生持续的端口占用错误，也不会留下多个 Core。
- 点击“退出全部服务”，确认依次取消 `Z:` 映射、停止 WebDAV、取消后台 context 并退出 Core。
- 确认退出后的前端停留在“服务已安全退出”页面，不再轮询已退出的 Core。
- 使用任务管理器确认 `easyshare-core.exe` 和 `easyshare.exe` 均已退出。
- 运行 `net use`，确认没有残留的 EasyShare `Z:` 映射。
- 再次启动 EasyShare，确认 Core 可以正常绑定 `127.0.0.1:19079`，WebDAV 和映射仍可重新启动。

## 日志与配置

- 确认配置写入 `%LOCALAPPDATA%\EasyShare\config.json`。
- 确认运行日志写入 `%LOCALAPPDATA%\EasyShare\logs`，并至少包含 `desktop.log` 与 `core.log`。
- 制造一次可恢复错误（例如占用 `Z:` 后尝试映射），确认 UI 有可理解提示，日志中有时间、操作和底层错误信息。
- 确认日志不会记录 WebDAV 密码、API Token 或待传输文件内容。

## 已知环境条件

- Windows WebClient 服务必须允许 WebDAV 网络驱动器访问。
- 企业防火墙策略可能阻止 UDP `9527` 或 TCP `9528`。
- 当前 MVP 面向可信局域网，不提供设备配对或传输加密。
- 当前只支持单文件传输，不支持目录、多文件批量、断点续传或云端存储。


