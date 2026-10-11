# EasyShare 自动化任务清单

> 本文件是「自主迭代自动化」的工作队列：收件箱（情报扫描候选，未拍板）→ 待办池（排好优先级的可执行任务）→ 进行中 → 已完成。
> 进度与路线的**唯一真相源仍是 [progress.md](progress.md)**，本清单是其下游执行队列，两者冲突时以 progress.md 为准。
> 由一条每日 14:00 的「自主迭代调度」自动化维护（规则见文末）：到期判定后执行「情报学习扫描」（每 ≥3 天一轮）或「自主规划与迭代」（每周二、周五）。
> 人类可随时手动改写本文件，手动改写优先于自动化结论。创建：2026-09-02（人工）

## 执行契约（自动化必读）

1. **红线（任何情况下不可违反）**：不 commit、不 push、不 stash/reset/checkout 用户分支、不 Stop-Process/taskkill 任何进程（easyshare.exe 可能正在被使用）、不跑 `scripts/build.ps1` 全量、不改注册表、不删文件、不 docker restart 用户容器。
2. **脏树保护**：动手前先 `git status`。若存在非自动化产生的未提交改动，本轮**只做规划与文档更新，不修改任何代码**，并在运行日志记录跳过原因。
3. **来源约束**：只实现待办池中标注「可自主」的任务；执行中冒出的全新产品想法只写入收件箱等用户拍板，不实现（用户规矩：新想法先报拍板）。
4. **小批量**：每轮 1–2 个任务；大任务只做一片，并在任务行记录切片进度。
5. **验证**：涉及代码必须轻量回归——`go build ./... && go test ./...`；改 Python 则 `knowledge/.venv/Scripts/python.exe -m pytest -m "not integration"`；改前端则 `cd frontend && npx vue-tsc --noEmit`。新切片默认不写单测（冲刺期轻测试约定），回归绿即可。
6. **流程**：新代码主题按迭代流程——progress.md「进行中」登记 + 建 `docs/iterations/当天日期-主题.md`；完成后更新 progress.md 迭代记录表与本清单；排障经验写入 troubleshooting.md。
7. **代码约定**：遵守 AGENTS.md——Go 新代码中文注释、错误向上 wrap 附上下文、前端改动同步 `types/core.ts` 与 `services/core.ts`（Wails 绑定级联）。

## 用户动作区（自动化不做，仅陈列提醒）

| 事项 | 说明 |
| --- | --- |
| 公司 Linux 服务器部署端到端验收 | 2026-09-02 资产已写完（deploy/server-linux + ship-control-plane.ps1），待真实服务器部署验收——**部署日执行序：[DEPLOY-CHECKLIST.md](../deploy/server-linux/DEPLOY-CHECKLIST.md)（2026-10-11 建）**； MinerU 冒烟、WPS 真机随部署一并做；~~部署时手动应用 DDL~~ **2026-09-06 已修：ship 脚本每次投递自动重放全部 7 个幂等 DDL（含 es_file/上传会话/部门授权），本地 Docker 演练已验** |
| 当前未提交改动的审阅与提交推送 | ✅ 已处理（2026-09-04 用户拍板）：剪切板修复以 14c531d 提交（提交前 go build/test 全绿复验），自动化文档随本批 docs 提交入库，双远程推送 |
| 真机鼠标验收欠账 | 登录→上传→换账号、空间盘挂载/卸载、悬浮窗交互、设置页账号卡片（见 progress.md 已知阻塞） |
| 插件仓拆分 | 挂触发条件（插件≥3 / 外部协作者 / 发布节奏倒挂 / 客户定制），**勿主动执行**，计划在 plans/2026-09-01-plugin-repo-split.md |
| 向量库切 Milvus | 挂触发条件（chunk 逼近 10 万 / P95>500ms / 多 worker），观察期默认 JSON+numpy |

## 收件箱（情报候选 / 新想法，未拍板——自动化与学习扫描只追加，不实现）

