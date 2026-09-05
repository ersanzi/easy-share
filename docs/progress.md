# EasyShare 开发进度与路线

> 本文是**进度与路线的唯一真相源**：两条产品主线的阶段/里程碑状态、已完成清单、迭代记录表和待开始优先级都以此为准。
> 根 README 只保留概览；`product-vision.md` 与 `knowledge-platform.md` 负责方向"为什么"，本文负责"到哪了、接下来做什么"。
> 每次迭代开始和结束时更新。最后更新：2026-09-06

## 路线总览

EasyShare 有两条互相支撑的产品主线，通过统一账号与统一对象存储（RustFS）衔接——桌面端是采集触手，服务端是知识大脑（见 [`knowledge-platform.md`](knowledge-platform.md) 第 4 节）：

**主线一：桌面文件产品**（消费级，Wails + Go；方向依据 [`product-vision.md`](product-vision.md)、[`cloudreve-benchmark.md`](cloudreve-benchmark.md)）

| 阶段 | 主题 | 状态 |
| --- | --- | --- |
| 阶段 0 | 局域网可用（发现/传输/WebDAV 入口） | ✅ 2026-07-19 |
| 阶段 1 | 可分发、可日常使用（NSIS/自启动） | ✅ 2026-07-19 |
| 阶段 2 | 产品体验完善（托盘/设置/网盘/拖拽/macOS/悬浮窗） | ✅ 2026-07-29 |
| 阶段 3 | 安全加固（设备配对/TLS/凭据保护） | 🔄 进行中 |
| 阶段 4 | 云端 MVP（账号/文件元数据/持久任务） | 🔄 进行中 |
| 阶段 5 | 同步与原生入口（CfAPI / FileProvider） | 待开始 |
| 阶段 6 | 云端与内网融合（配对/E2EE/就近获取） | 待开始 |

**主线二：企业知识平台**（服务端，Python + Java + WPS；方向依据 [`knowledge-platform.md`](knowledge-platform.md)）

| 里程碑 | 主题 | 状态 |
| --- | --- | --- |
| 里程碑 0 | Python AI 服务骨架（解析→切块→问答） | ✅ 2026-07-22 |
| 里程碑 1 | 文档管线架构落地（清洗产物/版本化索引/评测集/结构感知切块/Milvus） | ✅ 2026-07-29 |
| 里程碑 1.5 | /lab 问答页（检索+生成+引用溯源） | ✅ 2026-07-26 |
| 里程碑 1.8 | 知识质量驾驶舱（单文档透视/策略对比/生成审计/健康度仪表盘） | 🔄 进行中 |
| 里程碑 1.9 | 混合检索加固（BM25 + Reranker + Agent 多跳；2026-09-01 回灌生产 /query） | ✅ 2026-09-01 |
| 里程碑 2 | Java 控制面（账号/权限/文件登记/权限感知检索） | 暂缓（账号部分已由 ADR-0007 RuoYi 控制面落地） |
| 里程碑 3 | WPS 插件（知识副驾驶/主动推送） | 暂缓 |

## 当前位置

**发芽路线：代码侧全部就绪，等公司部署观察（2026-09-01）**。三条线均到「代码完成」：

- **知识平台**：发芽四步（2a 账号登录 / 目录监听 / 桌面端知识问答 / WPS 最小闭环）2026-08-21 完成；里程碑 1.9 检索加固 2026-08-20 完成；1.8 驾驶舱四 Tab 实现余压测；**2b/2c 权限感知检索 2026-09-01 完成**（owner 贯通入库链路，`/query` 按登录用户裁剪可见文档——公司多账号部署的隐私前提）；**一键部署向导 2026-09-01 完成**（`knowledge/scripts/deploy.ps1`，30 分钟手册变一条命令）；**生产检索回灌 2026-09-01 完成**（混合/重排/多跳从驾驶舱接上生产 `/query`，策略部署级配置默认 hybrid，生产查询日志贯通——观察期数据源补齐；对标结论见 [`agent-knowledge-benchmark.md`](agent-knowledge-benchmark.md)）；**Contextual Chunking（P1a）2026-09-05 完成**（入库注入文档级定位摘要前缀，LLM→启发式回退链，`CONTEXTUAL_CHUNKING` 可关）。
- **账号控制面（阶段 4）**：外部批次 P0–P4 已合入并完成本仓回归（Go 18 包/vitest 33/pytest 120/wails build 全绿），KI-5 死代码已清、设置页账号资料已补。剩余真机鼠标验收（见已知阻塞）。
- **插件生态**：插件系统 + 官方自营商城 + 剪切板旗舰插件 2.0（双形态 + Win+V 快捷面板）代码完成，Windows 冒烟过，2026-09-02 真机修复热键回退链与微信 24bpp 图片损坏；插件仓拆分挂触发条件待执行（勿主动）。

