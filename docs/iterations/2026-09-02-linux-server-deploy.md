# 2026-09-02 Linux 生产服务器部署资产

## 背景与目标

公司部署观察期启动：明天上 Linux 服务器，全套部件（知识服务 + RustFS + RuoYi 控制面 + PG/Redis），同事用 Windows 客户端访问；且项目处于快速迭代期，需要"推完代码 30 秒内更新到服务器"的管道。

原有部署资产全是 Windows 向（`deploy.ps1` 向导、计划任务自启、NSSM 建议），本次补齐 Linux 侧。

## 方案决策（含权衡）

| 决策 | 选择 | 理由与代价 |
| --- | --- | --- |
| 控制面运行形态 | **Docker 容器**（eclipse-temurin:21-jre，jar 挂载） | 服务器免装 JDK21，重启语义与 compose 统一；代价是 jar 必须从开发机传入（`platform/` 源码树 gitignore，服务器建不了——这是设计：RuoYi 工程不进本仓） |
| 知识服务运行形态 | **宿主机 venv + systemd** | git pull + pip 的快速迭代节奏在宿主机最顺；不容器化（挂 watch 目录与数据目录反而绕） |
| 更新管道 | knowledge=服务器 `update.sh`（fetch+reset origin/dev）；控制面=开发机 `ship-control-plane.ps1`；客户端=既有 `publish-release.ps1`（在线升级） | 三通道对应三种部件形态；服务器跟 dev 分支（快速迭代主战场），跟稳定版改 update.sh 一行 |
| 客户端指向服务器 | **构建期注入**：`build.ps1 -PlatformUrl` → ldflags 覆盖 `internal/config.defaultPlatformBaseURL`（const 改 var） | 同事开箱即用（AGENTS.md 产品原则）；替代方案"每台手改 config.json"被否——`Load` 对残缺 JSON 不容忍且违背开箱即用 |
| captcha/api-decrypt | 关闭（compose 启动参数） | 桌面客户端 `Login` 不走验证码与接口加密，开着登不进；内网可接受，公网前必须重评估（见 README §五） |
| Milvus | 不部署 | 见 rollout guide §四「向量库演进」（2026-09-01 定案），本次未动 |
| CI/CD 自动化 | 不做 | 单人观察期，手动一条命令 30 秒内完成；重评估条件写入会话结论（多人协作 / 每日发版 >2-3 次） |

## 改动清单

**新增 `deploy/server-linux/`**：
- `compose.yaml`：PG(127.0.0.1:5433) + Redis(127.0.0.1:6380) + RustFS(**0.0.0.0:9000**——预签名直传必须对 LAN 开放，dev 版绑 127.0.0.1 的坑在此修正) + ruoyi 容器（启动参数平移 `run-ruoyi-admin.ps1`，数据源/Redis 指向 compose 服务名）
- `deploy.sh`：一键引导（幂等）：容器起停/健康等待、rustfs.env 随机凭据生成、知识服务 venv + .env、桶初始化、systemd 注册、账号、防火墙（ufw/firewalld）、使用指引生成
- `update.sh`：知识服务快速更新——fetch+reset origin/dev → pip 同步 → 重启探活 → **失败自动回退上一提交**（只回代码不动数据）
- `systemd/easyshare-knowledge.service`、`env.example`、`README.md`（runbook：首次部署 15 分钟、更新三通道、运维速查、坑清单）

**新增 `scripts/ship-control-plane.ps1`**（开发机）：mvnw 构建 → scp jar + 建表 SQL → 远程灌表（检测 sys_user 幂等）→ 换包（保留 .bak）→ `docker compose up -d --no-deps ruoyi` → 探活 8090。

**修改**：
- `internal/config/config.go`：`defaultPlatformBaseURL`/`defaultAdminConsoleURL` const → var，支持 ldflags 注入
- `scripts/build.ps1`：新增 `-PlatformUrl` 参数，注入到 core 与 wails 两处构建
- `docs/architecture.md` §1：补生产 Linux 部署指针；`docs/company-rollout-guide.md`：顶部指向 Linux runbook
- `docs/progress.md`：本迭代挂牌

## 验证

- `go build ./...` 通过；`go test ./...` 全绿；`internal/config` 单测通过（var 化无行为变化）
- `bash -n` 两个 .sh 通过；`docker compose config` 渲染通过（env_file/挂载/命令清单核对）
- 两个 .ps1 PowerShell 5.1 解析通过（修掉一处 PS7 才有的 `||` 语句语法）；含中文均带 UTF-8 BOM
- **未做**（需真实服务器/明天部署时验收）：deploy.sh 全流程、ship-control-plane 端到端、客户端 `-PlatformUrl` 烧录后真机登录、同事端直传 RustFS（预签名 URL 走 LAN IP）

## 部署顺序备忘（明天）

1. 服务器：`git clone -b dev` → `deploy.sh`（知识服务 + 基础设施就绪，约 15 分钟）
2. 开发机：`ship-control-plane.ps1 -SshTarget root@IP`（控制面 + 建表）
3. 开发机：`build.ps1 -PlatformUrl http://IP:8090` → 分发安装包；同事登录，「知识」页填 `http://IP:8000`
4. 冒烟：浏览器 /lab 问答、客户端登录+上传（真机鼠标验收欠账一并做）、WPS 插件一台先试

## 遗留

- `deploy.sh` 的 LLM/Embedding key 仍需人工输入（密钥不入库，设计如此）
- 公网暴露前需加 HTTPS 网关 + 重评 captcha（README §五已记）
- 观察期后若停机窗口敏感：知识服务可双实例蓝绿（单 worker SQLite 限制需先解除）