| 日期 | 条目 | 来源 | 建议优先级 |
| --- | --- | --- | --- |
| 2026-09-27 | **部署资产 RustFS 镜像升级**：`deploy/server-linux/compose.yaml:49` 钉 `rustfs/rustfs:1.0.0-beta.10`（2025 年 beta），上游 1.0.0 稳定版已发布（现 1.0.1-preview 系列）。建议部署验收时改钉 1.0.0 并重验存储三路（预签名直传/挂载盘/知识服务上传），消除 beta 期已知问题 | 2026-09-27 情报扫描 digest #4（09-06/09-13 观察点关闭并升级） | 已拍板（2026-10-11 用户：更新最新稳定）——compose 已改钉 **1.0.1**（GitHub release 核实当日 1.0.1 已 GA）；三路重验归部署日 checklist |
| 2026-09-06 | **知识服务 SSO**：知识服务信任控制面 JWT，消灭「控制面账号 vs 知识服务账号」双账号（分发冲刺余量：当前以账号预填+文档缓解） | 2026-09-06 分发冲刺迭代发现 | ✅ 已完成（2026-10-11 introspection 方案落地，见 iterations/2026-10-11-知识服务sso.md；客户端知识页免登留下一片） |
| 2026-09-06 | **企业部署能力缺口评估**（用户问"给员工用还缺什么"）五项入箱：① HTTPS 反代 ② 员工 Web 门户 ③ 企微/钉钉机器人通知 ④ 操作审计日志 ⑤ 全局快捷搜索——**2026-09-06 用户拍板：⑤ 全局快捷搜索、部门级权限（新提案）、管理员汇总页（新提案）三项转正待办池；①②③④ 用户评估暂不做（挂起，重评估条件：跨网段部署或安全合规要求出现时重启 HTTPS/审计讨论）** | 2026-09-06 用户部署咨询 | 已拍板（见待办池 P1 三行） |
| 2026-09-05 | 评测集扩充「上下文敏感性」难例：现 42 条对真实 embedding 饱和（双向 1.000），contextual 增益不可测量。需构造正文不含文档身份即失义的块（如无标题表格只写「上限每晚 300 元」）；设计不当会过拟合，需人工拍板语料方案 | 2026-09-05 Contextual Chunking 迭代发现 | P2（观察期后做） |

## 待办池（已排优先级，可自主=自动化可直接做）

| 优先级 | 任务 | 来源/依据 | 完成标准 | 可自主 |
| --- | --- | --- | --- | --- |
| P2 | 汇总页进阶可视化（查询趋势折线/按账号检索排行）：观察期看真实需要再加，数据都在 query_log | 管理员汇总页片收尾记录 | 有真实诉求时做 | ✅ |
| P2 | AI 周报接知识服务：todo 周报插件增加 AI 汇总（经 Core 网关调 /query，登录态复用；注意 2026-09-05 日志提示涉跨进程登录态） | progress.md 待开始 #5 | 插件冒烟 + 回归绿 | ✅ |
| P2 | 空间整理算法：plans/2026-08-30-space-pool-and-organize.md 中「整理算法」待实施部分——实现前先读该 plan | progress.md 账号控制面段 | 按 plan 验收标准 | ✅ |
| P3 | 文档对账巡检：progress.md / iterations / README 交叉链接与状态一致性检查，发现漂移即修文档 | AGENTS.md 文档规矩 | 巡检结果写运行日志 | ✅（适合被安全门拦截时的填充任务） |
| 后置 | 1.8 批量压测 | 2026-08-20 决策与其他测试统一做 | 与其他测试统一做 | ❌（后置） |
| 后置 | 体检报告获客入口 | progress.md 待开始 #2（种子发芽后作钩子） | 等发芽 | ❌（等观察期结论） |
| 需拍板 | 「问过的不再问第二遍」FAQ 沉淀 | product-positioning.md §三（待真实使用验证形态） | 用户拍板形态后再入待办池 | ❌（形态待定） |

## 进行中

（空）

## 已完成（自动化批次）

