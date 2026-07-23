# EasyShare 开发进度

> 本文是开发者和 AI 协作的进度快照。每次迭代开始和结束时更新。
> 最后更新：2026-07-23

## 当前位置

**阶段 2：产品体验完善** — 进行中（文件传输/网盘能力已较完整）

**新方向：企业知识管理平台** — 启动中（里程碑 0：Python AI 服务最小骨架）

阶段 0（局域网可用）和阶段 1（可分发）已全部完成并通过验收。产品定位正从消费级文件工具向企业知识管理平台演进，详见 [`knowledge-platform.md`](knowledge-platform.md)。

## 已完成

### 阶段 0：局域网可用（2026-07-19 完成）

- [x] UDP 局域网设备发现（端口 9527，2s 广播，7s 过期）
- [x] TCP 流式文件发送/接收，支持接受/拒绝（端口 9528）
- [x] 本地 WebDAV 服务 + Digest 认证（端口 19080）（Digest 认证已于 2026-07-20 移除，改为仅回环无认证）
- [x] 启动自动映射网络驱动器（Z:），幂等复用，安全取消（盘符已于 2026-07-20 移除，改为「此电脑」命名空间入口直接委托 WebDAV UNC）
- [x] Apple/macOS 风格 Vue 3 单页界面
- [x] 桌面端/Core/前端三层文件日志（5 MiB 轮转）
- [x] 重复 Core 检测（HMAC 身份校验）
- [x] 有序退出：取消映射 → 停止 WebDAV → 取消后台 → 退出 Core
- [x] 生产构建流水线（scripts/build.ps1）
- [x] RustFS 对象存储基础层（接口 + S3 adapter + 内存 fake，未接入 Core）

### 阶段 1：可分发、可日常使用（2026-07-19 完成）

- [x] NSIS 安装包：双进程部署、中文界面、用户级安装
- [x] 可选开机自启动（注册表 Run 键）
- [x] 卸载清理：终止进程、移除网络映射、删除快捷方式和注册表
- [x] 统一版本号（wails.json info.productVersion = 0.1.0）
- [x] build.ps1 集成 `wails build --nsis` + NSIS PATH 自动检测
- [x] 真实 Windows 安装/卸载验收通过

### 阶段 2：产品体验完善（进行中）

- [x] 系统托盘：关闭最小化到托盘、右键菜单、双击恢复、Page Visibility 降频轮询
- [x] Frameless 无边框窗口：去除 Windows 套 macOS 嵌套感，自定义窗口控制条
- [x] 设置页：设备名称、接收目录、共享目录、映射盘符可视化配置
- [x] Core 异常恢复：启动清理残留映射、watchdog 健康探测与自动重启、配置热加载
- [x] 传输历史持久化：终态任务写入 history.json，重启后恢复，支持一键清除
- [x] 接收另存为：接收时可选自定义目录（默认接收 / 另存 / 拒绝三按钮）
- [x] 单条记录删除 + 传输用时显示
- [x] 多文件发送：多选文件对话框，循环逐文件发送
- [x] 传输速度实时高亮：传输中速度蓝色加粗显示
- [x] 网盘功能（RustFS）：文件上传/列表/下载/删除/分享链接，设置页 RustFS 连接配置
- [x] 此电脑品牌入口：去盘符，命名空间入口直接委托 WebDAV UNC（类 WPS），去 Digest 认证（仅回环），修复入口显示名（清 LocalizedString 等旧值）
- [x] 拖拽发送：Wails v2.13.0 原生 EnableFileDrop + OnFileDrop 接收真实文件路径，过滤文件夹后弹设备选择浮层，点选设备即发
- [x] 文件夹传输（局域网）：拖入文件夹自动 zip 打包走 TCP 管线（Metadata.Kind=folder），接收端解压到同名目录并删 zip，含 zip slip 防护
- [x] 网盘文件夹上传：「上传文件夹」按钮 + 拖拽上传，X-Object-Key 头保留目录结构（如 photos/2024/img.jpg）
- [x] 网盘拖拽上传：拖拽上下文感知，网盘页直接上传云端，其他页面弹设备选择浮层
- [x] macOS 支持（待真机复验）：平台抽象与构建标签就位，Finder 挂载 WebDAV 作「此电脑」等价入口；托盘改用不接管 Wails `AppDelegate` 的原生 AppKit `NSStatusItem`；`build-mac.sh` 可产出 .app/DMG，并为 universal 桌面端合成 universal Core。详见 [`macos-port.md`](macos-port.md)

