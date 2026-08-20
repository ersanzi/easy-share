# EasyShare 开发进度与路线

> 本文是**进度与路线的唯一真相源**：两条产品主线的阶段/里程碑状态、已完成清单、迭代记录表和待开始优先级都以此为准。
> 根 README 只保留概览；`product-vision.md` 与 `knowledge-platform.md` 负责方向"为什么"，本文负责"到哪了、接下来做什么"。
> 每次迭代开始和结束时更新。最后更新：2026-08-20

## 路线总览

EasyShare 有两条互相支撑的产品主线，通过统一账号与统一对象存储（RustFS）衔接——桌面端是采集触手，服务端是知识大脑（见 [`knowledge-platform.md`](knowledge-platform.md) 第 4 节）：

**主线一：桌面文件产品**（消费级，Wails + Go；方向依据 [`product-vision.md`](product-vision.md)、[`cloudreve-benchmark.md`](cloudreve-benchmark.md)）

| 阶段 | 主题 | 状态 |
| --- | --- | --- |
| 阶段 0 | 局域网可用（发现/传输/WebDAV 入口） | ✅ 2026-07-19 |
| 阶段 1 | 可分发、可日常使用（NSIS/自启动） | ✅ 2026-07-19 |
| 阶段 2 | 产品体验完善（托盘/设置/网盘/拖拽/macOS） | ✅ 2026-07-29 |
| 阶段 3 | 安全加固（设备配对/TLS/凭据保护） | 🔄 进行中 |
| 阶段 4 | 云端 MVP（账号/文件元数据/持久任务） | 待开始 |
| 阶段 5 | 同步与原生入口（CfAPI / FileProvider） | 待开始 |
| 阶段 6 | 云端与内网融合（配对/E2EE/就近获取） | 待开始 |

**主线二：企业知识平台**（服务端，Python + Java + WPS；方向依据 [`knowledge-platform.md`](knowledge-platform.md)）

| 里程碑 | 主题 | 状态 |
| --- | --- | --- |
| 里程碑 0 | Python AI 服务骨架（解析→切块→问答） | ✅ 2026-07-22 |
| 里程碑 1 | 文档管线架构落地（清洗产物/版本化索引/评测集/结构感知切块/Milvus） | ✅ 2026-07-29 |
| 里程碑 1.5 | /lab 问答页（检索+生成+引用溯源） | ✅ 2026-07-26 |
| 里程碑 1.8 | 知识质量驾驶舱（单文档透视/策略对比/生成审计/健康度仪表盘） | 🔄 进行中 |
| 里程碑 1.9 | 混合检索加固（BM25 + Reranker + Agent 多跳） | 待开始 || 里程碑 2 | Java 控制面（账号/权限/文件登记/权限感知检索） | 暂缓 |
| 里程碑 3 | WPS 插件（知识副驾驶/主动推送） | 暂缓 |

## 当前位置

**知识平台里程碑 1.8：知识质量驾驶舱** — 进行中（四个 Tab 已实现，余批量压测与人工验收）

**桌面端阶段 3（安全加固）** — 暂缓，优先验证管线质量

产品核心命题："万级文档灌进去，AI 答案能不能直接用？"先验证"值不值得上线"，再解决"能不能上线"。详见 [`knowledge-platform.md`](knowledge-platform.md) 与 [`knowledge-quality-cockpit.md`](knowledge-quality-cockpit.md)。

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

### 迭代记录

| 日期 | 主题 | 状态 |
| --- | --- | --- |
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

**知识平台主线：业务流跑通**（2026-08-20 用户指示）——压测统一后置，优先把解析→RAG→交付的端到端价值闭环跑通。三层解析路由（pdf-inspector 路由 + MinerU 深度解析 + PaddleOCR 兜底）代码已落地并通过全量回归，待真实 mineru-api 部署后冒烟；设计文档见 [`iterations/2026-08-20-mineru-parsing-provider-design.md`](iterations/2026-08-20-mineru-parsing-provider-design.md)（含 §4.8 pdf-inspector 增补），实现记录见 [`iterations/2026-08-20-three-tier-pdf-routing.md`](iterations/2026-08-20-three-tier-pdf-routing.md)。

**知识平台里程碑 1.8：知识质量驾驶舱** — 四个 Tab 均已落地；**批量压测后置**（2026-08-20 决策：与其他测试统一做），驾驶舱人工验收待业务流跑通时一并执行。

**P0 双平台发布验收** — CI 与 Release 下载闭环已完成；`v0.1.0-test.2` 的 macOS/Windows workflow 均成功，Release 已标记为 Prerelease 且 6 个资产均可下载。macOS 真机验收已于 2026-07-29 通过。详见 [`iterations/2026-07-23-platform-release-test.md`](iterations/2026-07-23-platform-release-test.md)。

## 待开始（按优先级）

> 知识平台各里程碑的目标与依据见 [`knowledge-platform.md`](knowledge-platform.md)；早期任务分解已归档至 [`archive/knowledge-platform-roadmap.md`](archive/knowledge-platform-roadmap.md)（选型以 knowledge-platform.md 为准）。
> 2026-08-20 调整：压测统一后置，主线改为业务流跑通；以下顺序按用户当日决策重排。

1. **三层解析路由实现**（当前）：pdf-inspector Spike（Windows wheel + 中文语料）→ MinerU Provider 实现，按设计文档 §6 顺序
2. **里程碑 1.9 混合检索加固**：Agent 多跳检索（驾驶舱 D 列轮次可视化），带上 TDB-AM 对标 P0 四条（召回预算控制/两步自发现/BM25 降级/hop 级日志）
3. **知识时效最小闭环**（2026-08-20 批准）：chunk 入库时间 + 检索结果标注新旧 + 生成时提示"存在更新版本"，消灭新旧知识混答
4. **解析即验收（上传即可视化）**（2026-08-20 批准）：处理完成页内嵌单文档透视（切块地图+清洗 Diff），黑盒变白盒
5. **质量体检报告获客入口**（2026-08-20 批准，后置于 WPS 前）：上传 10 份文档出质量体检报告，驾驶舱能力产品化
6. **WPS 最小闭环**：选中段落查知识引用的最小插件，验证真实使用（walking skeleton 用在交付端）
7. **1.8 批量压测**（后置）：与其他测试统一做，暂不单列冲刺
8. **知识平台里程碑 2**（暂缓）：Java 控制面接入
9. **网盘增强 / 设备配对 / 传输加密**（暂缓）：桌面端功能完善

## 已知阻塞

- Windows CI（`build-windows.yml`）覆盖 master/PR/tag/手动触发，`dev` 推送刻意不触发以节省 runner 时长；日常 `dev` 开发仍依赖本地 `scripts/build.ps1` 全量验证与真机安装验收

## 版本约定

当前版本：**0.1.0**（开发基线，尚未正式发布）

版本号规则：
- 0.x.y：阶段 0-3 的迭代版本
- 1.0.0：云端 MVP 可用时
- 安装包版本与 wails.json `info.productVersion` 保持一致