| 完成日 | 任务 | 验证结果 |
| --- | --- | --- |
| 2026-10-02 | P2 OKF 化切片一：derived/ 重建验证（`knowledge/scripts/rebuild_from_derived.py`——从 document.json+manifest.json 全量重建索引，contextual 参数从 manifest 恢复不重调 LLM，四指标断言不回退） | 重建验证一次通过（10 文档 29 块，recall@5/hit@1/mrr/snippet 与管线直建基线逐项持平 0.952/0.857/0.892/0.905）；pytest 147 passed 全绿。见 [iterations/2026-10-02-okf-rebuild-verify.md](../docs/iterations/2026-10-02-okf-rebuild-verify.md) |
| 2026-09-06 | P1 管理员汇总页：AdminPanel 概览 Tab 六卡片 + 知识服务 /stats 聚合端点 | 三栈回归全绿（Java 37/go/pytest 144/vitest 40）。见 [iterations/2026-09-06-admin-overview.md](../docs/iterations/2026-09-06-admin-overview.md) || 2026-09-06 | P1 部门级权限片 2（文档级可见性数据面）：visible_depts 字段 + 上传声明 + 设置端点 + 列表过滤收口 | Java 39 全绿 +2；go 全绿。/query 联动核验后拆片 2b。见 [iterations/2026-09-06-dept-permission.md](../docs/iterations/2026-09-06-dept-permission.md) §5 |
| 2026-09-06 | P2 部门级权限片 2b（/query 检索联动）：auth dept 列（登录下发）+ ingest metadata 透传 + doc_visible_depts 双后端 + visible_doc_ids 过滤 | pytest 147 全绿 +3（端到端部门过滤/auth 往返/旧库补列）。见 [iterations/2026-09-06-dept-permission.md](../docs/iterations/2026-09-06-dept-permission.md) §5 |

| 2026-09-06 | P1 部门级权限片 1（空间级）：授权主体泛化 + 生效权限取宽 + 管理页部门授权区 | Java 37 全绿 +5；go/vitest 40/vue-tsc 全绿。见 [iterations/2026-09-06-dept-permission.md](../docs/iterations/2026-09-06-dept-permission.md) |
| 2026-09-06 | P1 全局快捷搜索（用户点名）：Alt+Space 呼出第二面板，知识+文件聚合搜索，Enter 打开/复制 | go build/test 全绿；知识服务 144 绿（mode=search 加法）；真机冒烟随下轮打包。见 [iterations/2026-09-06-global-quick-search.md](../docs/iterations/2026-09-06-global-quick-search.md) |
| 2026-09-05 | P2 插件批次 3 余项：剪切板「开机自动记录」开关（边界确认后交付：缺口在 OS 自启链路的应用内可控，非录制恢复） | go build/test 全绿（+3 mock 用例，不写真注册表）；插件 JS 语法过；真机开关冒烟归真机验收欠账，存量安装需商城发布 2.1.0。见 [iterations/2026-09-05-clipboard-autostart-toggle.md](../docs/iterations/2026-09-05-clipboard-autostart-toggle.md) |
| 2026-09-05 | P2 观察期周报脚本：QueryLog.windowed_stats + 五节中文周报（使用率/命中/盲区/生成质量/观察提示） | pytest 144 全绿（+4 用例）；空库兜底/旧库窗口过滤/有数据渲染三路冒烟过。见 [iterations/2026-09-05-querylog-weekly-report.md](../docs/iterations/2026-09-05-querylog-weekly-report.md) |
| 2026-09-05 | P1 contextual chunking：切块注入文档级上下文（文档级定位摘要前缀，LLM→启发式回退链） | pytest 140 全绿（+7 新用例）；42 条评测对比留档：哈希口径噪声内持平（MRR −0.002=单个 trigram 碰撞，向量分解证明）、真实 embedding 口径双向饱和 1.000；LLM 真链路冒烟过。见 [iterations/2026-09-05-contextual-chunking.md](../docs/iterations/2026-09-05-contextual-chunking.md)。「有增益」在现评测集不可测（饱和），扩充难例已入收件箱 |
| 2026-09-05 | P3 补 progress.md 迭代记录表「2026-09-02 Linux 生产服务器部署资产」行 | 已补，与「进行中」段落及 51ebf0d 提交一致 |

## 运行日志

