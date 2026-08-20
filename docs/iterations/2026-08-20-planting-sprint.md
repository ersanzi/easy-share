# 种下去冲刺：让产品在自己公司跑起来

> 发芽路线执行（定位见 [`../product-positioning.md`](../product-positioning.md) §三）。目标唯一：**两周内公司同事每天在用它查东西**。
> 开工：2026-08-20。

## 用户问题

产品功能已多但没被真人用过；用户的信心焦虑源于"种子没进土"。本冲刺交付的不是功能，是"能种下去"的完整条件。

## 目标

1. **共享盘目录监听自动入库**：指向目录（共享盘/钉盘同步夹），新文件/改动文件自动流入知识库——"文件沼泽一键变知识"落地。轮询实现（SMB 共享盘上 watchdog 事件不可靠），幂等靠内容哈希版本号（同内容重复扫不重复入库）。
2. **一键部署**：启动脚本（venv + 依赖检查 + uvicorn 单 worker）+ 开机自启（计划任务）。
3. **同事使用指引**：一页纸（登录/提问/往监听目录放文件），面向非开发者。

## 非目标

- 不做文件归属（2b 顺延）——先把内容流进来。
- 不做桌面端集成（下一冲刺）。
- 不做删除同步（监听目录删文件不删知识，显式行为，指引中说明）。

## 设计决策

- **轮询而非 watchdog**：文件系统事件在 SMB 共享盘/网络映射盘上不可靠，轮询对本地/共享盘行为一致（30s 间隔 + mtime 稳定性窗口防复制中文件）。
- **幂等三层**：指纹（size+mtime）跳过未变文件 → 内容 SHA256 作 version_id（同内容不重复入库）→ job_store 幂等（重启后重扫零成本）。
- **复用 lab 链路**：storage.write → create_or_get → runner.submit，不另建解析逻辑；file_id 按路径哈希稳定生成。
- **失败重试**（冒烟抓到的 bug）：入库失败不记录指纹，下一轮自动重试；目录缺失只告警不中断。
- **/lab 登录闭环**：登录条 UI + localStorage 令牌 + apiFetch 统一携带 Bearer；GET 引用链接支持 `?token=` 直开；/health 透出 auth/watch_dirs 状态。

## 完成记录

### 已完成（2026-08-20）

- `app/watcher/`：DirectoryWatcher（轮询/稳定性/指纹/内容版本去重/失败重试）；config 三字段；services 装配（随 start/close 生命周期）。
- `/lab` 登录闭环：index.html 登录条、lab.js 认证态与 apiFetch、401 自动弹登录、引用链接带 token；/health 新增 `auth` 与 `watch_dirs` 字段；auth 中间件支持 GET `?token=`。
- 部署件：`knowledge/scripts/start_server.ps1`（单 worker 启动）+ `install_autostart.ps1`（计划任务开机自启）；`.env.example` 补 AUTH/WATCH/MCP 段；**部署与使用一册** [`../company-rollout-guide.md`](../company-rollout-guide.md)（30 分钟部署 + 发群里的一页纸同事指引）。
- 测试：`tests/test_watcher.py` 6 条（新文件入库一次/幂等/改文件新版本/忽略扩展名/稳定性窗口/**失败重试**/目录缺失不崩）；全量 `pytest -m "not integration"` **120 passed, 1 skipped**。
- **真实端到端冒烟通过**（本机、真实 LLM+Embedding+RustFS）：丢文件进监听目录 → 自动入库（records=1）→ 登录拿令牌 → 提问"出差住宿标准" → 回答"每晚四百元"附引用与文档时间 → 无令牌 401。

### 环境事实（本机）

- Docker 引擎拉起后，9000 端口由既有 `mineru-web-rustfs-1` 实例服务（用户指定沿用，凭证已写入 knowledge/.env——本地文件不进库），`easyshare` bucket 已在其上创建；项目自带 `deploy/rustfs` compose 已让出端口。

### 已知限制与后续工作

- 删除不同步（显式设计）；登录条为实验台级体验，正式多端入口随桌面端集成迭代。
- 下一步按发芽路线：桌面端集成知识问答（把入口长进同事每天用的桌面端）。