产品核心命题："万级文档灌进去，AI 答案能不能直接用？"先验证"值不值得上线"，再解决"能不能上线"。详见 [`knowledge-platform.md`](knowledge-platform.md) 与 [`knowledge-quality-cockpit.md`](knowledge-quality-cockpit.md)；下一步唯一关键路径是照 [`company-rollout-guide.md`](company-rollout-guide.md) 公司部署两周观察。

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
- [x] 客户端 C1.2 全局活动抽屉与统一任务中心：局域网发送/接收与云上传统一进入 Core 任务真相源；提供跨页面活动入口、三段优先级排序、完整任务中心、新状态/类型展示及旧任务兼容；已完成手工交互验收。详见 [`iterations/2026-07-27-client-activity-drawer.md`](iterations/2026-07-27-client-activity-drawer.md)
- [x] 任务中心接收文件夹跟随设置：每次点击都重新读取持久化 `receiveDir`，设置保存后无需重启即可打开新目录；配置损坏时拒绝回退旧缓存；已完成手工交互验收。详见 [`iterations/2026-07-27-receive-folder-follows-settings.md`](iterations/2026-07-27-receive-folder-follows-settings.md)
- [x] 此电脑品牌入口：去盘符，命名空间入口直接委托 WebDAV UNC（类 WPS），去 Digest 认证（仅回环），修复入口显示名（清 LocalizedString 等旧值）
- [x] 拖拽发送：Wails v2.13.0 原生 EnableFileDrop + OnFileDrop 接收真实文件路径，过滤文件夹后弹设备选择浮层，点选设备即发
- [x] 文件夹传输（局域网）：拖入文件夹自动 zip 打包走 TCP 管线（Metadata.Kind=folder），接收端解压到同名目录并删 zip，含 zip slip 防护
- [x] 网盘文件夹上传：「上传文件夹」按钮 + 拖拽上传，X-Object-Key 头保留目录结构（如 photos/2024/img.jpg）
- [x] 网盘拖拽上传：拖拽上下文感知，网盘页直接上传云端，其他页面弹设备选择浮层
- [x] macOS 支持（真机验收通过）：平台抽象与构建标签就位，Finder 挂载 WebDAV 作「此电脑」等价入口；托盘改用不接管 Wails `AppDelegate` 的原生 AppKit `NSStatusItem`；`build-mac.sh` 可产出 .app/DMG，并为 universal 桌面端合成 universal Core。详见 [`macos-port.md`](macos-port.md)；GitHub Actions macOS Build 已通过编译、测试、universal 架构校验和产物上传；2026-07-29 真机验收通过
- [x] 双仓库推送与 macOS CI 日常流程：提交后分别推送 `origin`（Gitee）和 `github`（GitHub）；`dev` 自动构建、手动触发可选架构、`v*` tag 自动创建 Release
- [x] P0 双平台测试 Release：`v0.1.0-test.2` 已验证 macOS/Windows Actions、Prerelease 创建和 6 个 Release 资产下载
- [x] 首个对外预览版：采用 `v0.1.0-preview.1`；所有带连字符的 SemVer 预发布 tag（preview/beta/rc/test）自动标记为 GitHub Prerelease
- [x] Python Local Lab：通过 `http://127.0.0.1:8000/lab` 上传 Office/PDF/文本文件、观察八阶段处理进度并检查三类派生产物；仅用于本地开发测试，不接入 Wails 客户端、不代表最终产品 UI
- [x] Office 文件格式签名校验：在解析前识别旧版 OLE、损坏 OOXML、类型错配与缺少核心结构，向 Local Lab 返回可操作的中文修复提示。详见 [`iterations/2026-07-26-office-format-signature-validation.md`](iterations/2026-07-26-office-format-signature-validation.md)
- [x] 检索质量评测集：30 条标注（黄金 Office 样本 + 6 篇企业文档语料，含权限范围用例），recall@5 / hit@1 / MRR / 片段命中率基线进入 pytest 常规回归；`scripts/eval_retrieval.py` 可切真实 embedding 对比。新增 knowledge-tests CI（ubuntu，dev/master push + PR）补上 Python 测试无 CI 的缺口。详见 [`iterations/2026-07-26-retrieval-eval-harness.md`](iterations/2026-07-26-retrieval-eval-harness.md)
- [x] /lab 知识问答页（里程碑 1.5）：检索 + 生成 + 引用溯源，`/query` contexts 透出 `file_id/version_id`，引用一键打开 clean.md；未配置 LLM 时降级纯检索并明示能力。真机冒烟通过（百炼 embedding + SenseNova LLM 真实链路）。详见 [`iterations/2026-07-26-lab-ask-panel.md`](iterations/2026-07-26-lab-ask-panel.md)
- [x] 清洗规则引擎：结构噪声（跨页页眉页脚/页码，默认开）+ PII 脱敏（手机/身份证/邮箱/地址，默认关）+ 自定义 regex；规则集为 JSON 数据（本地文件过渡，里程碑 2 由 Java 按租户下发同一 schema），manifest 记录逐规则命中数，含 ReDoS/坏配置防护。真实 embedding 语义基线同步留存（评测集全指标 1.000）。详见 [`iterations/2026-07-27-cleaning-rule-engine.md`](iterations/2026-07-27-cleaning-rule-engine.md)
- [x] 扫描件 OCR 最小闭环与来源感知切块：可选 PaddleOCR 支持图片、纯扫描 PDF 与混合 PDF 页级分流；保留页码、块 ID、提取方式、置信度与 bbox，并贯通 manifest、`/health`、向量 metadata 和 `/query` context。详见 [`iterations/2026-07-28-paddleocr-scanned-documents.md`](iterations/2026-07-28-paddleocr-scanned-documents.md)
- [x] 结构感知切块：标题边界分段（H1/H2 硬分段、H3+ 软分段）、层级上下文注入（`[标题层级]` 前缀）、表格完整性保护（独立切块、超大表格按行拆分保留表头）、段内 overlap 不跨段。详见 [`iterations/2026-07-29-milestone1-closure.md`](iterations/2026-07-29-milestone1-closure.md)
- [x] 评测集扩充 30→42 条：同义改写、近义干扰、表格跨行、跨文档干扰四类难例；HashEmbedder 基线 recall@5=0.933 / hit@1=0.900 / mrr=0.917 / snippet=0.900
- [x] Milvus 向量库迁移：docker-compose Standalone（etcd + Milvus，对象存储复用 RustFS）、pymilvus 可选依赖、`MilvusVectorStore` 同接口适配（IVF_FLAT + COSINE + doc_id Trie 索引）、配置开关留空退回 JSON
- [x] 统一版本号：`internal/version/version.go` 常量 + 健康 API 引用 + `frontend/package.json` 同步 0.1.0，三处版本源对齐
- [x] 知识质量驾驶舱第一期（里程碑 1.8）：单文档透视（结构化块 + 切块地图 + 清洗 Diff + 管线统计）+ 检索调试 + 生成审计（prompt 透视 + 逐句忠实度标注），`/debug/` API 仅回环可访问。详见 [`iterations/2026-08-04-cockpit-cleaning-diff.md`](iterations/2026-08-04-cockpit-cleaning-diff.md)（清洗 Diff 为第一期收尾，与驾驶舱同批次）
- [x] 驾驶舱第二期：BM25 混合检索对比（vector/bm25/hybrid 三列并排，RRF 融合）+ 知识健康度 API（规模/新鲜度/覆盖/使用率/盲区五维）
- [x] 查询日志 + 健康度真实数据：`QueryLog` 记录检索/生成事件，使用率/盲区/生成质量（平均忠实度、无依据比例）可度量，非占位数据
- [x] Reranker 精排接入：`app/kb/reranker.py`（OpenAI 兼容 rerank API + NoopReranker 回退），`/debug/query` 第四策略 "reranked"（混合 RRF → Cross-Encoder 精排），驾驶舱升级四列对比
- [x] 驾驶舱第四 Tab 健康度仪表盘：六维指标卡片 + 文档命中排行/僵尸文档/盲区查询/格式覆盖四象限
- [x] 清洗 Diff 视图（驾驶舱第一期收尾）：规则引擎记录逐动作明细（整块删除/文本改写，含 before/after），manifest 内嵌持久化，UI 删除线 + 红色背景标注规则名与命中计数。详见 [`iterations/2026-08-04-cockpit-cleaning-diff.md`](iterations/2026-08-04-cockpit-cleaning-diff.md)
- [x] 桌面端集成知识问答（发芽路线第 3 步）：Core 作知识网关（登录/健康/问答 5 个代理端点，令牌存 Core 侧 knowledge.json 不进前端），桌面端「知识」页登录 + 会话式问答 + 引用折叠，令牌失效自动回登录页。详见 [`iterations/2026-08-21-desktop-knowledge-qa.md`](iterations/2026-08-21-desktop-knowledge-qa.md)
- [x] WPS 最小闭环（发芽路线第 4 步）：知识服务自托管加载项（/wps 同源免跨域），WPS 文字「知识」页签选中段落一键查询，任务窗格登录 + 引用展示；本机安装脚本登记 jsaddons。详见 [`iterations/2026-08-21-wps-minimal-loop.md`](iterations/2026-08-21-wps-minimal-loop.md)
- [x] 托盘悬停浮窗（切片 1，2026-08-31 外部批次合入）：Windows 侧改用原生 `Shell_NotifyIcon` + `NOTIFYICON_VERSION_4` 获得悬停事件（`getlantern/systray` 不具备且无法配置获得），浮窗为独立线程上的 Win32 窗口内嵌 WebView2，标题栏含图标/名称/头像/设置，点设置显示主窗口；定位基于 `Shell_NotifyIconGetRect`，适配任务栏四边缘与多显示器负坐标；已移除 systray 及其 8 个传递依赖。详见 [`iterations/2026-08-28-tray-hover-widget.md`](iterations/2026-08-28-tray-hover-widget.md)
- [x] 客户端在线升级（2026-08-31）：升级源为 RuoYi 控制面（platform-drive 发布接口，安装包存 RustFS、预签名直传不经控制面）；Windows 全自动「检查→下载（SHA256 校验）→重启并更新→NSIS 静默安装→自动重启」，macOS 检测+引导下载；设置页「关于与更新」卡片 + 启动 24h 节流自动检查（设置入口红点）；`publish-release.ps1` 一条命令发布。真机 UI 全流程验收通过（0.1.0→0.1.1）。详见 [`iterations/2026-08-31-online-update.md`](iterations/2026-08-31-online-update.md)
- [x] P4 空间挂载（随外部批次合入，2026-08-31 收尾迭代对账确认）：`spacemount.go` + `internal/spacedav`（建在 drive 控制面客户端之上，每个文件操作经控制面，配额/授权对资源管理器生效），登录后按账号实际拥有的空间挂「此电脑」条目——个人盘「<昵称> 的网盘」（19082）+ 共享盘「EasyShare 共享」（19083，只读授权挂盘但拒写），登出/配额收回/授权撤销自动卸载；浮窗空间切换器 + 拖放上传（`SetDropSpace`/`uploadDroppedToSpace`）；旧 19081 云盘 WebDAV 永久下线。双平台 `namespace.SpaceEntries` 同一模型（darwin 只读标注重漏由 macOS CI 捕获修复）
- [x] 控制面批次收尾（2026-08-31）：文档对账（本条 + architecture/README/known-issues/合并文档勘误——此前文档把 P4 误记为"剩余"）+ KI-5 死代码清理（`/api/cloud/*` 七路由、`cloud.Service`、`webdavfs`、desktop.Client Cloud* 方法，净删约 1390 行）+ 设置页「账号」资料卡片（头像/昵称/账号/管理员标识 + 空间用量 + 退出登录）。详见 [`iterations/2026-08-31-account-plane-closure.md`](iterations/2026-08-31-account-plane-closure.md)
- [x] 悬浮窗布局重构与固定态（切片 2，2026-08-31 外部批次合入）：浮窗加高按内容分区、固定态（固定是文件拖放的前提）、托盘图标 GUID 持久化与窗口位置/固定状态持久化。详见 [`iterations/2026-08-29-hover-widget-layout.md`](iterations/2026-08-29-hover-widget-layout.md)
- [x] 2b 文件归属 / 2c 权限感知检索（2026-09-01）：owner 贯通任务表/manifest/索引元数据（令牌用户优先防伪造，watcher 监听目录为共享），`/query` 服务端按登录用户裁剪可见文档（共享文档所有人可见、owner 文档本人+admin、空交集短路），未登录行为不变；向量库双后端新增 `doc_owners()`；桌面端/WPS 经 Core 网关透传令牌零改动生效。pytest 新增 8 用例、全量 128 全绿；真实服务双账号冒烟隔离验证通过。详见 [`iterations/2026-09-01-permission-aware-retrieval.md`](iterations/2026-09-01-permission-aware-retrieval.md)
- [x] 部署提效一键 bootstrap（2026-09-01）：`knowledge/scripts/deploy.ps1` 一键向导（Python 检查/RustFS 复用或 Docker 起新/venv 清华镜像/桶初始化/.env 生成/启动探活/管理员+批量同事账号/防火墙放行/自启/同事使用指引.txt），交互+无人值守双模式；`docker-compose.rustfs.yml`；`start_server.ps1`/`install_autostart.ps1` 加 -Port；**修存量缺口：requirements.txt 补 httpx（MinerU client 无条件 import，旧手册部署必炸）**；手册第一节重写为一键部署并补防火墙步骤。隔离目录真跑全流程 + 放文件自动入库 + 同事账号检索命中。详见 [`iterations/2026-09-01-deploy-bootstrap.md`](iterations/2026-09-01-deploy-bootstrap.md)
- [x] 生产检索回灌（2026-09-01）：1.9 的混合检索/重排/多跳从 `/debug` 驾驶舱接上生产 `/query`——新增 `QueryOrchestrator` 编排层，`QUERY_STRATEGY` 部署级配置（默认 hybrid，vector 可回滚，multi_hop 无 LLM 自动降级），响应透出实际策略与降级说明；生产查询/生成事件落 `QueryLog`（观察期使用率/盲区数据源此前为空）；向量库双后端补 `count()`/`snapshot_records()` 协议，修 Milvus 无 `records` 属性导致 `/health`、BM25 降级、驾驶舱聚合崩溃的隐藏 bug。pytest 新增 5 用例、全量 133 全绿。对标背景与 P1/P2 后续（contextual chunking/OKF 化/FAQ 沉淀）见 [`agent-knowledge-benchmark.md`](agent-knowledge-benchmark.md)。详见 [`iterations/2026-09-01-production-retrieval-upgrade.md`](iterations/2026-09-01-production-retrieval-upgrade.md)
- [x] Contextual Chunking（2026-09-05，P1a）：入库时为每块注入文档级定位摘要前缀（`[文档] 摘要` + `[标题路径]` 双层），对标 Anthropic Contextual Retrieval；LLM 每文档一次摘要、失败/未配置退启发式（永不阻塞入库），启发式仅注入无标题上下文段落；先装箱后加前缀消除边界漂移，manifest 留痕 `contextual`，`CONTEXTUAL_CHUNKING` 一键开关。42 条评测：哈希口径噪声内持平（MRR −0.002 为单个 trigram 碰撞事件，附向量分解证明）、真实 embedding 口径双向饱和 1.000——**现评测集无增益测量空间**，上下文敏感性难例扩充进收件箱。pytest 140 全绿。详见 [`iterations/2026-09-05-contextual-chunking.md`](iterations/2026-09-05-contextual-chunking.md)
- [x] 观察期周报脚本（2026-09-05，P2）：`scripts/weekly_report.py` 从 QueryLog 严格窗口聚合（新 `windowed_stats`，与驾驶舱全时段口径分离）生成五节中文周报——使用率/检索命中/盲区查询/生成质量/事实触发的观察提示；空数据兜底、只读不改状态、UTC+8 显示。服务两周观察决策的周期输入，部署机上 `--days 14` 直接跑。pytest 144 全绿。详见 [`iterations/2026-09-05-querylog-weekly-report.md`](iterations/2026-09-05-querylog-weekly-report.md)
- [x] 剪切板插件「开机自动记录」开关（2026-09-05，P2 批次 3 余项）：应用内读写 HKCU Run 键（与 NSIS 安装器同名同键，卸载自然清理），`clipboard.settings` 扩展 `autoStart/autoStartSupported`，插件侧栏新开关行（不支持平台隐藏），manifest 2.1.0；录制随应用启动恢复本就有（08-31 起），本切片补的是「开机 → 自启 → 记录」链路的应用内可控。go build/test 全绿。详见 [`iterations/2026-09-05-clipboard-autostart-toggle.md`](iterations/2026-09-05-clipboard-autostart-toggle.md)
- [x] 网盘目录导航（2026-09-06，用户指定网盘优化·Cloudreve 对标第一片）：文件夹进入/面包屑/跨目录搜索/时间·名称·大小排序/统计徽标/上传落当前目录——`driveFolder.ts` 从扁平 key 推导目录视图（权威目录层将来落控制面，本模块换数据源即可）；`CloudUpload/CloudUploadFolder` 加 targetDir 走完整 Wails 级联，拖拽/悬浮窗语义不变。详见 [`iterations/2026-09-06-drive-folder-navigation.md`](iterations/2026-09-06-drive-folder-navigation.md)