| 日期 | 轮次 | 模式 | 结果 |
| --- | --- | --- | --- |
| 2026-09-02 | 人工创建 | — | 建清单；登记定时自动化（见下一行） |
| 2026-09-02 | 调度登记 | — | 因平台每会话限一条定时任务，合为单条每日 14:00 调度轮：周二五自主迭代 / 每 ≥3 天情报扫描，判定规则见「自动化运行规则」 |
| 2026-09-03 | 情报学习扫描 | B | 自动化首轮：四轴完成（FastAPI/RustFS/RuoYi/PaddleOCR 未覆盖下轮补）；收件箱 0 条新增——P1 contextual chunking 获 Anthropic Contextual Retrieval 主流化佐证，P0 回灌刚落地；P2/P3 观察项 12 条入 digest（要点：Wails 上游 v2.14.0 vs 项目 v2.13.0、Milvus 3.0.0、Cloudreve V4 发布、reranker 候选池 20→5 经验参数）；产出 [docs/intelligence/2026-09-03-digest.md](intelligence/2026-09-03-digest.md)。工作树用户 WIP 未受影响 |
| 2026-09-04 | 自主规划与迭代 | A（安全门拦截） | 工作树有用户 WIP（剪切板修复未提交），按契约只做规划与文档：执行 P3 文档对账巡检——五真相源 0 断链；发现 2 处漂移并处置（progress.md 迭代表缺 Linux 部署行→因在其 WIP 中不代改，登记待办池 P3；本清单用户动作区过时表述→已勘误）；待办池重排结论=维持现状。详见 [docs/iterations/2026-09-04-文档对账巡检.md](iterations/2026-09-04-文档对账巡检.md) |
| 2026-09-05 | 自主规划与迭代（会话触发，用户指示找 GitHub/Gitee 灵感） | A | 树干净安全门过。执行 2 任务：P1 contextual chunking（完成，收获三实测修正：样板词噪声/装箱漂移/启发式注入范围；评测发现「42 条集对真实 embedding 饱和」并量化哈希碰撞噪声，难例扩充入收件箱）+ P3 补 progress.md 迭代表 Linux 行。待办池重排=P1 消化后维持 P2 顺序。灵感扫描：Anthropic 官方 cookbook/工程博文范式确认（逐块 LLM 上下文→本项目收敛为每文档一次），无新增产品条目 |
| 2026-09-05 | 会话续轮（用户拍板：提交推送后继续） | A | P1+P3 以 4861d0d 提交并双远程推送。续执行 P2 观察期周报脚本（完成：windowed_stats 严格窗口聚合 + 五节周报，pytest 144 绿，三路冒烟过）；顺手修盲区标签误导（向量检索零分也计 top_k 条，改按分数表达） |
| 2026-09-05 | 会话续轮 2（用户确认继续） | A | 周报以 6f815a6 双远程推送（Gitee 一次 TLS 握手失败重试即过）。续执行 P2 开机自启记录：边界确认（缺口=OS 自启链路应用内不可控，录制随应用恢复 08-31 已有）→ HKCU Run 键开关 + 能力扩展 + 插件 UI 2.1.0（完成，go 全绿）。剩余可自主任务=AI 周报接知识服务（涉跨进程登录态，建议下轮） |
| 2026-09-06 | 用户直配轮（网盘优化，Cloudreve 对标参考） | A | 推送核对（双远程 8c26a49 无欠）。取对标 P0 的客户端最小片：目录导航/搜索/排序/上传落当前目录（视图推导，权威目录层留控制面）；targetDir 完整走 Wails 级联。vue-tsc 干净 + vitest 40 全绿 + go 绿；真机点验随下轮打包。网盘后续顺序已写进 progress.md 待开始 #8（控制面 es_file+fileId → Multipart Session，跨 Java 仓） |
| 2026-09-06 | 用户直配轮 2（定调 Java-first：后续逻辑优先控制面） | A | 网盘 P0 正主落地：es_file 目录层 + fileId（presignPut 登记/列表补账/删除双轨，模块单测 24 全绿；客户端透传向后兼容）。踩坑三条入迭代文档（maven.test.skip 默认 true/JAVA_HOME 指 21/Mockito 桩贴真实调用序）。DDL+jar 上服务器归 Linux 部署验收；下一步=Upload Session Multipart |
| 2026-09-06 | 用户直配轮 3（Upload Session 开工指令） | A | Multipart 断点续传全栈落地：控制面 es_upload_session 记账 + 四端点（幂等 Complete/防孤儿分片/配额同口径/归属强校验），Go SessionStore 指纹持久化 + 续传 + 分片重试，UploadFile 32MB 自动分流零改动。Java 32 全绿 +8；go 全绿 +5。网盘 P0 三片合龙收官；DDL 上服务器归部署验收 |
| 2026-09-06 | 用户直配轮 4（部署缺口评估拍板） | A | 三项转正（部门级权限/管理员汇总页/全局快捷搜索，均 P1 入待办池），四项挂起（HTTPS/Web 门户/通知/审计，重启条件已记）。顺序：快捷搜索（独立可先交付）→ 部门级权限（需设计先行，拆空间级/文档级两片）→ 管理员汇总页 |
| 2026-09-06 | 用户直配轮 5（快捷搜索开工） | A | 全局快捷搜索全栈落地：面板多表面化（kind/spec/独立守卫/host.* 信封/异步 searchEmit 防卡窗）+ go:embed 搜索页 + /query mode=search 仅检索链路。go 全绿 + 知识服务 144 绿。三 P1 剩两项：部门级权限（需设计先行）→ 管理员汇总页 |
| 2026-09-06 | 用户直配轮 6（部门权限片 1） | A | 设计定稿 plans/2026-09-06-dept-permission.md（两片拆分+生效规则+不做清单）→ 片 1 全栈落地（member_type 泛化/生效取宽/管理页部门授权区，Go 双格式兼容部署窗口期）。Java 37 全绿 +5。剩：片 2 文档级可见性、管理员汇总页 |
| 2026-09-06 | 用户直配轮 7（管理员汇总页） | A | AdminPanel 概览 Tab 全栈落地（知识服务 /stats + Core 代理 + 六卡片）。踩坑：Wails 生成器不支持匿名嵌套 struct（KnowledgeStats 生成质量改命名类型）。用户拍板三项全部交付完毕 |
| 2026-09-06 | 用户直配轮 8（片 2 文档级可见性） | A | 数据面全量落地：es_file.visible_depts + 上传声明 + 设置端点（上传者校验）+ /objects 过滤收口（网盘/挂载盘/快搜自动生效）。/query 联动核验：/query 走知识服务本地 auth 无 deptId（原假设不成立）→ 拆片 2b（auth 部门模型 + ingest metadata 链），设计结论入迭代文档 §5。Java 39 全绿 +2 |
| 2026-09-06 | 用户直配轮 9（片 2b 检索联动） | A | 三步全栈落地：auth dept 列（存量库自动补列/登录会话下发）→ /documents/process 透传 visible_depts（job CSV 列/metadata/manifest）→ doc_visible_depts 双后端 + visible_doc_ids 过滤。pytest 147 全绿 +3（端到端：同部门可见/异部门与无部门不可见/admin 全见）。「这文档只有研发能看」问答链路端到端生效 |
| 2026-09-06 | 用户直配轮 10（分发冲刺：部署发安装包，员工视角痛点） | A | 五痛点全处理：租户服务发现（/easyshare/service/config，sys_config 零 DDL，知识页免手填+账号预填+管理页登记，推导回退 :8000）+ 登录页去 admin 预填/错误带地址 + NSIS 防火墙按程序放行（修互搜不到设备）+ v0.2.0 三处对齐 + build.ps1 全量跑通烧录实证 + 同事一页纸 company-desktop-guide + 分发清单六项。go 全绿 +6/vitest 40/Java 39（重踩 maven.test.skip 坑）；防火墙 UAC 真机点验与公司版构建待真实 IP |
| 2026-09-06 | 用户直配轮 11（续轮：真机冒烟 + Docker 演练） | A | 真机静默安装 0.1.1→0.2.0 升级路径 + computer-use 首启点验三验过（空账号框/EasyShare 品牌/报错带地址指引）；**修 ship 脚本 DDL 缺口**（9-06 四个新 DDL 未入清单=部署日缺表地雷，7 个幂等 DDL 改为每次投递重放）；**控制面容器栈本地 Docker 隔离演练全绿**（11 SQL/表断言/探活 200/服务发现 API 6/6 含 403 反例/sys_config 行 DB 级核验），顺手修 ftp:// 500 文案（Go 前置校验）。新坑：Git Bash 调安装包 /S 须 MSYS_NO_PATHCONV=1（已入 AGENTS 坑表）；pytest 147 绿 |
| 2026-09-06 | 情报学习扫描 | B | 第 2 轮（间隔 3 天到期）：补齐上轮轴③缺口——FastAPI 0.141.1 / PaddleOCR v3.7.0 / RuoYi-Vue-Plus v6.0.0 均在项目版本线无紧迫动作，Wails v3 仍 beta.16 未 GA；关键发现=**RustFS 仍 Beta 未到 production**，建议列入部署验收观察点；轴①沉淀难例扩充方法学（hard negatives / Ragas 合成测试集）直接服务收件箱 P2；收件箱 0 条新增（连续 2 轮）；轴②无新变化（连续 2 轮，下轮起降频试验）；Cloudreve V4 细读因论坛动态渲染未成，下轮改走 GitHub releases。队列维护发现：待办池「汇总页进阶可视化」存在重复行，留给下轮 A 重排处理（B 轮不改待办池）。产出 [docs/intelligence/2026-09-06-digest.md](intelligence/2026-09-06-digest.md) |
| 2026-09-13 | 情报学习扫描 | B | 第 3 轮（距上轮 7 天，本周调度未触发今日补跑）：收件箱 0 条新增（连续 3 轮）；轴① agentic retrieval / context engineering——行业转向确认，项目 1.9 多跳 + hybrid 已在主干，观察期不动；轴②降频窄查格局未变（连续 3 轮重复 → 下轮起降为隔轮一扫）；轴③ Wails v3 beta.20（09-10）仍未 GA、稳定线仍 v2.14.0，其余上游沿用上轮结论未逐一复查（已标注，下轮优先复查 RustFS production 动态）；轴④ Cloudreve V4 细读完成（4.15→4.18）：并行分块上传修复与项目 Multipart 互证（建议真机验收加分片边界用例）+ 安全修复节奏高频（部署期预期）。产出 [docs/intelligence/2026-09-13-digest.md](intelligence/2026-09-13-digest.md)。备注：自动化文档（09-06/09-13 digest 与日志）持续滞留工作树未提交 |
| 2026-09-27 | 情报学习扫描 | B | 第 4 轮（距上轮 14 天，09-14~26 调度未触发——机器/ZCode 未运行，错过不补跑本次补偿；HEAD 两周无新提交）：**收件箱 +1（连续 4 轮后首条）**：部署资产 RustFS 镜像仍钉 1.0.0-beta.10 而上游 1.0.0 稳定版已发布，建议部署验收时升级重验（P1）；Milvus 3.0.2/2.6.24 双稳定线活跃、Wails v3 仍 beta.26 未 GA；轴① late chunking vs contextual retrieval 共识确认项目 contextual chunking（LLM→启发式回退）在推荐象限；Cloudreve 4.19 再次收紧并行分块上传（强化 Multipart 验收用例建议）；轴②按隔轮计划跳过。产出 [docs/intelligence/2026-09-27-digest.md](intelligence/2026-09-27-digest.md)。备注：自动化文档滞留已 3 轮待用户提交 |
| 2026-09-30 | 情报学习扫描 | B | 第 5 轮（间隔 3 天到期；**周二 09-29 A 轮未触发**，待办池去重与任务执行连续第二周顺延）：收件箱 0 条新增，既有 P1 RustFS 镜像升级复核有效（1.0.1 仍 preview.11）；轴②隔轮窄查首次抓到进榜事件——Hindsight 新晋（LongMemEval 94.6%，单 PG 四路检索），降频机制价值验证，维持该节奏；轴①元结论=查询日志挖盲区无成熟公开方法，项目周报脚本属自建小资产，观察期后可按低分聚类增强（想法存 digest 待拍板）；Cloudreve 无增量。产出 [docs/intelligence/2026-09-30-digest.md](intelligence/2026-09-30-digest.md)。备注：自动化文档滞留已 4 轮待用户提交 |
| 2026-10-02 | 自主规划与迭代 | A | 周五 A 轮（连续两个周二未触发后首次跑成），安全门通过（树内仅自动化文档）。先清挂账：待办池「汇总页进阶可视化」重复行去重。执行 P2 OKF 化切片一（derived/ 重建验证）：新增 `knowledge/scripts/rebuild_from_derived.py`——口径定稿为黄金语料双路对比（管线直建 vs 只读 derived 重建，contextual 参数从 manifest 恢复不重调 LLM），重建验证一次通过（四指标逐项持平 0.952/0.857/0.892/0.905），pytest 147 全绿。待办池重排：汇总页可视化按自身标准继续等观察期；剩余可自主=AI 周报接知识服务、空间整理算法。详见 [docs/iterations/2026-10-02-okf-rebuild-verify.md](iterations/2026-10-02-okf-rebuild-verify.md) |
| 2026-10-11 | 用户直配轮 12（欠账清理 + 部署窗口准备，五问拍板） | A | ① 欠账清零：10-02 OKF 切片与四份 digest 提交双远程推送（35 天滞留，自动化安全门解锁），推送前 pytest 147 复验绿；② RustFS 拍板升最新稳定——GitHub release 核实 1.0.1 已 GA，compose 改钉 1.0.1（本机 Docker 未运行 + registry 不可达，digest 缺席改纯 tag 钉，部署日可补）；③ 新增 deploy/server-linux/DEPLOY-CHECKLIST.md 部署日执行序。真机清欠批次暂缓、安全项维持挂起（用户拍板） |
| 2026-10-11 | 用户直配轮 13（OKF 切片二） | A | 用户授权「需要做就做」：rebuild --apply 生产索引重建落地——重建核心上移 app/kb/rebuild.py（切片一脚本复用，四指标逐位一致 0.952/0.857/0.892/0.905）、JobStore.latest_completed_docs 权威清单（可恢复索引丢文档灾难）、预览/执行双段式（预览零写入）、失败整体中止+备份+原子替换；embedder 走线上同一工厂防维度错配。pytest 150 全绿 +3（破坏性写路径例外用例）；--apply 预览真机冒烟过（4 文档/OpenAIEmbedder 识别正确）。同轮用户拍板：WSL 部署彩排不做（不值得）、Wails v2.14 升级做（推翻助手后置建议）、SSO 待解释后定。**Wails v2.13.0→v2.14.0 升级完成**（上游仅修 WebView2 引导器下载空指针/吞错，低风险）：CLI 双位置坑入 AGENTS 坑表（GOPATH=D:\GoWorks 装 D:\GoWorks\bin，PATH 命中 C:\Users\lishi\go\bin 旧版，覆盖解决）；go.mod v2.14.0+传递依赖升级；绑定重生成零变化；go build/test 全绿、vue-tsc 干净、vitest 40 绿、wails build 过；真机冒烟双进程存活+WebView2 创建+健康端点应答（冒烟进程已清） |
| 2026-10-11 | 用户直配轮 14（SSO 落地） | A | 用户解释 SSO 后拍板「做」：知识服务信任控制面令牌——app/auth/sso.py introspection 回查 /system/user/getInfo（头与解析同 Go 客户端一套）+60s 缓存 + JIT 建档（role 按控制面 superadmin/*:*:*/dept 取 deptId 跟随，本地 role 不覆盖）+ 本地登录保留；config 加 CONTROL_PLANE_URL/CLIENT_ID，deploy.sh/deploy.ps1/.env.example 默认写同主机 :8090，checklist §2 改单账号口径。pytest 156 全绿 +6（含真实 HTTP 桩验证头与包装解析）；真实进程冒烟：控制面令牌 200+JIT wang/admin/103、匿名 401。收件箱 P1「知识服务 SSO」就此关闭；客户端知识页免登（Core 透传令牌）留下一片 |

