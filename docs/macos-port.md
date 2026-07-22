# EasyShare macOS 支持指南

> 本文记录 EasyShare 的 macOS 移植：平台差异、构建方式、原生集成与排障。
> 状态：**代码已 mac-ready**（平台抽象与构建标签就位，internal 包与 Core 可通过 `GOOS=darwin` 交叉编译验证），但 `.app` 实际构建与运行验证必须在 Mac 上完成（Wails 依赖 macOS WebKit/CGO，无法从 Windows 交叉编译）。
> 最后更新：2026-07-23

## 1. 支持范围

网络核心完全跨平台，无需改动：局域网设备发现（UDP）、文件传输（TCP）、网盘（RustFS/S3）、WebDAV 服务。

平台相关能力做了对等实现：

| 能力 | Windows | macOS |
| --- | --- | --- |
| 「此电脑」品牌入口 | Shell NameSpace 注册表条目 | **Finder 挂载 WebDAV 卷**（侧边栏可见） |
| 系统托盘 | systray + ICO 图标 | systray 菜单栏 + PNG 图标 |
| 后台 Core 启动 | `easyshare-core.exe` + 隐藏窗口 | `easyshare-core` + 独立会话（Setsid） |
| 磁盘/卷浏览 | 枚举盘符（kernel32） | 枚举 `/Volumes` 挂载卷（statfs） |
| 配置路径 | `%LOCALAPPDATA%\EasyShare` | `~/Library/Application Support/EasyShare` |
| 日志路径 | `%LOCALAPPDATA%\EasyShare\logs` | `~/Library/Caches/EasyShare/logs` |
| 安装包 | NSIS（.exe） | DMG（.app） |
| 开机自启 | 注册表 Run 键 | LaunchAgent（见 §5） |

macOS 上**没有**的概念：盘符、注册表、COM Shell 扩展（`build/shellext/` 仅 Windows 用，macOS 构建不涉及）。

## 2. 代码结构（平台抽象）

平台差异通过 Go 构建标签隔离，跨平台代码不带标签：

```
internal/fsutil/
  fsutil.go            # 跨平台：DriveInfo/FileEntry/ListDir
  fsutil_windows.go    # //go:build windows — 盘符枚举（kernel32）
  fsutil_darwin.go     # //go:build darwin  — /Volumes 卷枚举（statfs）

internal/desktop/
  process.go           # 跨平台：ProcessOptions/EnsureCore/CoreHealthy
  process_windows.go   # //go:build windows — coreBinaryName(.exe)/隐藏窗口
  process_darwin.go    # //go:build darwin  — coreBinaryName/Setsid

internal/namespace/
  namespace_windows.go # //go:build windows — 注册表 NameSpace
  namespace_darwin.go  # //go:build darwin  — Finder 挂载 WebDAV

internal/config/config.go  # DefaultConfigPath() 跨平台路径

tray.go                # 跨平台：startTray
tray_windows.go        # //go:build windows — 嵌入 icon.ico
tray_darwin.go         # //go:build darwin  — 嵌入 trayicon.png
```

## 3. 构建（在 Mac 上）

```bash
# 前置：Xcode CLT、Go、Node、Wails CLI
xcode-select --install
go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0

# 一键构建（Core + .app + DMG，自动生成 iconfile.icns）
bash scripts/build-mac.sh
```

产物：`build/bin/easyshare-core`、`build/bin/easyshare.app`、`build/bin/EasyShare.dmg`。

> 无法从 Windows 交叉编译 `.app`：Wails 在 macOS 上用系统 WKWebView + CGO，必须在 macOS 上 `wails build`。
> 但平台相关的 Go 代码可在任意平台用 `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./internal/... ./cmd/core` 验证编译。

### 3.1 CI 构建（推荐，日常无需 Mac）

仓库带 `.github/workflows/build-mac.yml`，在 GitHub 的 `macos-latest` 构建机上自动出 `.app`/`.dmg`，**本地不需要有 Mac**：

- **触发**：① Actions 页面手动 Run workflow（可随时按需构建，可选 `darwin/universal|arm64|amd64`）；② 推送 `v*` 标签时自动构建并发 GitHub Release。
- **取产物**：Actions 运行记录的 Artifacts 里下载 `EasyShare-macOS`；tag 构建还在 Releases 附带 DMG。
- **前提**：工作流只在 GitHub 上运行。主仓库在 Gitee，需镜像到 GitHub（见 §3.2）。Gitee Go 虽也有 macOS 运行器，但免费分钟数消耗快，故选用 GitHub Actions（公开仓库免费）。

### 3.2 Gitee → GitHub 镜像