### 迭代记录

| 日期 | 主题 | 状态 |
| --- | --- | --- |
| 2026-09-06 | 网盘目录导航 — 文件夹进入/面包屑/跨目录搜索/排序/上传落当前目录（Cloudreve 对标「轻量目录层」客户端最小实现，视图推导不造第二真相源；targetDir 走 Wails 级联） | 已完成（vue-tsc 干净 + vitest 40 全绿 +7 用例 + go 绿；真机点验随下轮打包） |
| 2026-09-05 | 剪切板插件「开机自动记录」开关（批次 3 余项）— HKCU Run 键应用内读写（与 NSIS 同名同键），clipboard.settings 能力扩展 + 插件侧栏开关，manifest 2.1.0；录制随应用恢复本就有，补的是 OS 自启链路的应用内可控 | 已完成（go build/test 全绿 +3 用例；真机开关冒烟与商城发布归欠账/日常链路） |
| 2026-09-05 | 观察期周报脚本 — QueryLog 严格窗口聚合（windowed_stats）+ 五节中文周报（使用率/命中/盲区/生成质量/事实提示），空数据兜底，只读不改状态 | 已完成（pytest 144 全绿 +4 用例；空库/旧库/有数据三路冒烟过） |
| 2026-09-05 | Contextual Chunking — 入库时为每块注入文档级定位摘要前缀（LLM 摘要→启发式回退链，对标 Anthropic；先装箱后加前缀消混杂，哈希碰撞噪声分析） | 已完成（回归 140 绿；42 条评测哈希口径噪声内持平/真实口径饱和 1.000 双向持平；LLM 真链路冒烟过） |
| 2026-09-02 | Linux 生产服务器部署资产 — deploy/server-linux（compose 容器化 + deploy.sh 引导 + update.sh 秒级更新自动回退）+ ship-control-plane.ps1 + build.ps1 -PlatformUrl 构建期注入 | 已完成（代码侧；go test 全绿/compose config/PS5.1 解析过；**真实服务器端到端待部署验收**） |
| 2026-09-02 | 剪切板真机修复 — 热键回退链重排（Win+V→Win+Alt+V→Ctrl+Alt+V→Alt+Shift+V，面板底部显示实际生效热键）+ 微信 24bpp DIB 行对齐（图片斜纹/通道错位根因） | 已完成（单测+全量回归绿+安装版真机冒烟过：热键弹出/脚注展示/合成 DIB 全像素/真实微信截图无损） |
| 2026-09-01 | 生产检索回灌 — 混合/重排/多跳接上生产 /query（QueryOrchestrator + QUERY_STRATEGY + 生产查询日志）+ 修 Milvus records 隐藏 bug；附 Agent 知识库六年演进对标（agent-knowledge-benchmark.md） | 已完成（pytest 133 全绿） |
| 2026-09-01 | 部署提效一键 bootstrap — deploy.ps1 向导（RustFS 分支/venv 镜像/.env 生成/账号/防火墙/自启/使用一页纸）+ 修 httpx 依赖缺口 | 已完成（隔离目录全流程真跑 + 端到端入库/检索验证） |
| 2026-09-01 | 2b 文件归属 / 2c 权限感知检索 — owner 落 job/manifest/索引元数据，/query 按登录用户过滤可见文档（多账号公司部署前置） | 已完成（pytest 128 全绿 + 双账号真实链路冒烟隔离验证） |
| 2026-09-01 | 剪切板旗舰插件 + 全局快捷面板 — 插件 2.0 重构（天分组/收藏/分类/双形态）+ Win+V / ⌘⇧V 独立小窗 + 自动粘贴 + darwin 剪切板监听；当日追加：改普通可卸载插件（首启种子+商城更新）| 代码完成（回归绿+构建过+Windows GUI 冒烟过+已上架商城；macOS 运行行为待真机） |
| 2026-08-31 | 插件系统 + 官方自营商城 — 沙箱 iframe 运行时 + 权限化能力 API + 内置剪切板插件（不可卸载）+ platform-drive 商城 + 待办周报插件 | 代码完成（回归绿+构建过+冒烟过；商城端到端与真机交互验收遗留） |
| 2026-08-31 | 控制面批次收尾 — P4 挂载对账（文档纠偏）+ KI-5 死代码清理 + 设置页账号资料 | 已完成（回归绿+构建过；真机鼠标验收遗留） |
| 2026-08-31 | 客户端在线升级（控制面托管）— 检查/下载/校验/静默安装全自动（Windows）+ platform-drive 发布接口 | 已完成（UI 全流程验收通过 0.1.0→0.1.1） |
| 2026-08-31 | 合入外部协作者批次 — RuoYi 账号控制面 P0–P3 + 托盘悬浮窗 + 空间/配额/池上限（快照基点 f5524f9） | 已完成（本仓回归进行中） |
| 2026-08-29 | 账号控制面 P2：按用户隔离的存储授权 — 预签名 URL 直传，客户端不再持 RustFS 凭据 | 已完成（隔离验收 9/9；桌面端 UI 链路待真机补验） |
| 2026-08-29 | 账号控制面 P1：桌面登录 + 登录态贯通（头像跟随账号） | 已完成（待手工交互验收） |
| 2026-08-29 | 账号控制面 P0：RuoYi-Vue-Plus 6.0 环境落地（PG+Redis+登录） | 已完成 |
| 2026-08-30 | 空间池上限与物理容量感知 — es_space 空间/配额模型 + CapacityService 池上限 | 已完成（随外部批次合入，待回归） |
| 2026-08-29 | 悬浮窗布局重构与固定态（切片 2：加高分区+固定按钮+托盘图标GUID持久化） | 已完成（待手工交互验收） |
| 2026-08-28 | 托盘悬停浮窗（切片 1：悬停链路） | 已完成（待手工交互验收） |
| 2026-08-21 | WPS 最小闭环 — 选中段落查知识引用的最小加载项（发芽路线第 4 步） | 已完成（**真机验收通过**：jspluginonline 在线登记，选中→查询→引用全链路） |
| 2026-08-21 | 桌面端集成知识问答 — 登录态 + 问答面板长进 Wails 桌面端，打通桌面端 ↔ 知识服务（发芽路线第 3 步） | 已完成（全量回归通过；真实服务手工验收待公司部署合并） |
| 2026-08-20 | 种下去冲刺 — 目录监听自动入库 + /lab 登录闭环 + 部署脚本 + 公司使用指引；真实端到端冒烟通过 | 已完成（全量回归 120 passed） |
| 2026-08-20 | 薄控制面 2a：账号与登录（发芽路线第一步） | 已完成（全量回归 114 passed） |
| 2026-08-20 | 产品定位与破局讨论 — 竞品调研收敛为 product-positioning.md，定发芽路线 | 已完成（战略讨论，详见定位文档） |
| 2026-08-20 | MCP Server 暴露（1.9 收官切片）— stdio 薄桥 + knowledge_query/health 工具 | 已完成（全量回归 109 passed；官方客户端端到端冒烟通过） |
| 2026-08-20 | BM25 检索降级（1.9 第二切片）— Embedding 故障自动转 BM25，索引懒构建 | 已完成（全量回归 102 passed） |
| 2026-08-20 | Agent 多跳检索（1.9 首切片）— 分轮检索 + 预算控制 + hop 级日志 + 驾驶舱可视化 | 已完成（全量回归 97 passed；真实 LLM 增益验证归入统一测试） |
| 2026-08-20 | 解析即验收 — /lab 验收摘要条 + 驾驶舱 ?doc 预选联动 + 引用文档时间 | 已完成（JS 语法与全量回归通过，浏览器端待人工冒烟） |
| 2026-08-20 | 知识时效最小闭环 — ingested_at 贯通管线/检索/生成，提示文档时效 | 已完成（全量回归 89 passed） |
| 2026-08-20 | 三层解析路由实现 — pdf-inspector 路由 + MinerU 深度解析 + PaddleOCR 兜底 | 已完成（全量回归通过；MinerU 真实服务冒烟待部署） |
| 2026-08-20 | pdf-inspector 路由层增补 — 三层解析路由定稿（快路由/深解析/页级兜底），三项业务想法获批入路线图 | 已完成（设计增补） |
| 2026-08-20 | MinerU 解析集成设计 — PDF 专项可选 Provider（分流回退/配置/映射表定稿） | 已完成（纯设计，实现待排期） |
| 2026-08-20 | Agent 开发基础设施 — 安装 5 个方法论技能（grilling/tdd/diagnosing-bugs/handoff/research）+ /iterate /verify 原生命令 | 已完成 |
| 2026-08-20 | Matt Pocock Skills 评估 — 工程方法论技能包分级吸收结论（5 装戒 + 设计模式借鉴 + issue tracker 流派不装） | 已完成（纯调研，无代码变更） |
| 2026-08-20 | TencentDB-Agent-Memory 对标学习 — 分层记忆/资产治理/按需知识，产出 1.9/里程碑 2/3 分级借鉴建议 | 已完成（纯调研，无代码变更） |
| 2026-08-04 | 驾驶舱清洗 Diff：规则引擎动作明细 + Diff 视图 | 已完成（全量回归通过） |
| 2026-07-31 | 驾驶舱第四 Tab：健康度仪表盘可视化 | 已完成 |
| 2026-07-31 | Reranker 精排接入 — 混合检索后 Cross-Encoder 重排序 | 已完成 |
| 2026-07-31 | 查询日志 + 健康度真实数据 — 使用率/盲区/生成质量可度量 | 已完成 |
| 2026-07-30 | 驾驶舱第二期 — BM25 混合检索对比 + 知识健康度 API | 已完成 |
| 2026-07-30 | 知识质量驾驶舱第一期 — 单文档透视 + 检索调试 + 生成审计 | 已完成 |
| 2026-07-29 | 里程碑 1 收尾：结构感知切块 + 评测扩充 + Milvus 迁移 + 统一版本号 | 已完成（全量回归通过） |
| 2026-07-28 | 扫描件 OCR 最小闭环与来源感知切块 | 已完成（全量回归通过） |
| 2026-07-27 | 任务中心接收文件夹跟随设置 | 已完成（手工交互验收通过） |
| 2026-07-27 | 客户端 C1.2：全局活动抽屉与统一任务中心前端一期 | 已完成（手工交互验收通过） |
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