## 自动化运行规则

> 平台限制：一个会话只能登记一条定时任务，故原设计的两条自动化合并为单条调度轮，到期判定不变。

- **每日 14:00 · 自主迭代调度**（到期判定：星期用 `date +%u`，上次扫描日期取 `docs/intelligence/` 最新 digest 文件名）：
  - 周二/周五 → **自主规划与迭代**：安全门（脏树拦截则本轮只做规划与文档）→ 重排待办池 → 执行 1–2 个「可自主」任务 → 轻量回归 → 更新本清单与 progress.md。
  - 距上次情报扫描 ≥3 天（digest 不存在也算到期）→ **情报学习扫描**：扫 RAG 检索质量 / Agent 记忆框架 / 上游版本（Wails、FastAPI、Milvus、RustFS、RuoYi、MinerU/PaddleOCR）/ 可借鉴产品四轴，产出 `docs/intelligence/当天日期-digest.md`，P0/P1 可落地条目追加本清单收件箱。不改代码。
  - 都不到期 → 空转结束，不改动文件、不写日志。
- 重评估条件：连续两轮被安全门拦截 → 迭代轮降为每周一次或纯提案模式；情报 digest 连续 3 轮重复度高 → 学习扫描降为每 5 天；观察期数据回流后重点变化 → 重排待办池。
