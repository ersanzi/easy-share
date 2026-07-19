# EasyShare NSIS 安装包

> 日期：2026-07-19
> 状态：进行中
> 主题：让 EasyShare 可以作为正式 Windows 应用安装、卸载和自启动

## 用户问题

- 当前用户只能手动复制 `build/bin/` 下的两个 EXE 来使用 EasyShare，没有安装/卸载流程。
- 没有"应用和功能"注册，用户无法通过 Windows 标准方式管理 EasyShare。
- 没有开机自启动选项，用户每次重启后需要手动打开。
- 卸载时如果不清理，会残留进程、网络映射和 WebView2 数据。

## 目标

- 用户双击安装包即可完成安装，桌面和开始菜单出现快捷方式。
- 安装包同时部署 `easyshare.exe` 和 `easyshare-core.exe`。
- 安装完成后可选择立即启动和设置开机自启动。
- 在 Windows"应用和功能"中显示 EasyShare，支持标准卸载。
- 卸载时终止 EasyShare 进程、移除网络映射、清理安装目录和快捷方式。
- 安装界面使用简体中文。
- 版本号统一来自 `wails.json` 的 `info.productVersion`。

## 非目标

- 本次不实现自动更新/升级机制。
- 本次不实现安装包代码签名（需要证书，后续阶段处理）。
- 本次不实现 ARM64 支持，仅 AMD64。
- 本次不实现 MSI 格式，仅 NSIS EXE。
- 本次不实现安装时自动配置 Windows WebClient 服务（仅在文档中提示）。

## 设计决策

### 1. 双进程部署

Wails 默认 NSIS 模板只安装主 EXE。EasyShare 是双进程架构（桌面 + Core），需要在 `project.nsi` 中额外部署 `easyshare-core.exe`。

Core 二进制放在与主程序相同的安装目录，桌面进程通过相对路径或同目录查找启动 Core（当前 `process_windows.go` 已使用同目录逻辑）。

### 2. 卸载清理

卸载时必须：
1. 终止 `easyshare.exe` 和 `easyshare-core.exe`（如果正在运行）。
2. 执行 `net use` 移除 EasyShare 创建的网络映射（通过 UNC 地址匹配）。
3. 删除安装目录、快捷方式、注册表项。
4. 删除开机自启动注册表项。
5. 不删除用户数据（`%LOCALAPPDATA%\EasyShare`、接收文件、共享目录），但在卸载完成页提示用户可手动清理。

### 3. 开机自启动

使用 HKCU `Software\Microsoft\Windows\CurrentVersion\Run` 注册表键，值为安装目录下的 `easyshare.exe` 路径。安装时通过可选组件让用户勾选，卸载时无条件移除。

选择 HKCU 而非 HKLM：EasyShare 是用户级应用，不需要管理员权限运行；自启动也应是当前用户级别。

### 4. 安装级别

使用 `user` 安装级别（`$LOCALAPPDATA\Programs\EasyShare`），不需要管理员权限。这与 Wails 的 `WAILS_INSTALL_SCOPE=user` 一致。

理由：
- 网络驱动器映射是用户级别的。
- 不需要写入 Program Files。
- 降低 UAC 弹窗频率，提升安装体验。

### 5. 版本号统一

在 `wails.json` 中增加 `info` 字段：

```json
{
  "info": {
    "companyName": "EasyShare",
    "productName": "EasyShare",
    "productVersion": "0.1.0",
    "copyright": "Copyright 2026 laifeng"
  }
}
```

NSIS 构建时 Wails 会自动将这些值注入 `wails_tools.nsh` 的模板变量。

### 6. 中文界面

NSIS 内置 `SimpChinese` 语言包。将 `MUI_LANGUAGE` 改为 `SimpChinese`，并自定义欢迎页和完成页文案。

## 实现任务

| 状态 | 任务 |
| --- | --- |
| 已完成 | 在 wails.json 中增加 info 字段（版本号、产品名、版权） |
| 已完成 | 自定义 project.nsi：双进程部署、中文界面、自启动选项、卸载清理 |
| 已完成 | 更新 build.ps1 增加 `--nsis` 构建步骤和 NSIS PATH 检测 |
| 已完成 | 构建验证：`wails build --nsis` 成功生成 EasyShare-amd64-installer.exe |
| 待完成 | 真实 Windows 安装/卸载验收 |
| 待完成 | 更新 README、架构文档和排障文档 |

## 兼容与迁移

- 安装包不影响现有开发模式（`wails dev`、`go run ./cmd/core`）。
- 已手动运行过 EasyShare 的用户安装后，旧的手动副本不会被自动清理；文档中提示用户删除旧副本。
- 安装不修改 `config.json`；首次运行时 Core 仍会在 `%LOCALAPPDATA%\EasyShare` 生成默认配置。
- 卸载不删除用户数据和日志，避免误删共享目录中的用户文件。

## 测试计划

### 安装验收

1. 在干净环境（无 EasyShare）运行安装包，确认无报错。
2. 确认桌面和开始菜单出现快捷方式。
3. 确认"应用和功能"中显示 EasyShare 0.1.0。
4. 双击快捷方式启动，确认 Core 正常启动、网络映射成功。
5. 勾选"开机自启动"后重启，确认 EasyShare 自动启动。

### 卸载验收

1. 运行中的 EasyShare 执行卸载，确认进程被终止。
2. 确认网络映射被移除（`net use` 无 EasyShare 条目）。
3. 确认安装目录、快捷方式、注册表项被清理。
4. 确认用户数据目录（`%LOCALAPPDATA%\EasyShare`）未被删除。
5. 确认开机自启动注册表项被移除。

### 构建验证

```powershell
powershell -ExecutionPolicy Bypass -File scripts/build.ps1
```

确认产物包含：
- `build/bin/easyshare.exe`
- `build/bin/easyshare-core.exe`
- `build/bin/EasyShare-amd64-installer.exe`

## 发布与回滚

- 安装包为单文件 EXE，可直接分发。
- 回滚方式：卸载当前版本，安装旧版本（当前无旧版本，首次发布无需考虑）。
- 后续版本升级时，NSIS 默认覆盖安装同目录文件；如需版本检测，后续迭代增加。

## 完成记录

（待验收后填写）