**Linux 生产服务器部署资产**（2026-09-02 开工，当日完成代码侧）— 公司部署观察期启动，服务器为 Linux，全套上线：新增 `deploy/server-linux/`（compose：PG/Redis/RustFS/RuoYi 容器化，RustFS 9000 对 LAN 开放修正预签名直传坑；deploy.sh 一键引导；update.sh 知识服务秒级更新+失败自动回退，跟 dev 分支）+ 开发机 `scripts/ship-control-plane.ps1`（jar 只能开发机构建，传包+建表+重启+探活）；客户端指向服务器改为**构建期注入**（`build.ps1 -PlatformUrl`，`internal/config` 默认地址 const→var 走 ldflags，同事开箱即用）。验证：go test 全绿 / bash -n / compose config / PS5.1 解析全过；**真实服务器端到端待明天部署时验收**。详见 [`iterations/2026-09-02-linux-server-deploy.md`](iterations/2026-09-02-linux-server-deploy.md)。

**剪切板旗舰插件 + 全局快捷面板**（2026-09-01 开工，当日完成）— 插件系统批次 3 两项（插件独立小窗口 + darwin 剪切板）与插件旗舰化合并推进：剪切板插件重构 2.0（`plugins/clipboard/`，已改为**普通可卸载插件**：首启种子安装、更新走商城、卸载即停录制收面板；源码在插件工程，主仓 embed 直读做种子）——按天分组卡片/收藏/分类/搜索/明暗双主题，同一代码带 `?panel=1` 紧凑面板形态；宿主新增「快捷面板」表面——全局热键（Win+V，被占自动回退 Win+Shift+V；mac ⌘⇧V）唤起独立小窗（Win: Win32+WebView2，mac: NSPanel+WKWebView），面板内选中条目即复制并自动粘贴回之前的焦点窗（Win+V 语义）；macOS 侧 NSPasteboard 轮询监听补齐 darwin 剪切板能力。验证：Go 回归/wails build 全绿；**Windows 真机端到端冒烟通过**（热键回退链→面板弹出→实时历史→Enter 复制→焦点切回→自动粘贴落字）；macOS 编译由 CI 把关、运行行为待真机。**2026-09-02 真机修复**：热键回退链重排为 Win+V→Win+Alt+V→Ctrl+Alt+V→Alt+Shift+V（Win+V 被系统剪贴板历史占、旧回退 Ctrl+Shift+V 抢走各应用「粘贴为纯文本」），实际生效热键在面板底部展示；微信 24bpp DIB 行尾对齐填充解析修复（图片斜纹+通道错位根因）。详见 [`iterations/2026-09-02-clipboard-hotkey-image-fix.md`](iterations/2026-09-02-clipboard-hotkey-image-fix.md) 与 [`iterations/2026-09-01-clipboard-flagship-panel.md`](iterations/2026-09-01-clipboard-flagship-panel.md)。

