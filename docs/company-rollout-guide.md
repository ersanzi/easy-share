# 公司内部部署与使用指引（种下去手册）

> 面向"把 EasyShare 知识服务种进公司"的部署者和普通同事。目标：一台常开的电脑/服务器 + 共享目录 + 一页纸指引。
> 开发者文档见 [`../knowledge/README.md`](../knowledge/README.md)；本册只讲部署和使用。
>
> **服务器是 Linux 的走这里**：[`../deploy/server-linux/README.md`](../deploy/server-linux/README.md)（全套：知识服务 + RustFS + RuoYi 控制面 + PG/Redis，含快速迭代更新管道）；本册下文的一键部署（`deploy.ps1`）适用于** Windows 机器**当服务器的场景。

## 一、部署（IT 同学，一键约 5 分钟）

> 前提：一台常开的 Windows 机器（4 核 8G 起步，文档多再加大硬盘），能被同事局域网访问。

### 一键部署（推荐）

把仓库（或至少 `knowledge/` 目录）拷到服务器，然后：

```powershell
cd knowledge
powershell -ExecutionPolicy Bypass -File scripts\deploy.ps1
```

向导会依次完成并只问业务问题（缺什么问什么，全部支持参数无人值守）：

1. **Python 检查**：缺 3.11+ 会给 winget/下载指引；
2. **RustFS 对象存储**：本机已有则复用（填地址凭据）；没有则用 Docker 一键起新（`docker-compose.rustfs.yml`，数据落 `knowledge\rustfs-data\`，自动生成凭据）；
3. **依赖安装**：venv + pip（默认走清华镜像，公司网络友好）；
4. **生成 .env**：LLM/Embedding 凭据（可留空，降级纯检索/关键词模式）、入库目录（自动创建）、`AUTH_ENABLED=true` 默认开启；
5. **启动探活**：自动起服务并做 /health 检查，失败打印日志尾部；
6. **账号**：创建管理员 + 批量同事账号（逗号分隔用户名，随机初始口令打印）；
7. **防火墙**：放行 8000（需管理员权限；非管理员会打印 netsh 命令自己跑）;
8. **开机自启**：可选注册计划任务；
9. **生成 `同事使用指引.txt`**：含局域网访问地址、入库目录、账号清单——直接发群里。

无人值守示例：

```powershell
powershell -ExecutionPolicy Bypass -File scripts\deploy.ps1 -AdminPassword '<口令>' `
  -ColleagueUsernames 'xiaowang,xiaoli' -WatchDir 'D:\公司共享盘\知识库入库' `
  -RustfsAccessKey <k> -RustfsSecretKey <s> -LlmApiKey sk-xxx -LlmBaseUrl <url> -LlmModel <model> `
  -EmbeddingApiKey sk-xxx -EmbeddingBaseUrl <url> -EmbeddingModel <model> -NonInteractive
```

### 进阶：手动分步部署（脚本不适用时）

```powershell
# 在 knowledge/ 目录（代码从仓库拉取或拷贝）
python -m venv .venv
.\.venv\Scripts\Activate.ps1
pip install -r requirements.txt
# 可选增强：扫描件支持 → pip install -r requirements-ocr.txt；MCP → pip install mcp
Copy-Item .env.example .env   # 照 .env.example 注释逐项填写（RustFS/LLM/Embedding/AUTH/WATCH_DIRS）
```

手动启动与自启：

```powershell
# 手动启动（前台，先验证一切正常）
powershell -ExecutionPolicy Bypass -File scripts\start_server.ps1
# 验证：http://localhost:8000/health 返回 ok，且 /lab 页面能打开

# 注册开机自启（当前用户登录即启动）
powershell -ExecutionPolicy Bypass -File scripts\install_autostart.ps1

# 防火墙放行（管理员 PowerShell，同事能访问的必要条件）
netsh advfirewall firewall add rule name="EasyShare Knowledge (8000)" dir=in action=allow protocol=TCP localport=8000
```

手动开账号（也可用向导代劳）：

```powershell
curl -X POST http://localhost:8000/auth/bootstrap -H "Content-Type: application/json" -d '{"username":"admin","password":"<管理员口令>"}'
# 登录拿 token 后为同事开账号：
curl -X POST http://localhost:8000/auth/login -H "Content-Type: application/json" -d '{"username":"admin","password":"<管理员口令>"}'
curl -X POST http://localhost:8000/auth/users -H "Authorization: Bearer <token>" -H "Content-Type: application/json" -d '{"username":"xiaowang","password":"<初始口令>"}'
```

### WPS 知识查询（可选，每台需要用的电脑各跑一次）

