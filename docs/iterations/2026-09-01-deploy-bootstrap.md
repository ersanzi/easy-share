# EasyShare 部署提效 — 一键 bootstrap 向导

## 用户问题

- 公司部署（发芽路线关键路径）目前是 30 分钟的 6 步手工流程：venv、pip、手填 .env、手动启动、手敲 curl 开账号、注册自启；RustFS 装法在手册里缺失，防火墙放行 8000 无人处理（同事访问不了的第一坑），pip 走境外源在公司网络经常超时。
- 部署门槛直接决定"种下去"能不能发生：目标是从"IT 同学照手册逐步操作"变成"跑一个脚本、回答几个问题"。

## 目标

- 一条命令完成公司服务器部署：`powershell -ExecutionPolicy Bypass -File scripts\deploy.ps1`，交互补齐缺失信息；也支持全参数无人值守。
- 覆盖：Python 检查（缺失给 winget/链接指引）、RustFS 三分支（复用已有 / Docker compose 起新 / 明确报错）、venv + pip（国内镜像默认）、.env 生成（LLM/Embedding 可跳过降级纯检索）、首启 + /health 探活、管理员 bootstrap + 批量同事账号（随机初始口令打印）、防火墙放行 8000、可选开机自启、生成「同事使用一页纸」文本文件（含本机局域网 IP）。
- 在本机隔离目录真实跑通一遍（复用本机已有 RustFS）。

## 非目标

- 不把 knowledge 服务容器化（SMB 挂载/镜像构建/IT 熟悉度三个坑，且放弃复用现有脚本基建）；NSIS 安装包形态不做（服务仍在快速迭代）。
- 不做桌面端/RuoYi 控制面栈的部署（本切片只管知识服务这一发芽路线最小闭环）。
- 不自动安装 Docker/Python（只检测 + 引导；自动装系统级软件越权）。

## 设计决策

- **向导脚本而非全容器化**：复用既有 `start_server.ps1`/`install_autostart.ps1` 基建；WATCH_DIRS 直读本地/共享盘路径（容器挂 SMB 坑多）；pip 默认清华镜像（公司网络）。脚本参数化 + 交互兜底：`-NonInteractive` 全参数无人值守，缺什么问什么。
- **RustFS 三分支**：参数/交互指定已有实例（复用）；本机 Docker 用 `docker-compose.rustfs.yml` 起新（自动生成随机凭据、数据 bind mount 落 `knowledge\rustfs-data\` 对 IT 可见可备份、9000 API 只绑本机 9001 控制台局域网可达）；不配置则警示降级。桶初始化用 venv 内 boto3 head/create（幂等）。
- **端口占用归属校验**：启动前探测端口，被占时先验 `/health` 是否 EasyShare（有 `embedder` 字段）——非 EasyShare 明确报 PID/进程名退出，防止拿别人的服务继续部署后 401 崩溃（真实踩到：测试机 8100 被 java 占用）。
- **账号幂等**：bootstrap 409 时改用输入口令登录验证，通过即复用；同事账号逗号分隔批量建，随机 10 位口令收集后写入「同事使用指引.txt」并提示分发后删除。
- **防火墙**：管理员权限自动放行（规则幂等）；非管理员打印 netsh 命令由 IT 自行执行——不自动装系统级软件、不静默提权。
- **`-Port` 参数贯通**：`start_server.ps1`/`install_autostart.ps1` 同步加 `-Port`（默认 8000 不变，向后兼容），deploy 自定义端口时全链路一致。

## 兼容与迁移

- 纯新增脚本 + compose 文件 + 文档，不改运行代码；既有手工部署路径保留为"进阶"章节。
- deploy.ps1 幂等：已有 .venv/.env/账号时询问保留或重建（非交互模式默认保留 venv、.env 有参数输入则重写）。
- **requirements.txt 补 `httpx>=0.27`**：原清单缺失但 `app/parsing/mineru/client.py` 是主服务无条件 import 链——按旧手册部署的服务会直接起不来（本次隔离测试抓到的存量 bug）。
- 三个含中文 .ps1 均存 UTF-8 带 BOM（Windows PowerShell 5.1 GBK 坑，项目既有规矩）。

## 测试计划

- 本机隔离目录（%TEMP% 拷贝 knowledge 源码）参数式真实跑通：RustFS 复用本机实例、LLM/Embedding 留空（纯检索模式）、验证 health/账号创建/指引文件生成。
- 交互分支逻辑走查（无 RustFS/无 Python 分支不真装，验证输出文案）。

## 发布与回滚

- 仅新增文件，回滚 = 删除；不影响已部署环境。

## 完成记录

**已完成（2026-09-01，当日完成）**：

- `knowledge/scripts/deploy.ps1`：一键部署向导（Python 检查/RustFS 三分支/venv+清华镜像/桶初始化/.env 生成/启动探活/管理员+批量同事账号/防火墙/自启/同事使用指引.txt），交互+无人值守双模式
- `knowledge/docker-compose.rustfs.yml`：RustFS 单容器（bind mount 持久化、API 仅本机、控制台局域网）
- `knowledge/scripts/start_server.ps1`、`install_autostart.ps1`：加 `-Port` 参数（默认不变）
- `knowledge/requirements.txt`：补运行时依赖 `httpx>=0.27`（存量缺口）
- `docs/company-rollout-guide.md`：第一节重写为「一键部署（约 5 分钟）」，手工流程降为进阶；补防火墙放行步骤（原手册缺失）

**隔离测试结果（本机 %TEMP% 全新目录，参数式无人值守）**：

- 全流程通过：venv 创建 → 依赖清华镜像安装 → 桶校验 exists → .env 生成（AUTH 开、入库目录自动创建）→ 服务启动 /health ok → admin + xiaowang + xiaoli 三账号创建（随机口令入指引文件）→ 指引文件含局域网地址
- 端到端功能：往 watch-inbox 放文件 → watcher 30s 轮询自动入库（1 record/1 job completed）→ xiaowang 登录 /query 检索命中（score 0.599），未配 LLM 正确降级纯检索
- 测试中修复的脚本缺陷：Python 探测语法（$args 自动变量冲突）、pip 镜像参数需数组展开、端口占用归属校验、Get-LanIp 排除 vEthernet（WSL/Hyper-V）虚拟网卡
- 测试环境已清理（8110 服务停止、目录删除）

**已知边界**：

- 交互式分支（RustFS 菜单、LLM 逐项询问、自启确认）未真实走键盘流，逻辑走查通过；首次公司部署建议 IT 同学在场跑一遍交互模式。
- Docker 新起 RustFS 分支未真实执行（本机测试走复用分支）；compose 配置与在用容器逐项对齐（镜像/卷/端口/env）。
- WPS 插件安装仍是每台客户端电脑各自跑（不属服务器部署范围）。