**桌面端插件系统 + 官方自营插件商城**（2026-08-31 开工，当日完成）— 插件 = Web 包（manifest + HTML/JS/CSS）跑在沙箱 iframe，经权限化能力 API（storage/剪切板/通知/云盘上传）调宿主，唯一动态通道 `PluginInvoke` 避开绑定级联；**剪切板记录为内置插件（不可卸载、目录被删重启即恢复）**，Win32 监听 + JSONL 环形截断 + 图片 LRU；商城由 platform-drive 承载（`es_plugin` 三表 + 两段式预签名发布，平移在线升级链路），前端「插件中心」商城/已装双 tab；**待办周报插件（`plugins/todo/`）已作为首个商城插件真实上架**（周报聚合 + 复制 + 存个人云盘）。验证：Go 18 包回归/前端构建/wails build 全绿；插件全链路冒烟（安装/鉴权/内置保护/serve 安全）通过；**商城端到端（发布→匿名列表→预签名下载→SHA256→Go 客户端安装）全部通过**。剩余：真机 UI 验收（剪切板实时记录/插件中心交互/iframe 桥，见迭代文档）。详见 [`iterations/2026-08-31-plugin-system.md`](iterations/2026-08-31-plugin-system.md)。

**账号控制面（阶段 4 云端 MVP，外部批次已合入）** — 决策见 [ADR-0007](adr/0007-account-control-plane-ruoyi.md)：统一账号采用 **RuoYi-Vue-Plus 6.0**（Java 控制面，调和了主线一 Go 单体与主线二 Java 控制面的选型冲突——账号归 RuoYi，`easyshare-core` 退回本机采集/传输层）。P0：RuoYi 6.0 跑在 PostgreSQL 16 + Redis 上，登录返回 JWT（`deploy/ruoyi-db/`）。P1：桌面客户端登录门禁 + 登录态贯通（主界面与悬浮窗头像跟随账号、登出、点头像进设置）。P2：按用户隔离的存储授权——控制面模块 `platform-drive/` 签发短期预签名 URL，客户端不再持任何 RustFS 凭据，对象键落在 `users/{userId}/` 下，跨用户隔离 9/9 验收通过；**KI-2 关闭、KI-1/KI-4 顺带修掉、KI-3 的用户隔离部分关闭**（稳定文件身份仍未做），新登记 KI-5。P3 已随批次落地：客户端自绘管理页（`AdminPanel.vue`，账号/注册开关/空间配额一页管）+ `es_space` 空间授权与配额模型 + 池上限与物理容量感知（`CapacityService`，方案见 [`plans/2026-08-30-space-pool-and-organize.md`](plans/2026-08-30-space-pool-and-organize.md)，整理算法部分待实施）。P4（浮窗空间切换器 + 按用户命名空间的资源管理器挂载）与设置页账号资料均已落地——前者随外部批次（`spacemount.go` + `internal/spacedav`，收尾迭代对账确认），后者随收尾迭代，KI-5 死代码（约 1390 行）同批清除，详见 [`iterations/2026-08-31-account-plane-closure.md`](iterations/2026-08-31-account-plane-closure.md)。剩余：真机鼠标验收（见已知阻塞）。详见 [`iterations/2026-08-29-account-control-plane-p0.md`](iterations/2026-08-29-account-control-plane-p0.md)、[`iterations/2026-08-29-account-p1-desktop-login.md`](iterations/2026-08-29-account-p1-desktop-login.md)、[`iterations/2026-08-29-account-p2-storage-isolation.md`](iterations/2026-08-29-account-p2-storage-isolation.md)。

