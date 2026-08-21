# 桌面端集成知识问答（发芽路线第 3 步）

## 用户问题
- 发芽路线前两步已落地（2026-08-20）：公司服务器上知识服务可登录、监听目录自动入库。但同事查知识必须开浏览器、输服务器地址进 `/lab` 实验台——入口远，且登录条是"实验台级体验"（种下去冲刺已知限制中明确：正式多端入口随桌面端集成迭代）。
- 知识要用起来，入口必须长进同事每天开着的 EasyShare 桌面端：登录一次、随时提问、答案带引用。本迭代打通 Wails 桌面端 ↔ 知识服务。

## 目标
- 桌面端新增「知识」页：未配置/未登录时呈现登录表单（服务器地址 + 账号 + 密码），登录后进入问答界面
- 问答最小闭环：提问 → 答案 + 引用列表（文件名/相似度/入库时间/片段），401 引导重新登录，服务不可达给出可理解错误
- 知识令牌全程不进前端：由 Core 持有并持久化，桌面重启后登录态保持
- Core 暴露版本化知识代理 API，后续 WPS 插件/Shell 扩展可复用同一通道

## 非目标
- 不做对话持久化（会话仅组件内存，重启清空）
- 不做文档上传/监听目录管理/账号管理（建账号仍走 curl/bootstrap 或 /lab）
- 不做流式回答（/query 为同步 JSON，首切片沿用）
- 不改 knowledge 服务端任何代码（API 已齐备：/auth/login、/auth/me、/query、/health）

## 设计决策
- **Go Core 作知识网关（用户拍板，2026-08-21）**：前端直连方案的替代被否决——破"UI 只依赖 Core"边界先例、令牌落前端、需给服务端加 CORS、后续 WPS/Shell 集成仍要重写 Go 客户端。链路：前端 → Wails 绑定 → App 方法 → desktop.Client → Core `/api/knowledge/*` → 知识服务器 FastAPI。
- **会话存 `knowledge.json` 而非 config.json**：桌面进程 SaveSettings 用启动时的旧 Config 整份回写 config.json，若令牌存在其中会被冲掉（竞态）。知识会话（serverUrl/token/username/role/expiresAt）单独存与 config.json 同目录的 `knowledge.json`，仅 Core 读写，桌面进程不感知。config.json 结构零改动。
- **Core 新增 5 个代理端点**：`GET /api/knowledge/status`（本地态，无网络探测，供 UI 秒开）、`POST /api/knowledge/login`（携服务器地址+凭据，Core 调远端 /auth/login 成功后落盘会话）、`POST /api/knowledge/logout`、`POST /api/knowledge/health`（探测远端 /health，面板显示知识库规模与 LLM 状态）、`POST /api/knowledge/query`（Bearer 代理远端 /query）。
- **超时解除**：Core HTTP 服务全局 WriteTimeout 30s，多跳检索 LLM 链路可能超时——query 代理 handler 用 `http.NewResponseController` 解除读写期限（同 cloudUpload 流式上传先例）；知识客户端单独 http.Client，问答请求无固定超时（跟随请求上下文）。
- **服务器地址在登录页输入**（不违反开箱即用原则：该原则针对 EasyShare 自有云的编译期常量；知识服务器是各公司自部署实例，地址必须可配，同 WPS 企业版登录形态）。地址带协议校验（http/https），缺省补 http://。
- **错误映射**：远端 401 → Core 401（前端引导重新登录）；网络不可达/超时 → 502 附可读信息；FastAPI detail 字段透传。

## 兼容与迁移
- config.json 无新增字段；`knowledge.json` 不存在视为未配置（首启动即此态），登录后自动创建
- 旧 Core + 新 UI 不共存场景不涉及（单仓单版本）；Core API 其余端点行为不变
- 不涉及端口、盘符、进程模型变化；令牌文件与 config.json 同目录（%LOCALAPPDATA%\EasyShare）

## 测试计划
- Go 单元测试：knowledge 客户端（httptest 模拟登录/问答/401/网络错误映射）、会话存取（落盘/加载/清空）、API handler（未配置 503→登录成功→query 代理→logout 清态）
- 前端：vue-tsc 类型检查 + vitest 既有套件不回归
- 全量：`go test ./...` + `npm --prefix frontend run build` + Python 侧 `pytest -m "not integration"` 不受影响抽查
- 手工验收：本机起知识服务（AUTH_ENABLED=true），桌面端登录→提问→引用展示→退出登录→重启桌面端登录态保持（延后到与公司部署冒烟合并）

## 发布与回滚
- 构建产物：`wails build` 双 exe（桌面端 + Core）；本迭代未动 NSIS 脚本
- 升级步骤：覆盖安装即用；首次进入「知识」页登录（输入公司服务器地址与账号）
- 回滚方式：回退构建即可；`knowledge.json` 为独立新文件，回滚版本忽略它即可，无残留影响
- 日志信号：core.log 中 `knowledge login ok` / `knowledge query: ...` / `knowledge health: ...`；不记录令牌

## 完成记录

### 已完成（2026-08-21）

- `internal/knowledge`：HTTP 客户端（登录/问答/健康探测，远端错误映射 `RemoteError`）+ `knowledge.json` 会话存储（原子写 0600，仅 Core 读写）+ Service 运行态（SignIn/SignOut/Status）
- Core API 5 个端点（`internal/api/knowledge.go` + server.go 注册）：status（本地态秒回）/ login（15s 超时，成功落盘）/ logout / health（5s 探测）/ query（解除全局 30s 写期限，120s 上下文兜底；远端 401 自动清会话并返回 `knowledge_auth_expired`）
- 链路级联：`cmd/core/main.go` 装配 → `desktop.Client` 5 个代理方法（问答走无客户端级超时 slowRequest）→ `app.go` 5 个 Wails 导出方法 → `wails generate module` 重生成绑定 → 前端 types/services 级联
- 前端「知识」页：`KnowledgePanel.vue`（登录表单含服务器地址回填 / 会话式问答 / 引用折叠列表（文件名·相似度·入库时间·片段）/ 令牌失效自动回登录页）+ App.vue 导航与视图 + style.css 样式段
- 测试：`internal/knowledge` 9 条（地址规范化/登录/问答/错误映射/健康/存储往返/服务生命周期）、`internal/api` 知识网关 3 条（全流程/参数校验/令牌失效清会话）；全量 `go test ./...` 通过、前端 vue-tsc+build+vitest 19 条通过、Python 120 passed（服务端零改动基线不变）
- 顺手修复既有环境耦合测试：`internal/cloud/preview_test.go` 对 `.md` ContentType 硬编码 `text/plain`，在系统 mime 表映射为 `text/markdown` 的 Windows 上必挂；改为断言 `text/` 前缀（与本迭代无关，见日志）
- `architecture.md` 同步：进程图加知识服务外连、API 清单 +5 端点、持久化一节补 knowledge.json、新增 §8a 知识网关

### 已知问题与后续工作

- 手工验收（真实知识服务登录→提问→引用→重启保活）留待与公司部署冒烟合并执行
- 会话消息不持久化（非目标，重启清空）；后续若要历史可下沉 Core
- 问答为同步 JSON，长问题等待期只有加载动画；流式输出待后续切片
- 登录页服务器地址对普通同事仍偏"技术"——若公司统一部署，可考虑安装包预置默认地址（留待种子反馈）

### 测试结果

2026-08-21：`go test ./...` 全绿；`npm --prefix frontend run build`（含 vue-tsc）与 `vitest run` 19 passed 通过；`wails build` 产出成功；Python `pytest -m "not integration"` 120 passed, 1 skipped（基线不变）。
