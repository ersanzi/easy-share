# Python 本地文档处理可视化实验台

> 日期：2026-07-25
> 状态：已完成
> 定位：仅用于本地开发与测试可视化，不是 EasyShare 客户端功能，不代表最终产品 UI 方案

## 1. 用户问题

Go 上传到 RustFS 后触发 Python 处理可以继续推进，但在是否接入桌面客户端尚未确定前，先提供一个通过浏览器打开的本地 Web 页面，方便上传文档、观察处理进度和检查派生产物。

## 2. 本轮边界

- 页面由 Python FastAPI 服务提供，只允许通过回环地址访问；
- 页面和配套上传 API 明确标记为 `Local Lab / 测试工具`；
- 不修改 Wails/Vue 客户端，不把实验台当成正式用户入口；
- 不引入账号、多租户、RBAC 或 Java 控制面；
- 上传文件写入 RustFS 后复用正式 `DocumentPipeline`，不另建解析逻辑；
- 页面用于查看任务状态、失败位置、错误以及 `clean.md`、`document.json`、`manifest.json`；
- 接口无生产认证能力，禁止监听公网地址。

## 3. 实现结果

### 3.1 Local Lab 页面与接口

新增本地实验台：

```http
GET  /lab
GET  /lab/assets/lab.css
GET  /lab/assets/lab.js
POST /lab/api/uploads
GET  /lab/api/jobs?limit=20
```

支持上传：

```text
.txt .md .markdown .docx .pdf .xlsx .pptx
```

页面提供：

- 拖拽或点击选择文件；
- `入队 → RustFS 读取 → 解析清洗 → 保存产物 → 切块 → Embedding → 索引 → Manifest` 八阶段轨道；
- 当前任务、最近任务、失败原因与重试入口；
- `clean.md`、`document.json`、`manifest.json` 三类产物切换查看；
- 响应式布局、键盘焦点和减少动画偏好支持。

页面顶部和页脚均明确说明：这是测试辅助界面，不进入 Wails 客户端交付范围，也不代表最终产品界面。

### 3.2 上传与任务复用

浏览器上传按以下流程执行：

1. 校验扩展名、文件大小和可选的 `file_id/version_id`；
2. 清理路径穿越字符和非法文件名；
3. 默认生成 `file_id=lab-{uuid}`、`version_id=v1`；
4. 写入 `lab/uploads/{file_id}/{version_id}/{filename}`；
5. 创建或复用 SQLite 任务；
6. 交给现有 `JobRunner` 与 `DocumentPipeline`；
7. 继续使用正式派生产物和版本化索引逻辑。

任务列表新增 `find_latest()` 与 `list_recent()`，查询数量限制在 `1..100`。失败任务的页面轨道按已保存的 `progress` 推断失败阶段，避免任务存储把 `stage` 置为 `failed` 后错误回到第一节点。

### 3.3 安全边界

新增配置：

```text
LOCAL_LAB_ENABLED=true
```

路由同时执行两层检查：

- `LOCAL_LAB_ENABLED=false` 时返回 `404`；
- 客户端地址不是 `127.0.0.1`、`::1` 或测试客户端时返回 `403`。

启动命令必须显式使用：

```powershell
cd knowledge
.\.venv\Scripts\python.exe -m uvicorn app.main:app --host 127.0.0.1 --port 8000 --workers 1
```

当前 `JobRunner` 是进程内线程池，SQLite 和 JSON 向量库也属于本地过渡实现，因此禁止用多个 Uvicorn worker 运行实验台。

## 4. 验证结果

### 4.1 自动化测试

新增 `knowledge/tests/test_lab.py`，覆盖：

- 页面产品边界文案和静态资源；
- TXT 与 DOCX 上传；
- RustFS 对象键、文件名清理和 Content-Type；
- 完整处理管线与三类派生产物；
- 最近任务顺序和 `limit`；
- 不支持格式、超限、非法 ID；
- 实验台禁用和非回环访问。

验证命令与结果：

```powershell
knowledge/.venv/Scripts/python.exe -m pytest knowledge/tests/test_lab.py -q
# 5 passed, 1 warning

knowledge/.venv/Scripts/python.exe -m pytest knowledge/tests -q
# 29 passed, 1 skipped, 1 warning
```

唯一警告来自 FastAPI/Starlette TestClient 对当前 httpx 兼容层的上游弃用提示，不影响本轮功能验证。

### 4.2 浏览器真实闭环

使用真实浏览器完成：

```text
TXT 上传
  → RustFS PutObject
  → DocumentPipeline
  → Embedding
  → 版本化索引
  → manifest.json
```

结果：任务显示 `completed / 100%`，最近任务可选择，三类产物均能读取；新浏览器会话控制台为 `0 errors / 0 warnings`。验证截图保存为：

```text
output/playwright/local-lab-completed.png
```

该截图是本地测试证据，不属于正式客户端视觉稿。

### 4.3 项目全量构建

按项目约定顺序验证：

```powershell
go build ./...                                          # 通过
go test ./...                                           # 通过
npm --prefix frontend run build                         # 通过
npm --prefix frontend test                              # 通过
wails build                                             # 通过，产出 build/bin/easyshare.exe
go build -o build/bin/easyshare-core.exe ./cmd/core     # 通过
```
## 5. 排障记录

### 5.1 上传接口启动时报 multipart 错误

FastAPI 接收 `UploadFile` 需要安装 `python-multipart`。依赖已加入 `knowledge/requirements.txt`；更新依赖后重新安装：

```powershell
.\.venv\Scripts\python.exe -m pip install -r requirements.txt
```

### 5.2 RustFS 返回 `SignatureDoesNotMatch`

本次真实浏览器上传首次遇到该错误，根因是 `knowledge/.env` 的 RustFS secret 与正在运行的 `deploy/rustfs/.env` / RustFS 容器不一致。

排查时只比较凭据是否一致，不要把 secret 打印到日志或提交到仓库。修正 `.env` 后必须重启 Python 服务，Pydantic Settings 才会重新读取配置。

### 5.3 页面返回 403 或 404

- `403`：请求不是来自回环地址；实验台有意拒绝局域网和公网访问。
- `404`：检查 `LOCAL_LAB_ENABLED` 是否为 `true`。
- 不要为了临时演示改成 `0.0.0.0`；当前接口没有生产认证、多租户隔离或 RBAC。

### 5.4 任务一直排队

必须从 `knowledge/` 目录启动并使用 `--workers 1`。多个 worker 各自持有进程内 `JobRunner`，同时共享 SQLite/JSON 存储，不属于受支持部署方式。

## 6. 后续决策

- 暂不把 `/lab` 接到 Wails 客户端；是否进入桌面端需单独做产品决策和正式 UI 设计。
- 下一阶段继续强化 Python：扫描件 OCR、复杂版面结构、结构感知切块、Milvus。
- Java 仍后置，等多租户、权限、文件登记和业务任务真相源进入实施阶段再接入。