**托盘悬浮窗（切片 1、2 已合入）** — 阶段 2 内的新增能力。切片 1：Windows 侧用原生 `Shell_NotifyIcon` + `NOTIFYICON_VERSION_4` 替换 `getlantern/systray`（该库无悬停事件且无法配置获得），浮窗为独立线程上的 Win32 窗口内嵌 WebView2。切片 2：浮窗加高按内容分区、固定态（固定是文件拖放的前提——悬停态下鼠标一旦离开图标浮窗即收起，无法作为拖放目标）、窗口位置与固定状态持久化。「桌面右下角常驻悬浮窗」与「托盘图标悬停」为**同一窗口的两种状态**，不另建部件。剩余真实鼠标交互验收。详见 [`iterations/2026-08-28-tray-hover-widget.md`](iterations/2026-08-28-tray-hover-widget.md) 与 [`iterations/2026-08-29-hover-widget-layout.md`](iterations/2026-08-29-hover-widget-layout.md)。

**知识平台主线：业务流跑通**（2026-08-20 用户指示）——压测统一后置，优先把解析→RAG→交付的端到端价值闭环跑通。三层解析路由（pdf-inspector 路由 + MinerU 深度解析 + PaddleOCR 兜底）代码已落地并通过全量回归，待真实 mineru-api 部署后冒烟；设计文档见 [`iterations/2026-08-20-mineru-parsing-provider-design.md`](iterations/2026-08-20-mineru-parsing-provider-design.md)（含 §4.8 pdf-inspector 增补），实现记录见 [`iterations/2026-08-20-three-tier-pdf-routing.md`](iterations/2026-08-20-three-tier-pdf-routing.md)。

