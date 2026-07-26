# RustFS 真实集成测试与 Office 黄金语料

> 日期：2026-07-25
> 状态：已完成

## 1. 用户问题

在 Python 文档处理闭环完成后，用户确认继续推进两个 P0：

1. 使用真实 RustFS 验证原始对象读取、清洗产物写回和索引结果；
2. 建立首批 Office 黄金测试文档，固定 DOCX、XLSX、PPTX 和文本型 PDF 的结构化解析预期。

## 2. 本轮目标

- 集成测试默认不依赖外部服务，只有显式开启并提供凭据时才连接本地 RustFS；
- 测试使用唯一 `file_id` 创建源对象，结束时只清理该源对象和三个已知派生产物，不能污染用户文件；
- 验证 `source object → Python pipeline → derived artifacts → versioned index` 的真实 S3 兼容读写闭环；
- 黄金样本由代码确定性生成，避免提交来源不明且难审查的二进制文件；
- 预期结构采用可读 JSON 清单，既用于自动断言，也便于人工评审解析质量；
- 固定结构块、来源位置、Markdown 渲染和重复生成的语义一致性。

## 3. 技术决策

### 3.1 集成测试采用显式开关

Python 与现有 Go 集成测试统一使用：

```text
EASYSHARE_RUSTFS_INTEGRATION=1
```

并要求提供 endpoint、access key、secret key 和已存在的 bucket。测试不会自动创建或删除 bucket，避免误操作共享开发环境。普通 `pytest` 未设置开关时只跳过一项真实存储测试。

### 3.2 测试对象必须可精确清理

源对象使用：

```text
integration/python/rustfs-it-{uuid}/word-policy.docx
```

派生产物使用正式管线键：

```text
derived/{fileId}/v1/clean.md
derived/{fileId}/v1/document.json
derived/{fileId}/v1/manifest.json
```

`finally` 中只删除以上四个已知对象，不按宽泛前缀批量删除，也不触碰 bucket 中其他文件。

### 3.3 黄金二进制由代码构造

`knowledge/tests/golden/builders.py` 使用 `python-docx`、`openpyxl`、`python-pptx` 和 `pypdf` 构造最小但有代表性的样本；`cases.json` 保存人工可审查的预期结构。需要手工打开文档时，再运行脚本物化到已忽略的 `generated/` 目录。

首批语料覆盖：

- DOCX：一级/二级标题、段落、表格；
- XLSX：多工作表、表格行与来源工作表；
- PPTX：多幻灯片、标题、正文、表格；
- 文本型 PDF：文本页和空白页 warning。

### 3.4 RustFS 客户端配置可注入

`RustFSStorage` 增加可选 `botocore.config.Config`，生产默认仍使用 S3 v4 签名；集成测试注入短连接/读取超时和有限重试，服务不可达时能快速失败，而不是长时间挂起。

## 4. 实现影响

- `knowledge/app/storage/rustfs.py`：允许注入 botocore 客户端配置；
- `knowledge/app/parsing/extractor.py`：PPTX 标题按稳定的 `shape_id` 判断；
- `knowledge/tests/golden/`：生成器、黄金预期、解析和可重复性测试；
- `knowledge/scripts/build_golden_corpus.py`：物化可人工查看的测试文档；
- `knowledge/tests/integration/`：真实 RustFS 文档管线测试；
- `knowledge/pytest.ini`：登记 integration marker；
- `knowledge/README.md`、`deploy/rustfs/README.md`、`docs/troubleshooting.md`：测试与排障说明。

## 5. 验证结果

### 5.1 黄金语料

```powershell
$env:PYTHONDONTWRITEBYTECODE = '1'
knowledge/.venv/Scripts/python.exe -m pytest knowledge/tests/golden -q
```

结果：

```text
8 passed
```

### 5.2 默认 Python 回归

```powershell
$env:PYTHONDONTWRITEBYTECODE = '1'
knowledge/.venv/Scripts/python.exe -m pytest knowledge/tests -q
```

结果：

```text
24 passed, 1 skipped, 1 warning
```

跳过项是需要显式开关的真实 RustFS 测试；warning 来自 FastAPI TestClient 的 Starlette/httpx 迁移提示，不影响本轮功能。

### 5.3 真实 RustFS 闭环

Docker Desktop 启动后，先显式唤起 `docker-desktop` WSL 发行版，再使用本地已有 `easyshare` bucket 运行：

```powershell
$env:EASYSHARE_RUSTFS_INTEGRATION = '1'
$env:EASYSHARE_RUSTFS_ENDPOINT = 'http://127.0.0.1:9000'
$env:EASYSHARE_RUSTFS_ACCESS_KEY = '<与 deploy/rustfs/.env 一致>'
$env:EASYSHARE_RUSTFS_SECRET_KEY = '<与 deploy/rustfs/.env 一致>'
$env:EASYSHARE_RUSTFS_BUCKET = 'easyshare'
knowledge/.venv/Scripts/python.exe -m pytest knowledge/tests/integration -q -m integration
```

结果：

```text
1 passed
```

实际验证了：源 DOCX 写入和读取、三类派生产物存在且 Content-Type 正确、结构化块与 Markdown 内容、manifest 完成状态、任务阶段回调、`v1` 向量索引，以及测试对象精确清理。

同一 RustFS 实例还通过了既有 Go 对象存储一致性测试：

```powershell
go test ./internal/cloud/objectstore/s3store -run '^TestRustFSIntegration$' -count=1 -v
```

结果：

```text
PASS
```

### 5.4 项目完整构建

按项目规定顺序验证：

```powershell
go build ./...
go test ./...
npm --prefix frontend run build
npm --prefix frontend test
wails build
go build -o build/bin/easyshare-core.exe ./cmd/core
```

结果全部通过；前端 Vitest 为 `4 passed / 10 tests passed`，Wails 成功生成 `build/bin/easyshare.exe`，Core 成功生成 `build/bin/easyshare-core.exe`。
## 6. 排障记录

### 6.1 Docker Desktop 进程存在但 API 返回 Internal Server Error

现象：

```text
request returned Internal Server Error ... /v1.24/info
```

同时 `wsl -l -v` 显示 `docker-desktop` 为 `Stopped`。仅启动 Windows 侧 Docker Desktop 进程不一定会立即唤起 WSL 后端，可执行：

```powershell
wsl.exe -d docker-desktop -e sh -lc 'echo ready'
docker version
```

确认 `docker version` 同时显示 Client 和 Server 后再执行 Compose。不要把只有 Client 信息或空的 ServerVersion 当作 daemon 已就绪。

### 6.2 bucket 不存在或凭据不匹配

集成测试会先执行 `HeadBucket` 并快速失败。先在 RustFS Console 创建专用 bucket，再确认测试变量与 `deploy/rustfs/.env` 一致。测试本身不会创建 bucket，也不会输出 secret key。

### 6.3 PPTX 标题被解析为普通段落

`python-pptx` 的 `slide.shapes.title` 与遍历 `slide.shapes` 得到的代理对象不保证 Python 对象身份相同，因此不能使用 `shape is title_shape`。应比较稳定的 `shape_id`。黄金语料首次运行即捕获了该问题，修复后标题和 Markdown 一级标题断言通过。

### 6.4 黄金文件需要人工查看

```powershell
knowledge/.venv/Scripts/python.exe knowledge/scripts/build_golden_corpus.py
```

输出目录 `knowledge/tests/golden/generated/` 已被忽略。不要把生成的 Office/PDF 二进制文件提交到仓库；修改生成器后应同时审查并更新 `cases.json`。
