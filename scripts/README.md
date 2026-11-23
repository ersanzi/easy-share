# scripts 目录说明

> 长期资产放本目录顶层；一次性排障工具放 [`diag/`](diag/)。新增脚本时对号入座，
> 用途不明的脚本宁可不进版本库。

## 顶层：长期资产（构建与验收流水线的一部分）

| 脚本 | 用途 |
| --- | --- |
| `build.ps1` | Windows 全量流水线：Go 测试 → 前端测试/构建 → Core 编译 → Wails + NSIS 安装包 |
| `check-release-version.mjs` | 三处产品版本与 tag 一致性闸门 |
| `check-release-artifact.mjs` | 构建/发布两端绑定安装资产的版本、提交、目标和 SHA256 |
| `publish-release.mjs` / `publish-release.ps1` | 正式 tag 控制面发布器与 PowerShell 兼容入口；幂等/防覆盖/匿名下载回验，配置见开发手册 |
| `build-mac.sh` | macOS 构建：universal `.app`/DMG + `lipo` 合成 universal Core |
| `create_bucket.go` | RustFS 建桶工具（凭据从环境变量读，缺失即退出；真值在 `deploy/rustfs/.env`） |
| `verify-drive-isolation.sh` | P2 存储隔离验收：跨用户列表不可见、路径穿越拒绝、未登录 401（9 项断言） |
| `verify-space-quota.py` | 空间配额/池上限验收（配合控制面活栈） |
| `bucket-usage.py` | RustFS 桶用量按前缀聚合（排查配额与实际用量偏差） |

## 发布守卫与回归（长期闸门，位于 diag/ 的既有命令体系）

`node scripts/diag/check-release-pipeline.mjs` 复算正式 tag → 本次平台构建资产 → 控制面发布的调用/门槛/凭据边界，并自测删除发布调用等回退必须报红。`node --test scripts/diag/release-version.test.mjs scripts/diag/publish-release.test.mjs` 在独立临时目录与 loopback HTTP mock 验证版本/资产绑定、正常上传回验、幂等、不同正文防覆盖与失败脱敏；不会向生产发布。二者由 Quality Gates、tag 构建及 Windows 本地 build 调用。

## diag/：一次性排障工具（2026-08-28~29 外部批次带回）

开发期定位特定问题用的快照工具，不保证跨环境可用，留档供同类问题参考：

| 脚本 | 当时定位的问题 |
| --- | --- |
| `check-popup-style.ps1` | 悬浮窗弹窗样式（置顶/无边框）是否生效 |
| `diagnose-drop.ps1` | 文件拖放无反应时的注册表与监听诊断 |
| `dump-namespace.ps1` | Shell NameSpace 注册表项导出（「此电脑」入口显示名异常） |
| `inspect-drop-windows.ps1` | Windows 拖放链路（OLE/COM 注册）检查 |
| `screenshot.ps1` | 定时截屏（悬浮窗自动收起时序观察） |