**知识平台里程碑 1.8：知识质量驾驶舱** — 四个 Tab 均已落地；**批量压测后置**（2026-08-20 决策：与其他测试统一做），驾驶舱人工验收待业务流跑通时一并执行。

**P0 双平台发布验收** — CI 与 Release 下载闭环已完成；`v0.1.0-test.2` 的 macOS/Windows workflow 均成功，Release 已标记为 Prerelease 且 6 个资产均可下载。macOS 真机验收已于 2026-07-29 通过。详见 [`iterations/2026-07-23-platform-release-test.md`](iterations/2026-07-23-platform-release-test.md)。

## 待开始（按优先级）

> 2026-08-20 战略讨论后主线改为**发芽路线**（定位与依据见 [`product-positioning.md`](product-positioning.md)，执行决策见其 §三）：先让产品在自己公司被真实使用，战略问题挂起待发芽后回答。

1. **用户侧动作（非开发）**：照 [`company-rollout-guide.md`](company-rollout-guide.md) 在公司部署，建同事账号，真实文档入监听目录，两周看使用率——种子发芽与否的第一手答案；WPS 真机冒烟随部署一并做（装 WPS 的机器跑 `install_wps_addon.ps1`）
2. **质量体检报告获客入口**：上传 10 份文档出体检报告（种子发芽后作为接触制造业客户的钩子）
3. **"问过的不再问第二遍"**（候选 idea，定位文档 §三）：问答自动沉淀为知识资产，待真实使用验证形态
4. **插件仓拆分**（待触发，不主动执行）：`plugins/` 拆为独立仓库——触发条件与完整步骤见 [`plans/2026-09-01-plugin-repo-split.md`](plans/2026-09-01-plugin-repo-split.md)（插件 ≥3 个 / 外部协作者 / 发布节奏倒挂 / 客户定制，满足任一即启动）
5. **插件批次 3 余项**（待排期）：AI 周报接知识服务（开机自启记录已随 2026-09-05 切片完成，见 [`iterations/2026-09-05-clipboard-autostart-toggle.md`](iterations/2026-09-05-clipboard-autostart-toggle.md)；darwin 剪切板与插件独立小窗已随 2026-09-01 剪切板旗舰切片完成）
6. **1.8 批量压测**（后置）：与其他测试统一做
7. **知识平台里程碑 2 全量**（暂缓）：Java 控制面按需拆分（薄切片路径替代，见定位文档；登录权限迁 Java 已定长期方向；2b/2c 权限感知已由 2026-09-01 切片在 Python 侧落地，迁移时平移语义）
8. **网盘增强**（进行中，参考 [`cloudreve-benchmark.md`](cloudreve-benchmark.md)）：目录导航/搜索/排序/上传落当前目录已落地（2026-09-06，见 [`iterations/2026-09-06-drive-folder-navigation.md`](iterations/2026-09-06-drive-folder-navigation.md)）；后续既定顺序=控制面权威目录层 + fileId（解锁回收站/版本/KI-3 关闭）→ Upload Session 断点续传（均需 platform-drive Java 端配合）；设备配对 / 传输加密继续暂缓
9. **知识服务向量库切 Milvus**（待触发，不主动执行）：观察期用进程内 JSON + numpy 余弦即可；触发条件（chunk 逼近 10 万 / 检索 P95 > 500ms / 多 worker 扩展，满足任一）与十分钟切换步骤见 [`company-rollout-guide.md`](company-rollout-guide.md) §四「向量库演进」

