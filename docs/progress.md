# EasyShare 开发进度与路线

> 本文是**进度与路线的唯一真相源**：两条产品主线的阶段/里程碑状态、已完成清单、迭代记录表和待开始优先级都以此为准。
> 根 README 只保留概览；`product-vision.md` 与 `knowledge-platform.md` 负责方向"为什么"，本文负责"到哪了、接下来做什么"。
> 每次迭代开始和结束时更新。最后更新：2026-07-27

## 路线总览

EasyShare 有两条互相支撑的产品主线，通过统一账号与统一对象存储（RustFS）衔接——桌面端是采集触手，服务端是知识大脑（见 [`knowledge-platform.md`](knowledge-platform.md) 第 4 节）：

**主线一：桌面文件产品**（消费级，Wails + Go；方向依据 [`product-vision.md`](product-vision.md)、[`cloudreve-benchmark.md`](cloudreve-benchmark.md)）

| 阶段 | 主题 | 状态 |
| --- | --- | --- |
| 阶段 0 | 局域网可用（发现/传输/WebDAV 入口） | ✅ 2026-07-19 |
| 阶段 1 | 可分发、可日常使用（NSIS/自启动） | ✅ 2026-07-19 |
| 阶段 2 | 产品体验完善（托盘/设置/网盘/拖拽/macOS） | 🔄 进行中 |
| 阶段 3 | 安全加固（设备配对/TLS/凭据保护） | 待开始 |
| 阶段 4 | 云端 MVP（账号/文件元数据/持久任务） | 待开始 |
| 阶段 5 | 同步与原生入口（CfAPI / FileProvider） | 待开始 |
| 阶段 6 | 云端与内网融合（配对/E2EE/就近获取） | 待开始 |

**主线二：企业知识平台**（服务端，Python + Java + WPS；方向依据 [`knowledge-platform.md`](knowledge-platform.md)）

| 里程碑 | 主题 | 状态 |
| --- | --- | --- |
| 里程碑 0 | Python AI 服务骨架（解析→切块→问答） | ✅ 2026-07-22 |
| 里程碑 1 | 文档管线架构落地（清洗产物/版本化索引/评测集；余 OCR、结构切块、Milvus） | 🔄 进行中 |
| 里程碑 1.5 | /lab 问答页（检索+生成+引用溯源） | ✅ 2026-07-26 |
| 里程碑 2 | Java 控制面（账号/权限/文件登记/权限感知检索） | 待开始 |
| 里程碑 3 | WPS 插件（登录/侧边栏/AI 接口） | 待开始 |

## 当前位置

**阶段 2：产品体验完善** — 进行中（文件传输/网盘能力已较完整）

**知识平台里程碑 1** — 进行中（管线闭环与问答实验台已落地，剩 OCR、结构切块与 Milvus）

