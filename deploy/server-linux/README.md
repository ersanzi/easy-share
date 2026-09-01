# EasyShare Linux 服务器部署（生产）

> 面向「一台 Linux 服务器跑全套」的部署与日常更新。Windows 机器部署（种下去手册）见
> [`../../docs/company-rollout-guide.md`](../../docs/company-rollout-guide.md)；本册是它的 Linux 服务器版。
> 服务器要求：2C4G 起步可跑，推荐 4C8G；数据盘 100G+；内网可达（同事电脑能访问）。

## 一、服务器上跑什么

| 部件 | 形态 | 端口（暴露范围） | 更新方式 |
| --- | --- | --- | --- |
| 知识服务（FastAPI） | 宿主机 venv + systemd | 8000（局域网） | `update.sh`（git pull，约 10 秒停机） |
| 控制面（RuoYi fat jar） | Docker 容器（temurin 21-jre） | 8090（局域网） | 开发机 `ship-control-plane.ps1` |
| RustFS 对象存储 | Docker 容器 | **9000（局域网，预签名直传）**、9001（管理台） | `docker compose pull` + up |
| PostgreSQL 16 | Docker 容器 | 127.0.0.1:5433（仅本机） | 同上 |
| Redis 7 | Docker 容器 | 127.0.0.1:6380（仅本机） | 同上 |

为什么控制面用容器、知识服务裸跑：jar 需要的环境只有 JRE，容器化免装 JDK21 且重启语义统一；
知识服务要 git pull + venv 的快速迭代节奏，宿主机直跑最顺。**控制面 jar 永远在开发机构建**
（`platform/` RuoYi 源码树 gitignore，服务器建不了），这是设计而非偷懒。

目录约定（`EASYSHARE_ROOT`，默认 `/opt/easyshare`）：

```
/opt/easyshare/
├── compose.yaml          # 由 deploy.sh 拷入（源：repo/deploy/server-linux/compose.yaml）
├── repo/                 # git clone 本仓；update.sh 在这里 git pull
├── control-plane/        # ruoyi-admin.jar(+.bak) / easyshare-drive.yml / rustfs.env / sql/
└── watch-inbox/          # 知识库入库目录（同事往这放文件）
```

## 二、首次部署（约 15 分钟）

### 0. 开发机（首次必做）

把仓库推到 Gitee——下一步服务器要从 Gitee clone，本目录这套部署资产得先在仓库里：

```bash
git push origin dev
```

### 1. 服务器侧（root）

```bash
# 前置：docker（含 compose 插件）、git、python3.11+（Ubuntu24.04: apt install -y python3 python3-venv python3-pip）
git clone https://gitee.com/liilaifeng/easy-share.git /opt/easyshare/repo
bash /opt/easyshare/repo/deploy/server-linux/deploy.sh
```

deploy.sh 做完（幂等，可重跑）：PG/Redis/RustFS 容器、rustfs.env（随机凭据）、知识服务
venv + .env、桶初始化、systemd 单元、管理员与同事账号、防火墙放行、《同事使用指引.txt》。
会问的只有业务项：LLM/Embedding 的 key（可留空降级）、入库目录、账号口令。

### 2. 开发机侧：送控制面（Windows PowerShell，仓库根目录）

> **为什么控制面要单独"送"**：它要先编译成 jar 才能跑，而 RuoYi 源码工程 `platform/`
> 在 gitignore 里、只存在于开发机（服务器第 1 步 clone 的仓库里没有这个目录），
> 所以 jar 永远是**开发机构建 → 传文件到服务器 → 服务器跑**。服务器不用装 JDK，
> RuoYi 跑在 Docker 容器里，容器自带 Java 运行环境。知识服务是纯 Python 源码，
> 服务器自己 git 拉下来就能跑——两者的更新方式因此不同（§三）。

一次性配置免密 ssh（脚本要远程执行多次命令，不配则每一步都弹密码输入，基本没法用）：

```powershell
# ① 开发机生成密钥对（Win10+ 自带 ssh；一路回车）
ssh-keygen -t ed25519

# ② 把公钥放到服务器上（这一步要输最后一次密码）
type $env:USERPROFILE\.ssh\id_ed25519.pub | ssh root@<服务器IP> "mkdir -p ~/.ssh && cat >> ~/.ssh/authorized_keys"

# ③ 验证：下面这条不再要密码即成功
ssh root@<服务器IP> "echo ok"
```

投递（以后改了控制面代码要更新服务器，也是跑这一条）：

```powershell
powershell -ExecutionPolicy Bypass -File scripts\ship-control-plane.ps1 -SshTarget root@<服务器IP>
# 快速重发不重新编译：加 -SkipBuild
```

脚本自动完成，全程约 1 分钟：

