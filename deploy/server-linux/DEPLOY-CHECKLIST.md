# 部署日 Checklist（照着打勾）

> 本页是**执行序**，只列动作与验收点；命令细节以 [`README.md`](README.md)（Linux 服务器侧）
> 与 [`docs/company-rollout-guide.md`](../../docs/company-rollout-guide.md)（Windows 机器当服务器/使用指引）为准。
> 全部勾完即部署完成、进入两周观察期。创建：2026-10-11。

## 0. 前一天（开发机）

- [ ] 服务器 IP 已确定，同事网段可达（`ping` / `curl http://<IP>:8090` 通）
- [ ] 本仓已推送 Gitee（服务器 clone 走 Gitee）：`git push origin dev`
- [ ] 公司版安装包已产出：`powershell -ExecutionPolicy Bypass -File scripts\build.ps1 -PlatformUrl http://<IP>:8090`（v0.2.0+，先别发群）
- [ ] 开发机能 ssh 到服务器（首次配免密：README §二·2）

## 1. 服务器：基础设施 + 控制面（约 20 分钟）

- [ ] 前置齐备：docker（含 compose 插件）、git、python3.11+
- [ ] `git clone https://gitee.com/liilaifeng/easy-share.git /opt/easyshare/repo`
- [ ] `bash /opt/easyshare/repo/deploy/server-linux/deploy.sh`（幂等；会问 LLM/Embedding key、入库目录、账号口令）
- [ ] `docker compose ps` 五容器全 healthy；`curl http://127.0.0.1:8000/health` 与 `curl http://127.0.0.1:8090/auth/tenant/list` 均应答
- [ ] **RustFS 已升 1.0.1 稳定版**（compose 已钉）：`docker compose pull rustfs` 需服务器可达 Docker Hub——不通则本机离线导入镜像；起后重验**存储三路**：
  - [ ] 预签名直传：桌面端登录后上传文件成功
  - [ ] 「此电脑」挂载盘：空间盘出现、上传/下载正常
  - [ ] 知识服务：文档入库后 /lab 能查到
- [ ] 防火墙：8000 / 8090 / **9000** 对同事网段放行（9000 是预签名直传必经）；9001 控制台收口到办公网段
- [ ] 开发机投递控制面：`ship-control-plane.ps1 -SshTarget root@<IP>`（DDL 幂等自动重放，探活 200 即成）

## 2. 管理配置（客户端管理页）

- [ ] admin 默认口令**立即修改**（默认 admin/admin123）
- [ ] 建部门 + 批量开通同事账号；**知识服务账号同步开通且与登录名一致**（deploy.sh 向导已代劳则核对）
- [ ] 管理页「服务配置」登记 `http://<IP>:8000`（不登记也行，客户端同主机 `:8000` 推导兜底）
- [ ] 按需：共享空间按部门授权 / 文档级可见部门（visible_depts）

## 3. 同事端分发

- [ ] 分发清单六项过一遍（README §二·3 末尾；杀软加白、[`同事上手指南`](../../docs/company-desktop-guide.md) 发群）
- [ ] 首台机器陪装：安装器**防火墙 UAC 询问选「是」**（否则局域网互传搜不到设备）
- [ ] 首台机器端到端：登录（账号已预填）→「知识」页免手填直接问 → 网盘上传 → 此电脑空间盘出现

## 4. 观察期启动（两周）

- [ ] 真实文档放入监听目录，约 1 分钟后 /lab 或驾驶舱可见入库
- [ ] WPS 真机冒烟：`install_wps_addon.ps1 -ServerUrl http://<IP>:8000`，选中段落「查知识」出引用
- [ ] （可选）MinerU 冒烟：部署 mineru-api 后放扫描件 PDF 走深度解析
- [ ] 每周在服务器跑 `python knowledge/scripts/weekly_report.py --days 7`；两周后据周报做观察期决策

## 出问题

- 知识服务更新失败：`update.sh` 自动回退；控制面：`cp ruoyi-admin.jar.bak ruoyi-admin.jar && docker compose up -d --no-deps ruoyi`
- 日志：知识服务 `journalctl -u easyshare-knowledge -f`；控制面 `docker logs -f easyshare-ruoyi`
- 排障手册：[`docs/troubleshooting.md`](../../docs/troubleshooting.md)
