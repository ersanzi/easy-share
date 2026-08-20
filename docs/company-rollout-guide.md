# 公司内部部署与使用指引（种下去手册）

> 面向"把 EasyShare 知识服务种进公司"的部署者和普通同事。目标：一台常开的电脑/服务器 + 共享目录 + 一页纸指引。
> 开发者文档见 [`../knowledge/README.md`](../knowledge/README.md)；本册只讲部署和使用。

## 一、部署（IT 同学，约 30 分钟）

### 1. 准备一台常开的 Windows 机器

配置不用高（4 核 8G 起步，文档多再加大硬盘）；需要能被同事访问（同局域网）；RustFS 对象存储跑在同一台或邻近机器（已有 Docker 部署则复用）。

### 2. 安装服务

```powershell
# 在 knowledge/ 目录（代码从仓库拉取或拷贝）
python -m venv .venv
.\.venv\Scripts\Activate.ps1
pip install -r requirements.txt
# 可选增强：扫描件支持 → pip install -r requirements-ocr.txt；MCP → pip install mcp
Copy-Item .env.example .env
```

### 3. 配置 `.env`（最少必填）

```ini
RUSTFS_ENDPOINT=...        # 对象存储地址与凭证（已有部署照抄）
RUSTFS_ACCESS_KEY=...
RUSTFS_SECRET_KEY=...

LLM_BASE_URL=...           # 生成回答用（OpenAI 兼容；不配则纯检索模式）
LLM_API_KEY=...
LLM_MODEL=...

EMBEDDING_BASE_URL=...     # 语义检索用（不配则只剩关键词检索）
EMBEDDING_API_KEY=...
EMBEDDING_MODEL=...

AUTH_ENABLED=true          # 公司多人使用务必开启
WATCH_DIRS=D:\公司共享盘\知识库入库   # 指向共享目录（分号分隔可多个）
```

### 4. 启动与自启

```powershell
# 手动启动（前台，先验证一切正常）
powershell -ExecutionPolicy Bypass -File scripts\start_server.ps1
# 验证：http://localhost:8000/health 返回 ok，且 /lab 页面能打开

# 注册开机自启（当前用户登录即启动）
powershell -ExecutionPolicy Bypass -File scripts\install_autostart.ps1
```

### 5. 开通账号（首次）

```powershell
curl -X POST http://localhost:8000/auth/bootstrap -H "Content-Type: application/json" -d '{"username":"admin","password":"<管理员口令>"}'
# 登录拿 token 后为同事开账号：
curl -X POST http://localhost:8000/auth/login -H "Content-Type: application/json" -d '{"username":"admin","password":"<管理员口令>"}'
curl -X POST http://localhost:8000/auth/users -H "Authorization: Bearer <token>" -H "Content-Type: application/json" -d '{"username":"xiaowang","password":"<初始口令>"}'
```

## 二、同事使用指引（发群里的一页纸）

> **公司知识库上线了：文件放进去，答案问出来。**

1. **怎么问**：浏览器打开 `http://<服务器IP>:8000/lab`，登录后底部「知识问答」输入问题。回答带引用，点引用可看原文出处。
2. **怎么贡献文件**：把文件放进共享盘的 `知识库入库` 文件夹（支持 Word/PDF/Excel/PPT/TXT/Markdown/图片），约 1 分钟后自动入库可被检索；文件更新后重新放入即可，答案会引用最新版。
3. **注意**：从入库文件夹**删除**文件不会从知识库删除（需要管理员处理）；敏感文件先问 IT 再放；支持的格式见文件夹说明。

## 三、日常维护

- **看健康度**：`http://<服务器IP>:8000/lab/cockpit`（质量驾驶舱：文档规模/使用率/盲区/回答质量）。
- **服务没起来**：重启机器（自启生效）或手动跑 `start_server.ps1`。
- **日志**：控制台输出；建议 NSSM/计划任务重定向到文件（后续版本内置）。

## 四、已知边界（部署前知悉）

- 单 worker 运行（SQLite 状态），百人级团队足够；更大规模再演进。
- 监听目录删除不同步（显式设计，防误删）；`AUTH_ENABLED=true` 下 API 需令牌，/lab 页面当前用浏览器登录态走 token（随桌面端集成完善）。
- 桌面端（局域网直传/网盘）与知识服务尚在整合中，当前知识库独立使用。
