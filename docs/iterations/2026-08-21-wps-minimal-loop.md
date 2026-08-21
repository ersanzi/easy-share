# WPS 最小闭环（发芽路线第 4 步）

## 用户问题
- 发芽路线前三步已就位：公司服务器可登录、监听目录自动入库、桌面端可问答。但天天写文档的人活在 WPS 里——切出去查知识这个动作本身就是摩擦。
- 差异化第三层"WPS 里知识找人"需要一个现场演示的"哇"时刻：在 WPS 文字文档里选中一段话，一键查出公司知识库中的相关依据与引用。

## 目标
- WPS 加载项最小版：任务窗格内一键取"当前选中文字"→ 调知识服务 /query → 展示答案 + 引用列表（文件名/相似度/片段）
- 登录：加载项内账号密码登录，令牌存加载项本地（演示级）
- 本机可安装可卸载（演示机能装上就行），附一页安装说明

## 非目标
- 不做表格/演示组件适配（仅 WPS 文字）
- 不做全文分析、主动推送、侧栏常驻策略（后续迭代）
- 不做企业分发/签名/上架（演示与公司内手动安装）
- 按冲刺期豁免不写新单测：门槛 = 手工冒烟 + 既有回归不破坏

## 设计决策
- **加载项由知识服务自己托管**（`app/wps_addon/` 三件套经 `/wps/` 路由伺服）：任务窗格页面与 `/auth`、`/query` 同源，**CORS 问题整个消失**，同事安装只需在 jsaddons 登记一个指向公司服务器的小条目。放弃的两条路：前端直连（要给服务端加 CORS 且令牌散落）与经 Go Core 网关（Core 的 APIToken 无法安全递给 WPS 页面，且演示机不保证 EasyShare 常驻）。与"Core 是桌面端唯一通道"不冲突——WPS 是独立生态客户端，与 MCP 同属"API 生态"卖点；Java 控制面接管后改指新服务端即可。
- **安装方式：手工登记 jsplugins.xml**（`knowledge/scripts/install_wps_addon.ps1` 合并式写入 `%APPDATA%\kingsoft\wps\jsaddons`，不覆盖他人登记；`-Remove` 卸载）。官方 publish 模式需 wpsjs CLI + 托管 publish.html，对冲刺演示过重；新版个人版若限制手工登记，则回退 publish 模式部署同一份页面（脚本尾注已写明排查路径：Alt+F12）。
- **取选区**：任务窗格内优先 `window.Application`，回退 `wps.WpsApplication()`，读 `Selection.Text`（\r→\n、压缩空白、截 800 字）；取不到时允许手工输入问题兜底——任务窗格对文档 API 的可见性存在版本差异，兜底保证任何环境可用。
- **全 ES5 + XMLHttpRequest**：防老版 WPS 内嵌 IE 内核（无 fetch/Promise/箭头函数）。
- **"哇"时刻动线**：选中段落 → 点功能区「查知识」→ 窗格打开即自动取选区并自动提问（已登录时）；未登录首次需登录一次，令牌存窗格 localStorage（演示级；后续可迁服务端会话）。

## 兼容与迁移
- 知识服务端仅新增只读静态路由（/wps 三件套）与根信息一个字段，不碰认证与业务逻辑；Go 桌面端零改动
- jsplugins 登记仅本机；卸载 `-Remove` 清理，服务端无需变更
- 后续 Java 控制面接管登录后，加载项改指新服务端 URL（脚本重跑一遍）即可

## 测试计划
- 手工冒烟：WPS 文字中选中段落 → 查询 → 引用展示；未登录/服务不可达错误提示
- 既有 Python 回归不破坏（若动服务端 CORS）

## 发布与回滚
- 交付物：加载项目录 + 安装/卸载说明
- 回滚：删除加载项注册与目录即可

## 完成记录

### 已完成（2026-08-21）

- `knowledge/app/wps_addon/`：ribbon.xml（「知识」页签 + 查知识按钮）、index.html（加载项回调上下文，CreateTaskPane 唤起右侧窗格）、taskpane.html（登录 / 取选区 / 问答 / 引用折叠列表，ES5 + XHR）
- 服务端：`app/wps_addon/routes.py` 三个静态路由挂入 main.py；根信息透出 `wps_addon`；认证中间件白名单不受影响（/wps 不在保护前缀）
- 安装/卸载：`knowledge/scripts/install_wps_addon.ps1`（jsplugins.xml 合并式登记，支持换地址与 -Remove）
- 文档：architecture.md §11 补 WPS 加载项条目；company-rollout-guide.md 增「一·6 开通 WPS」与同事指引第 2 条
- 验证：TestClient 冒烟三件套路由 + 认证中间件不拦截 + 根信息字段（临时脚本，验证后删除）；全量 `pytest -m "not integration"` **120 passed, 1 skipped**（基线不变）；按冲刺期豁免未新增单测
- 真机首跑抓到并修复：install_wps_addon.ps1 无 BOM 时 Windows PowerShell 5.1 按 GBK 解析中文导致语法错误——已补 UTF-8 BOM 并记入 AGENTS.md 排障表；本机服务实跑验证 /wps 三件套 200、/query 真实问答链路通（免登录模式）

### 已知问题与后续工作

- ~~WPS 真机排障~~ **已解决并真机验收通过（2026-08-21 深夜）**：三层原因依次排除——①jsplugins.xml 手工登记被 12.1.0.28043 个人版忽略，改写 publish.xml；②publish.xml 里 `<jsplugin>` 是**离线模式**标签（url 应指 .7z 包），在线模式必须用 **`<jspluginonline>`**（enable="true"），WPS 曾按离线解析去找 `jsaddons\EasyShareKnowledge_` 空目录并把地址记入 `jsaddinblockhost.ini` 域名拦截——修正标签 + 清理 authaddin.json 旧记录与拦截名单（.bak 备份）后彻底重启（taskkill /IM wps.exe /F，WPS 常驻进程不退不会重读登记）即成功；③服务日志验证完整链路：ribbon.xml → index.html → taskpane.html → POST /query 200，任务窗格取选区自动提问动线真机跑通
- 令牌存窗格 localStorage（演示级）；企业级改造时随 Java 控制面统一会话
- 仅 WPS 文字（type="wps"）；表格/演示适配后续按需

### 测试结果

2026-08-21：服务端冒烟通过 + Python 全量回归 120 passed；**WPS 真机验收通过**（12.1.0.28043 个人版，在线模式 jspluginonline 登记，选中→查知识→答案+引用全链路）。
