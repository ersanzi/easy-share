# 2026-07-25 Cloudreve 对标研究与网盘在线预览

## 用户问题

EasyShare 已具备 RustFS 上传、列表、下载、删除和分享，但与成熟网盘相比仍缺少预览、断点续传、回收站、缩略图和统一任务模型。用户要求继续对标 Cloudreve，分析差距、迁移设计思想并实现高价值完善项。

## 目标与非目标

### 目标

- 固定 Cloudreve 研究基线并形成可追溯能力矩阵。
- 迁移“后端声明文件能力、内容访问与元数据访问解耦”的设计。
- 在 EasyShare 中支持图片、PDF、限量 UTF-8 文本在线预览。
- 不向 WebView 暴露长期 API Token，不把 SVG 等主动内容当普通图片内嵌。
- 补齐后端、API、Wails 绑定和前端测试。

### 非目标

- 不实现 Office、音视频、ePub 或压缩包高级预览。
- 不实现缩略图、转码服务或全文索引。
- 不在本次实现大文件分片与断点续传。
- 不照搬 Cloudreve 的多用户、用户组、配额、管理员后台和公网部署架构。

## Cloudreve 研究证据

基线：`0bb0ab833571d380153edd3529e01a7957b8b4ce`，提交日期 2026-07-15。

重点阅读：

- `README_zh-CN.md`
- `application/dependency/dependency.go`
- `service/explorer/viewer.go`
- `pkg/filemanager/manager/viewer.go`
- `pkg/filemanager/manager/thumbnail.go`
- `pkg/filemanager/manager/recycle.go`
- `pkg/filemanager/workflows/upload.go`
- `pkg/queue/`
- `service/share/`

研究结论和能力矩阵见 [`../cloudreve-benchmark.md`](../cloudreve-benchmark.md)。

## 技术决策

### 1. 预览类型由后端声明

新增 `PreviewKind`：`unsupported`、`image`、`pdf`、`text`。云盘列表的 `cloud.File` 返回 `previewKind`，前端仅在后端声明可预览时显示按钮，不自行维护扩展名规则。

列表接口不为每个对象额外执行 `HeadObject`，只根据列表元数据和扩展名提供轻量提示；真正打开预览时再执行 `HeadObject` 校正 `Content-Type`、大小和能力。

### 2. 元数据端点与内容端点分离

- `GET /api/cloud/preview?key=...`：需要 Bearer API Token，返回预览描述。
- `GET /api/cloud/preview/content?key=...&expires=...&signature=...`：只接受短期 HMAC 票据，流式返回图片/PDF。

WebView 的 `<img>`/`<iframe>` 资源请求无法通过 Wails 绑定附加 Bearer Header，因此不能复用长期 Token。内容票据五分钟过期，签名绑定对象 key 和过期时间，并限制可接受的未来时间窗口。

### 3. 主动内容和文本边界

- SVG 即使 MIME 为 `image/svg+xml` 也返回 `unsupported`。
- 文本最多读取前 1 MiB，超限返回 `truncated`。
- 文本必须是 UTF-8；截断点若落在多字节字符中间，只回退不完整尾部。
- 前端使用 `<pre>{{ preview.text }}</pre>`，不使用 `v-html`。

### 4. MIME 采用多级回退

优先使用 `HeadObject.ContentType`，其次使用 `mime.TypeByExtension`，最后使用内置扩展名表。内置回退避免 Windows MIME 注册表缺失或被第三方软件改写时行为不一致。

## API 与代码影响

### 后端

- `internal/cloud/preview.go`：预览能力识别、文本限量读取、流式对象打开。
- `internal/cloud/service.go`：`cloud.File` 增加 `previewKind`。
- `internal/api/cloud_preview.go`：预览描述、内容票据和安全响应头。
- `internal/api/server.go`：注册预览路由。

### Desktop/Wails

- `internal/desktop/client.go`：请求预览描述并补全 Core 相对内容 URL。
- `app.go`：导出 `CloudPreview`。
- `frontend/wailsjs/go/`：重新生成 TypeScript 绑定。

### 前端

- `frontend/src/types/core.ts`：增加 `CloudPreview` 和 `PreviewKind`。
- `frontend/src/services/core.ts`：封装 `cloudPreview`。
- `frontend/src/components/CloudPreview.vue`：图片/PDF/文本预览模态框。
- `frontend/src/components/CloudPanel.vue`：按能力显示预览入口。
- `frontend/src/style.css`：毛玻璃、圆角预览界面和滚动布局。