```
开发机                                  服务器
──────                                  ──────
1. mvnw 编译 ruoyi-admin.jar
2. scp 传 jar + 建表 SQL        ───►    收到文件
3. 远程执行                     ───►    4. SQL 灌入 PostgreSQL（建账号/云盘表；
                                         仅首次，之后自动检测跳过）
5. 远程执行                     ───►    6. 旧 jar 备份为 .bak → 换新 jar
                                         → docker 重启 RuoYi 容器
7. 探活 8090（Spring 启动 30-60s）──►   8. 应答 → 完成
```

连不上服务器时先确认 sshd 开着：服务器上 `systemctl status sshd`。
默认账号 admin/admin123，**上线后第一时间在客户端管理页改密**。

### 3. 客户端分发（同事的 Windows 电脑）

```powershell
# 开发机构建「公司版」安装包：控制面地址烧进默认配置，同事装完即用、免手工改 config.json
powershell -ExecutionPolicy Bypass -File scripts\build.ps1 -PlatformUrl http://<服务器IP>:8090
```

- 装完登录（admin 开账号）；「知识」页首次使用时填 `http://<服务器IP>:8000` 并登录知识服务账号；
- 之后的版本升级走在线升级通道（`scripts/publish-release.ps1 -PlatformUrl http://<服务器IP>:8090`），同事端自动检查安装，无需再发安装包；
- WPS 插件按需：`install_wps_addon.ps1 -ServerUrl http://<服务器IP>:8000`（rollout guide §一·6）。

## 三、快速迭代更新（三种通道）

| 改了什么 | 在哪做什么 | 停机 |
| --- | --- | --- |
| knowledge/（Python） | 服务器：`bash /opt/easyshare/repo/deploy/server-linux/update.sh` | ~10s，失败自动回退 |
| platform-drive / 控制面（Java） | 开发机：`ship-control-plane.ps1 -SshTarget ...`（改完先推 git 存档） | ~30s |
| 客户端（Go/Vue） | 开发机：`build.ps1 -PlatformUrl ...` → `publish-release.ps1 -PlatformUrl ...` | 无（同事端自动升级） |
| PG 增量 SQL（deploy/ruoyi-db/*.sql） | 服务器手动：`docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < 文件`（**灌前备份**） | — |

数据安全：`.env`、`repo/knowledge/data/`、RustFS/PG 的 docker volume 都不在 git 里，
git pull / 换 jar 都碰不到。回滚：知识服务 update.sh 自动做；控制面 `cp ruoyi-admin.jar.bak ruoyi-admin.jar` 后 `docker compose up -d --no-deps ruoyi`。

## 四、运维速查

```bash
# 知识服务日志 / 状态
journalctl -u easyshare-knowledge -f
systemctl status easyshare-knowledge

# 控制面日志 / 重启
docker logs -f easyshare-ruoyi
cd /opt/easyshare && docker compose restart ruoyi

# 数据库备份（更新表结构前必做）
docker exec easyshare-ruoyi-pg pg_dump -U ruoyi ryvue > ~/ryvue-$(date +%F).sql

# 健康检查入口
curl http://127.0.0.1:8000/health        # 知识服务
curl http://127.0.0.1:8090/auth/tenant/list  # 控制面（200 即活）
```

## 五、已知决策与坑

- **RUSTFS_ENDPOINT 必须填局域网 IP**（`control-plane/rustfs.env`）：控制面签发的预签名 URL
  会把这个地址原样发给客户端，填 127.0.0.1 同事电脑就直传失败。知识服务自己的
  `RUSTFS_ENDPOINT` 倒是 127.0.0.1（服务端自访问）——两处用途不同，别「顺手统一」。
- **captcha / api-decrypt 关闭**（compose 启动参数）：桌面客户端登录不走验证码与接口加密，
  开着会登不进去。内网部署可接受；暴露公网前必须重新评估（再加 HTTPS 网关）。
- **PG/Redis 端口沿用 dev 的 5433/6380**：与 `deploy/ruoyi-db` 一套心智，且都只绑 127.0.0.1。
- **compose 里 ruoyi 启动参数是平移 `run-ruoyi-admin.ps1`**：命令行参数优先级最高，不依赖
  profile 差异；升级 platform/ 后若 dev 启动脚本加了参数，这里要同步。
- **服务器代码跟 dev 分支**：update.sh 用 `fetch + reset origin/dev` 确定性同步到刚推的
  提交（服务器仓库不做本地改动）；想跟稳定版，把 update.sh 里的 dev 改成 master。
- 向量库不部署（进程内 JSON + numpy），切换 Milvus 的条件与步骤见
  [`../../docs/company-rollout-guide.md`](../../docs/company-rollout-guide.md) §四「向量库演进」。