## 已知阻塞

> 历史遗留欠账总账（2026-09-01 整理）：全部为**需真实操作/真机**的验收项，代码侧已就绪；不阻塞公司部署启动，可与部署验收一并做。

- **真机鼠标验收欠账（账号控制面，需真实操作）**：① 登录 → 上传 → 换账号看列表（P2 桌面端 UI 链路只验过编译与类型） ②「此电脑」空间盘挂载/卸载与换账号重挂 ③ 悬浮窗悬停/固定/拖放交互（切片 1+2） ④ 设置页账号卡片与退出登录
- **插件系统真机 UI 验收（2026-08-31 起）**：复制文本/图片 → 剪切板历史 → 插件中心商城/已装交互 → 商城装 todo → 周报复制/上传（API 级端到端已过，UI 交互未验）；「开机自动记录」开关真机点验（写 Run 键 → 重启自启 → 录制恢复，2026-09-05 起）——存量安装需商城发布插件 2.1.0 后才可见
- **剪切板旗舰插件 2.0 的 macOS 运行行为（2026-09-01 起）**：Windows 真机端到端冒烟已过；darwin 剪切板监听/⌘⇧V 面板/自动粘贴编译由 CI 把关，运行行为待 Mac 真机
- **macOS 托盘链接修复重跑**：需在 Mac 上重跑 `bash scripts/build-mac.sh`，完成 .app/DMG 产出与菜单栏运行验收
- **MinerU 冒烟**：三层解析路由代码已落地（2026-08-20），待真实 mineru-api 部署后冒烟（公司部署时可一并）
- **1.8 批量压测**：2026-08-20 决策与其他测试统一做，保持后置
- 本仓回归已于合入迭代完成（Go 18 包全绿 / vitest 33 / pytest 120 / wails build），后续以各迭代自验为准；2026-09-01 起 pytest 全量为 133（+权限切片 8、+生产检索编排 5）
- Windows CI（`build-windows.yml`）覆盖 master/PR/tag/手动触发，`dev` 推送刻意不触发以节省 runner 时长；日常 `dev` 开发仍依赖本地 `scripts/build.ps1` 全量验证与真机安装验收

## 版本约定

当前版本：**0.1.1**（2026-08-31 起，随在线升级功能发布；经控制面升级通道真机验证）

版本号规则：
- 0.x.y：阶段 0-3 的迭代版本
- 1.0.0：云端 MVP 可用时
- 安装包版本与 wails.json `info.productVersion` 保持一致
- 发布新版：改三处版本号（`internal/version/version.go`、`wails.json`、`frontend/package.json`）→ `scripts/build.ps1` → `scripts/publish-release.ps1` 上传控制面