产品定位正从消费级文件工具向企业知识管理平台演进，详见 [`knowledge-platform.md`](knowledge-platform.md)。

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
- [x] 网盘在线预览：后端声明图片/PDF/文本能力，图片/PDF 使用五分钟 HMAC 内容票据，文本限量 1 MiB 并仅按 UTF-8 纯文本渲染。详见 [`cloudreve-benchmark.md`](cloudreve-benchmark.md) 与 [`iterations/2026-07-25-cloudreve-benchmark-and-preview.md`](iterations/2026-07-25-cloudreve-benchmark-and-preview.md)
- [x] Cloudreve 深度对标：明确稳定 `fileId`、轻量目录层、Upload Session、统一任务、文件事件流与 Windows CfAPI 原型路线。详见 [`iterations/2026-07-25-cloudreve-deep-benchmark.md`](iterations/2026-07-25-cloudreve-deep-benchmark.md)
- [x] 客户端 C1.2 全局活动抽屉与统一任务中心：局域网发送/接收与云上传统一进入 Core 任务真相源；提供跨页面活动入口、三段优先级排序、完整任务中心、新状态/类型展示及旧任务兼容。详见 [`iterations/2026-07-27-client-activity-drawer.md`](iterations/2026-07-27-client-activity-drawer.md)
- [x] 此电脑品牌入口：去盘符，命名空间入口直接委托 WebDAV UNC（类 WPS），去 Digest 认证（仅回环），修复入口显示名（清 LocalizedString 等旧值）
- [x] 拖拽发送：Wails v2.13.0 原生 EnableFileDrop + OnFileDrop 接收真实文件路径，过滤文件夹后弹设备选择浮层，点选设备即发
- [x] 文件夹传输（局域网）：拖入文件夹自动 zip 打包走 TCP 管线（Metadata.Kind=folder），接收端解压到同名目录并删 zip，含 zip slip 防护
- [x] 网盘文件夹上传：「上传文件夹」按钮 + 拖拽上传，X-Object-Key 头保留目录结构（如 photos/2024/img.jpg）
- [x] 网盘拖拽上传：拖拽上下文感知，网盘页直接上传云端，其他页面弹设备选择浮层
- [x] macOS 支持（待真机复验）：平台抽象与构建标签就位，Finder 挂载 WebDAV 作「此电脑」等价入口；托盘改用不接管 Wails `AppDelegate` 的原生 AppKit `NSStatusItem`；`build-mac.sh` 可产出 .app/DMG，并为 universal 桌面端合成 universal Core。详见 [`macos-port.md`](macos-port.md)；GitHub Actions macOS Build 已通过编译、测试、universal 架构校验和产物上传
- [x] 双仓库推送与 macOS CI 日常流程：提交后分别推送 `origin`（Gitee）和 `github`（GitHub）；`dev` 自动构建、手动触发可选架构、`v*` tag 自动创建 Release
- [x] P0 双平台测试 Release：`v0.1.0-test.2` 已验证 macOS/Windows Actions、Prerelease 创建和 6 个 Release 资产下载
- [x] 首个对外预览版：采用 `v0.1.0-preview.1`；所有带连字符的 SemVer 预发布 tag（preview/beta/rc/test）自动标记为 GitHub Prerelease
- [x] Python Local Lab：通过 `http://127.0.0.1:8000/lab` 上传 Office/PDF/文本文件、观察八阶段处理进度并检查三类派生产物；仅用于本地开发测试，不接入 Wails 客户端、不代表最终产品 UI
- [x] Office 文件格式签名校验：在解析前识别旧版 OLE、损坏 OOXML、类型错配与缺少核心结构，向 Local Lab 返回可操作的中文修复提示。详见 [`iterations/2026-07-26-office-format-signature-validation.md`](iterations/2026-07-26-office-format-signature-validation.md)
- [x] 检索质量评测集：30 条标注（黄金 Office 样本 + 6 篇企业文档语料，含权限范围用例），recall@5 / hit@1 / MRR / 片段命中率基线进入 pytest 常规回归；`scripts/eval_retrieval.py` 可切真实 embedding 对比。新增 knowledge-tests CI（ubuntu，dev/master push + PR）补上 Python 测试无 CI 的缺口。详见 [`iterations/2026-07-26-retrieval-eval-harness.md`](iterations/2026-07-26-retrieval-eval-harness.md)
- [x] /lab 知识问答页（里程碑 1.5）：检索 + 生成 + 引用溯源，`/query` contexts 透出 `file_id/version_id`，引用一键打开 clean.md；未配置 LLM 时降级纯检索并明示能力。真机冒烟通过（百炼 embedding + SenseNova LLM 真实链路）。详见 [`iterations/2026-07-26-lab-ask-panel.md`](iterations/2026-07-26-lab-ask-panel.md)
- [x] 清洗规则引擎：结构噪声（跨页页眉页脚/页码，默认开）+ PII 脱敏（手机/身份证/邮箱/地址，默认关）+ 自定义 regex；规则集为 JSON 数据（本地文件过渡，里程碑 2 由 Java 按租户下发同一 schema），manifest 记录逐规则命中数，含 ReDoS/坏配置防护。真实 embedding 语义基线同步留存（评测集全指标 1.000）。详见 [`iterations/2026-07-27-cleaning-rule-engine.md`](iterations/2026-07-27-cleaning-rule-engine.md)

### 迭代记录