## 安全边界

- Core API 与内容端点仍只监听回环地址。
- 长期 API Token 不写入内容 URL。
- 票据使用 HMAC-SHA256，过期或篡改返回 401。
- 内容响应设置 `Content-Disposition: inline`、`X-Content-Type-Options: nosniff` 和私有短缓存。
- 不返回对象存储凭据或预签名管理权限。
- SVG、非 UTF-8 文本和未知二进制内容不内嵌。

## 自动化验证

已按项目规定顺序完成完整流水线，全部通过：

```powershell
go build ./...
go test ./...
npm --prefix frontend run build
npm --prefix frontend test
wails build
go build -o build/bin/easyshare-core.exe ./cmd/core
```

- Go 编译与测试通过。
- 前端生产构建通过，包含 `vue-tsc` 类型检查。
- 前端测试共 4 个测试文件、10 个测试通过。
- `wails build` 成功生成 `build/bin/easyshare.exe`；`KnownStructs ... Not found: time.Time` 为项目已有的非致命警告。
- Core 独立构建成功生成 `build/bin/easyshare-core.exe`。
- 组件测试覆盖文本 HTML 转义、截断提示、图片/PDF 渲染器选择，以及关闭按钮、背景点击和 Escape 关闭行为。

## 手工与界面验收

已完成代码与样式静态验收：

- 图片使用 `object-fit: contain`，保持比例并限制在对话框内。
- 文本内容区域可滚动，截断提示有独立状态。
- PDF iframe 填满可用内容区域。
- 模态框限制最大宽高，并在 720px 以下切换为小窗口响应式布局。
- 关闭按钮、背景点击和 Escape 关闭行为由组件测试验证。

当前环境没有可用的应用内浏览器控制工具，以下真实集成场景尚未手工验收，不以自动化结果代替：

- 使用真实 RustFS 对象打开图片、PDF 和文本预览。
- 在 Wails WebView2 中确认真实 PDF/图片内容的显示效果。
- 等待内容票据过期后，从界面重新打开并确认获得新票据。

## 排障方法

1. **列表显示可预览但打开失败**：S3 `ListObjects` 不保证返回可靠 `Content-Type`；检查 `HeadObject` 的实际元数据和 `core.log`，不要让列表结论覆盖真实对象元数据。
2. **图片/PDF 请求无法认证**：WebView 资源请求不能附 Wails 调用使用的 Bearer Header。必须重新请求短期 HMAC 内容票据，不能把长期 API Token 拼进 URL。
3. **SVG 返回不支持**：这是主动内容安全边界，不是 MIME 判断缺陷。下载后由系统应用打开。
4. **文本过大或乱码**：只读取前 1 MiB 且只接受 UTF-8；非 UTF-8 文件降级为不支持，不做有损猜测转码。
5. **相同扩展名在不同 Windows 机器判断不同**：`mime.TypeByExtension` 可能受注册表影响，必须保留内置扩展名回退表，并以 `HeadObject` 为最终校正。
6. **前端找不到 `CloudPreview` 或新字段**：Go 导出方法/结构体变化后执行 `wails generate module` 或 `wails build`，并同步 `types/core.ts` 与 `services/core.ts`。
7. **票据返回 401**：链接已过期、参数被修改或 Core Token 已轮换。关闭后重新打开预览以获取新票据，不延长旧链接。
8. **PDF 空白**：先直接请求内容 URL检查状态码、`Content-Type: application/pdf`、`Content-Length` 和票据有效期，再确认 WebView/PDF 查看器能力；不要放宽长期认证。

## 回滚方式

- 前端可先移除 `CloudPanel` 的预览按钮和 `CloudPreview` 组件，不影响上传/下载。
- 后端可移除两个预览路由及 `CloudPreview` Wails 导出；`previewKind` 是附加 JSON 字段，旧前端可忽略。
- 无数据库迁移和持久化格式变更。

## 后续工作

1. P0：S3 Multipart Upload、可恢复 Upload Session、失败重试与故障注入测试。
2. P1：回收站/软删除与恢复。
3. P1：按 ETag/版本缓存的缩略图能力。
4. P1：统一上传、下载、同步、LAN 直传和后台处理任务模型。
5. P2：文件搜索、标签、媒体元数据以及分享记录/撤销/过期管理。