```bash
# 1. 在 GitHub 建空仓库（如 <user>/easy-share），不要初始化 README
# 2. 本地仓库加 GitHub 远程，并让 origin 同时推往两处：
git remote set-url --add --push origin <gitee-url>
git remote set-url --add --push origin https://github.com/<user>/easy-share.git
# 3. 推送（此后 git push origin master --tags 会同时更新 Gitee 与 GitHub）
git push origin master --tags
```

推上去后 GitHub Actions 即识别工作流；之后每次推送/打 tag 都会自动出 mac 包。

## 4. 「此电脑」等价：Finder 挂载 WebDAV

Windows 的「此电脑」品牌入口在 macOS 没有对应物。macOS 版采用 **Finder 挂载 WebDAV 卷**：

- 启动时 `namespace.Register` 挂载网盘卷（`WebDAVPort+1`）与共享卷（`WebDAVPort`），挂载后出现在 **Finder 侧边栏与桌面**，双击即可进入，体验最接近 Windows 的「此电脑」条目。
- 挂载是**真机相关行为，无法在 Windows 验证**，因此做了多策略 fallback + 详细日志，便于在 Mac 上快速定位：
  1. **幂等检测**：先看 `mount` 输出里该 URL 是否已挂载，已挂载则跳过（避免每次启动重复挂）。
  2. **策略 1**：`osascript -e 'mount volume "http://127.0.0.1:PORT/"'`（Finder 原生，无需管理挂载点，最贴合系统；无认证 WebDAV 可能弹连接/认证框）。
  3. **策略 2**：`mount_webdav <url> /Volumes/<名称>`（命令行直挂；创建 /Volumes 挂载点与 mount 可能需要管理员权限）。
  4. 单卷失败不阻断其他卷；每步结果（含命令输出）都经 `namespace.Log` 写入日志。
- **日志位置（排查首选）**：macOS 上桌面端日志在 `~/Library/Caches/EasyShare/logs/desktop.log`。挂载走了哪条策略、为何失败，都在这里。借 Mac 验证时第一时间看这个文件。
- 退出时 `Unregister` 卸载：优先按 URL 从 `mount` 找到挂载点 `diskutil unmount force`，找不到再按卷名 eject（best-effort）。

已知限制（待 Mac 上实测调优）：
- 挂载卷的显示名由 WebDAV 服务端响应决定，不一定能精确显示为「EasyShare 网盘」。
- 无认证的回环 WebDAV，Finder 可能弹出访客/匿名连接提示。
- 若两条策略都因权限失败，日志会明确给出原因（如 Operation not permitted），届时需评估是否改用 FileProvider（见产品规划）。

## 5. 开机自启（LaunchAgent）

macOS 用 LaunchAgent 替代 Windows 注册表 Run 键。在 `~/Library/LaunchAgents/com.easyshare.app.plist` 放置：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.easyshare.app</string>
    <key>ProgramArguments</key>
    <array>
        <string>/Applications/easyshare.app/Contents/MacOS/easyshare</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
</dict>
</plist>
```

加载：`launchctl load ~/Library/LaunchAgents/com.easyshare.app.plist`。
（路径按实际安装位置调整；后续可在安装器或设置页中自动写入。）

## 6. 待 Mac 侧完成 / 验证

- [ ] 在 Mac 上运行 `scripts/build-mac.sh`，确认 `.app` 与 DMG 产出。
- [ ] 实测 Finder WebDAV 挂载：能否挂载、显示名、双击进入、退出卸载。
- [ ] 实测菜单栏图标外观（当前复用应用图标，建议替换为黑白 template 图以适配深浅色）。
- [ ] 实测局域网发现/传输在 macOS 防火墙下的行为（首次可能弹防火墙授权）。
- [ ] 生成正式 `iconfile.icns`（脚本已自动从 appicon.png 生成，可替换为设计稿）。
- [ ] 代码签名与公证（Gatekeeper），便于分发。

## 7. 排障

| 现象 | 原因与解法 |
| --- | --- |
| Windows 上 `wails build -platform darwin` 失败 | 预期行为——macOS 版必须在 Mac 上构建（WebKit/CGO） |
| Mac 上 `go build` 报 fsutil/namespace/desktop 未定义 | 确认这些包有 `_darwin.go` 文件且带 `//go:build darwin` 标签 |
| Finder 不挂载 WebDAV | 确认 Core 已启动且 WebDAV 端口可达；`curl http://127.0.0.1:19080/` 验证；查看 osascript 报错 |
| 菜单栏图标过大/颜色不对 | 当前用应用图标占位，替换 `build/darwin/trayicon.png` 为 22px 黑白 template 图 |
| 配置文件找不到 | macOS 在 `~/Library/Application Support/EasyShare/config.json`（非 LOCALAPPDATA） |
| `.app` 打不开（Gatekeeper） | 系统设置→隐私与安全性→仍要打开；或签名公证 |