| 日期 | 主题 | 状态 |
| --- | --- | --- |
| 2026-07-27 | 客户端 C1.2：全局活动抽屉与统一任务中心前端一期 | 已完成（待手工交互验收） |
| 2026-07-27 | 清洗规则引擎（结构噪声+PII 脱敏+自定义规则）+ 真实 embedding 基线 | 已完成 |
| 2026-07-27 | 文档体系治理：单一真相源重组、断链修复、过时内容清理 | 已完成 |
| 2026-07-26 | /lab 知识问答页（检索+生成+引用溯源，里程碑 1.5） | 已完成 |
| 2026-07-26 | 检索质量评测集与知识面 CI | 已完成（Actions 首跑通过） |
| 2026-07-26 | Office 文件格式签名校验与可操作错误提示 | 已完成 |
| 2026-07-25 | Python 本地文档处理可视化实验台 | 已完成 |
| 2026-07-25 | RustFS 真实集成测试与 Office 黄金语料 | 已完成 |
| 2026-07-25 | Python 文档入库与结构化清洗闭环 | 已完成 |
| 2026-07-25 | Cloudreve 深度对标：文件身份、同步根与任务架构 | 已完成 |
| 2026-07-25 | Cloudreve 对标研究与网盘在线预览 | 已完成 |
| 2026-07-23 | v0.1.0-preview.1 对外预览版发布 | 已完成（待真机验收） |
| 2026-07-23 | 双仓库推送规范与 macOS CI 日常流程 | 已完成 |
| 2026-07-23 | GitHub Actions macOS 自动打包与产物校验 | 已完成（待 Mac 真机验收） |
| 2026-07-23 | macOS AppDelegate 链接冲突修复 | 已完成（待 Mac 复验） |
| 2026-07-23 | macOS 支持（平台抽象 + Finder 挂载 + 构建脚本） | 已完成（待 Mac 实测） |
| 2026-07-19 | 启动即用与双击进入 | 已完成 |
| 2026-07-19 | RustFS 对象存储基础层 | 已完成（Go/Python 真实 RustFS 集成验证通过） |
| 2026-07-19 | NSIS 安装包 | 已完成 |
| 2026-07-19 | 系统托盘 | 已完成（待手工验收） |
| 2026-07-19 | 设置页 | 已完成 |
| 2026-07-19 | Core 异常恢复 | 已完成 |
| 2026-07-19 | 传输体验增强（历史/另存/多文件/速度） | 已完成 |
| 2026-07-19 | 网盘功能（RustFS 接入） | 已完成 |
| 2026-07-20 | 此电脑品牌入口（去盘符 + 显示名修复） | 已完成 |
| 2026-07-20 | 拖拽发送（原生文件拖放 + 设备选择浮层） | 已完成 |
| 2026-07-22 | 文件夹传输 + 网盘文件夹/拖拽上传 | 已完成 |
| 2026-07-22 | 知识平台里程碑 0（Python AI 服务骨架） | 已完成 |
| 2026-07-23 | AI 模型选型与凭据配置（百炼 embedding + SenseNova LLM） | 已完成 |
| 2026-07-23 | 知识平台管线架构选型（Unstructured + PaddleOCR + Milvus + 自写薄编排） | 已完成 |

## 进行中

**P0 双平台发布验收** — CI 与 Release 下载闭环已完成；`v0.1.0-test.2` 的 macOS/Windows workflow 均成功，Release 已标记为 Prerelease 且 6 个资产均可下载。剩余真实 Mac/Windows 安装验收。详见 [`iterations/2026-07-23-platform-release-test.md`](iterations/2026-07-23-platform-release-test.md)。

**知识平台里程碑 1：管线架构落地** — 文档处理闭环和本地可视化实验台已完成：Python 已具备基于 RustFS 对象引用的异步任务、TXT/Markdown/DOCX/文本型 PDF/XLSX/PPTX 结构化解析、Office OLE/OOXML 真实格式预检、清洗产物、版本化索引替换、失败恢复，以及仅回环开放的 `/lab` 测试页面。`/lab` 只为开发验证方便，不进入 Wails 客户端，也不代表最终产品 UI。下一步集中在 PaddleOCR、Unstructured 结构增强和 Milvus，不提前引入 Java。详见 [`iterations/2026-07-25-python-document-cleaning-pipeline.md`](iterations/2026-07-25-python-document-cleaning-pipeline.md) 与 [`iterations/2026-07-25-python-local-lab.md`](iterations/2026-07-25-python-local-lab.md)。

## 待开始（按优先级）

> 知识平台各里程碑的目标与依据见 [`knowledge-platform.md`](knowledge-platform.md)；早期任务分解已归档至 [`archive/knowledge-platform-roadmap.md`](archive/knowledge-platform-roadmap.md)（选型以 knowledge-platform.md 为准）。

1. **知识平台里程碑 1**：接入扫描件 OCR、增强复杂版面与结构感知切块、把当前 JSON 向量存储迁移到 Milvus
2. **知识平台里程碑 2**：Java 控制面接入（账号、权限、文件登记、权限感知检索），走向多用户企业级
3. **知识平台里程碑 3**：WPS 插件（登录、侧边栏、调用 AI 接口）
4. **网盘增强**：稳定 `fileId` 与轻量目录层、可恢复分片上传、统一任务模型、回收站、文件事件流、缩略图，以及 Windows Cloud Files API 独立原型
5. **设备配对与传输加密**
6. **统一版本号**：健康 API、前端 package.json 使用同一版本源

## 已知阻塞

- macOS 托盘链接修复仍需在 Mac 上重跑 `bash scripts/build-mac.sh`，完成 .app/DMG 产出与菜单栏运行验收
- Windows CI（`build-windows.yml`）覆盖 master/PR/tag/手动触发，`dev` 推送刻意不触发以节省 runner 时长；日常 `dev` 开发仍依赖本地 `scripts/build.ps1` 全量验证与真机安装验收

## 版本约定

当前版本：**0.1.0**（开发基线，尚未正式发布）

版本号规则：
- 0.x.y：阶段 0-3 的迭代版本
- 1.0.0：云端 MVP 可用时
- 安装包版本与 wails.json `info.productVersion` 保持一致