```powershell
# 前提：能访问 knowledge\scripts\install_wps_addon.ps1（仓库任意位置改成相对/绝对路径均可）
# 把 <服务器IP> 换成知识服务器地址；装完完全退出 WPS（含托盘）再打开
powershell -ExecutionPolicy Bypass -File <仓库路径>\knowledge\scripts\install_wps_addon.ps1 -ServerUrl http://<服务器IP>:8000
# 卸载：
powershell -ExecutionPolicy Bypass -File <仓库路径>\knowledge\scripts\install_wps_addon.ps1 -Remove
```

装好后 WPS 文字功能区多一个「知识」页签：**选中一段话 → 点「查知识」**，右侧窗格自动给出答案和引用来源（首次需登录一次账号）。

## 二、同事使用指引（发群里的一页纸）

> **公司知识库上线了：文件放进去，答案问出来。**

1. **怎么问**：浏览器打开 `http://<服务器IP>:8000/lab`，登录后底部「知识问答」输入问题。回答带引用，点引用可看原文出处。
2. **装了 WPS 插件的**：在 WPS 文字里选中一段话，点「知识」页签的「查知识」，答案直接出现在右侧（首次登录一次）。
3. **怎么贡献文件**：把文件放进共享盘的 `知识库入库` 文件夹（支持 Word/PDF/Excel/PPT/TXT/Markdown/图片），约 1 分钟后自动入库可被检索；文件更新后重新放入即可，答案会引用最新版。
4. **注意**：从入库文件夹**删除**文件不会从知识库删除（需要管理员处理）；敏感文件先问 IT 再放；支持的格式见文件夹说明。

## 三、日常维护

- **看健康度**：`http://<服务器IP>:8000/lab/cockpit`（质量驾驶舱：文档规模/使用率/盲区/回答质量）。
- **服务没起来**：重启机器（自启生效）或手动跑 `start_server.ps1`。
- **日志**：控制台输出；建议 NSSM/计划任务重定向到文件（后续版本内置）。

## 四、已知边界（部署前知悉）

- 单 worker 运行（SQLite 状态），百人级团队足够；更大规模再演进（向量库切换条件见下「向量库演进」）。
- 向量检索为知识服务**进程内实现**（JSON 文件 + numpy 余弦），不部署独立向量库——观察期部署清单不含 Milvus，切换条件与步骤见下「向量库演进」。
- 监听目录删除不同步（显式设计，防误删）；`AUTH_ENABLED=true` 下 API 需令牌，/lab 页面走登录令牌，桌面端「知识」页与 WPS 窗格登录态分别由 Core 与窗格本地保存。
- 桌面端（v0.1.0 起）已有「知识」页：登录公司知识服务器即可问答；WPS 插件按需逐台安装（见一·6）。

### 向量库演进（后期优化项，观察期不执行）

**现状**：向量存在知识服务进程内（`knowledge/data/vector_store.json`，内存 + numpy 余弦暴力扫描），不依赖独立向量库服务。Milvus 后端已预留：`app/kb/milvus_store.py` 与 JSON 实现（`app/kb/store.py`）同接口，`.env` 的 `MILVUS_URI` 留空即自动退回 JSON——`deploy.ps1` 默认部署不含 Milvus。

**不默认启用的原因**：Milvus standalone 需 etcd + milvus 两个常驻容器（合计约 2~4G 内存），4C8G 服务器还要跑 PG/Redis/RustFS/控制面/知识服务；且项目定性「向量 = 可重建缓存」（源头文档在 RustFS），Milvus 的持久化/高可用在当前规模发挥不出价值。numpy 暴力扫描在 10 万 chunk 内为几十毫秒级，百人公司观察期远达不到。

**切换触发条件（满足其一即重评估）**：

1. 索引 chunk 数逼近 **10 万**（暴力扫描延迟随量线性增长）；
2. 检索 P95 延迟 **> 500ms**（质量驾驶舱 `/lab/cockpit` 可观测）；
3. 需要多 worker 横向扩展（届时与 SQLite 状态的单 worker 限制一并演进）。

**切换步骤（资产已铺好，约十分钟）**：

```powershell
# 1) 起 Milvus（etcd + milvus 两容器，对象存储复用 RustFS，不额外跑 MinIO）
cd knowledge
docker compose -f docker-compose.milvus.yml up -d

# 2) 装 pymilvus，.env 追加 MILVUS_URI（collection 默认 easyshare_chunks）
.venv\Scripts\pip install -r requirements-milvus.txt
#    MILVUS_URI=http://127.0.0.1:19530

# 3) 重启知识服务，存量文档重灌向量——向量是可重建缓存，源头在 RustFS：
#    对存量文件重跑 /ingest 或经入库目录重放触发新版本，索引按 replace_doc 语义整体替换
```

> 澄清：`deploy/ruoyi-db/README.md` 提到的 milvus/es 是 RuoYi-Vue-Plus 可选 snail-ai 模块的构建依赖（构建时已跳过），与知识服务的向量库无关。