### 迭代记录

| 日期 | 主题 | 状态 |
| --- | --- | --- |
| 2026-07-23 | GitHub Actions macOS 自动打包与产物校验 | 进行中 |
| 2026-07-23 | macOS AppDelegate 链接冲突修复 | 已完成（待 Mac 复验） |
| 2026-07-23 | macOS 支持（平台抽象 + Finder 挂载 + 构建脚本） | 已完成（待 Mac 实测） |
| 2026-07-19 | 启动即用与双击进入 | 已完成 |
| 2026-07-19 | RustFS 对象存储基础层 | 已完成（待 Docker 集成验证） |
| 2026-07-19 | NSIS 安装包 | 已完成 |
| 2026-07-19 | 系统托盘 | 已完成（待手工验收） |
| 2026-07-19 | 设置页 | 已完成 |
| 2026-07-19 | Core 异常恢复 | 已完成 |
| 2026-07-19 | 传输体验增强（历史/另存/多文件/速度） | 已完成 |
| 2026-07-19 | 网盘功能（RustFS 接入） | 已完成 |
| 2026-07-20 | 此电脑品牌入口（去盘符 + 显示名修复） | 已完成 |
| 2026-07-20 | 拖拽发送（原生文件拖放 + 设备选择浮层） | 已完成 |
| 2026-07-22 | 文件夹传输 + 网盘文件夹/拖拽上传 | 已完成 |
| 2026-07-22 | 知识平台里程碑 0（Python AI 服务骨架） | 进行中 |

## 进行中

**GitHub Actions macOS 自动打包** — GitHub 仓库与 SSH 推送链路已打通，正在重新登记并运行 macOS workflow，目标是稳定产出经过架构校验的 `.app.zip`、DMG 与 SHA-256 校验文件。详见 [`iterations/2026-07-23-github-macos-packaging.md`](iterations/2026-07-23-github-macos-packaging.md)。

**知识平台里程碑 0：Python AI 服务最小骨架** — 已搭建 `knowledge/` 服务并跑通端到端管线（入库/检索/权限过滤已验证），待配置真实 embedding/LLM 做语义验收。详见 [`knowledge-platform.md`](knowledge-platform.md)。

## 待开始（按优先级）

> 知识平台各里程碑的详细任务、交付物与验收标准见 [`knowledge-platform-roadmap.md`](knowledge-platform-roadmap.md)。

1. **知识平台里程碑 1**：扩展解析能力（docx/pdf/xlsx 多格式）、更好的切块与清洗、真正的向量库
2. **知识平台里程碑 2**：Java 控制面接入（账号、权限、文件登记、权限感知检索），走向多用户企业级
3. **知识平台里程碑 3**：WPS 插件（登录、侧边栏、调用 AI 接口）
4. **网盘增强**：大文件分片、断点续传、在线预览（图片/PDF/文本）
5. **设备配对与传输加密**
6. **统一版本号**：健康 API、前端 package.json 使用同一版本源

## 已知阻塞

- Docker daemon 不可用，RustFS 集成测试暂时跳过（不影响当前阶段）
- macOS 托盘链接修复仍需在 Mac 上重跑 `bash scripts/build-mac.sh`，完成 .app/DMG 产出与菜单栏运行验收
- macOS GitHub Actions 正在完成首次真实 runner 构建；Windows 尚无 CI，仍依赖本地 `scripts/build.ps1` 全量验证

## 版本约定

当前版本：**0.1.0**（开发基线，尚未正式发布）

版本号规则：
- 0.x.y：阶段 0-3 的迭代版本
- 1.0.0：云端 MVP 可用时
- 安装包版本与 wails.json `info.productVersion` 保持一致
