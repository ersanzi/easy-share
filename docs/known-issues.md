# EasyShare 已知缺陷清单

> 登记已确认、尚未修复的实现缺陷。**只登记不复制**：细节和修复决策仍在各自的迭代记录、ADR 或代码注释中。
> 与相邻文档的分工：产品级能力边界见 [`../README.md`](../README.md) 的「当前限制」；阻塞下一步推进的事项见 [`progress.md`](progress.md) 的「已知阻塞」；可复现的环境故障与解法见 [`troubleshooting.md`](troubleshooting.md)。
>
> 最后更新：2026-10-16

## 登记表

| 编号 | 严重度 | 标题 | 影响面 | 状态 |
| --- | --- | --- | --- | --- |
| [KI-1](#ki-1cloudenabled-不反映存储可达性) | 中 | `cloudEnabled` 不反映存储可达性 | 前端状态展示 | 已修复（[P2](iterations/2026-08-29-account-p2-storage-isolation.md)） |
| [KI-2](#ki-2云端凭据编译期硬编码且可从二进制明文提取) | 高 | 云端凭据编译期硬编码，可从二进制明文提取 | 服务器化部署的前置阻塞 | 已修复（[P2](iterations/2026-08-29-account-p2-storage-isolation.md)） |
| [KI-3](#ki-3对象-key-直接使用文件名无身份与用户隔离) | 高 | 对象 key 直接使用文件名，无身份与用户隔离 | 多用户与文件版本的前置阻塞 | 部分修复（[P2](iterations/2026-08-29-account-p2-storage-isolation.md) 做完用户隔离；稳定文件身份仍未做） |
| [KI-4](#ki-4预览测试依赖本机注册表的-mime-映射) | 中 | 预览测试依赖本机注册表的 MIME 映射 | 阻断本机完整构建流水线 | 已修复（[P2](iterations/2026-08-29-account-p2-storage-isolation.md)，取修复方向第 2 条） |
| [KI-5](#ki-5core-侧云盘路由已无调用方但仍可绕过用户隔离) | 中 | Core 侧云盘路由已无调用方，但仍可绕过用户隔离 | 隔离边界的回归风险 | 已修复（[批次收尾](iterations/2026-08-31-account-plane-closure.md)） |
| [KI-6](#ki-6llm-供应商限流时-query-整体-500不降级纯检索) | 中 | LLM 供应商限流时 `/query` 整体 500，不降级纯检索 | 公司使用高峰的问答可用性 | 未修复 |
| [KI-7](#ki-7彻底删除回收站旧行会误删同路径的当前文件) | **高** | 彻底删除回收站旧行会**误删同路径的当前文件** | **数据丢失**，回收站功能（在制品） | **已修复**（2026-10-16） |
| [KI-8](#ki-8回收站还原的测试从未真正运行且现在失败) | **高** | 回收站还原的测试**从未真正运行**，且现在失败 | **Java 测试全链路是假的**，在制品验收结论无依据 | **已修复**（2026-10-16） |
| [KI-9](#ki-9此电脑云盘无法下载任何文件webdav-get-一律-http-500) | **高** | 「此电脑」云盘**无法下载任何文件**（GET 一律 500） | **核心功能不可用**：双击任何云盘文件都打不开 | **已修复**（2026-10-16） |
| [KI-10](#ki-10删除后文件仍在列表里且回收站永远无法还原) | **高** | 删除后文件**仍在列表里**，且回收站**永远无法还原** | **回收站整条链路失效**（在制品） | **已修复**（2026-10-16） |
| [KI-11](#ki-11受限文件看不见但拿得到预签名下载不查可见性) | **高** | 受限文件「看不见但拿得到」：预签名下载**不查可见性** | **权限绕过**：部门可见性对下载无效 | **已修复**（2026-10-16） |
| [KI-12](#ki-12资源管理器改名会换掉-fileid-并留下一份回收站副本) | **高** | 资源管理器改名会**换掉 fileId** 并留下回收站副本 | **引用失效 + 空间泄漏**（软删引入的回归） | **已修复**（2026-10-16） |
| [KI-13](#ki-13拖大文件进资源管理器会让-core-吃掉等量内存) | **高** | 拖大文件进资源管理器会让 Core **吃掉等量内存** | **OOM 崩溃**：5 GB 文件 = 5 GB 分配 | **已修复**（2026-10-16） |
| [KI-14](#ki-14只读占位符实际可写用户改动会静默丢失) | **高** | 「只读」占位符**实际可写**，用户改动会静默丢失 | **数据丢失**：编辑保存后下次同步被覆盖 | **已修复**（2026-10-16） |
| [KI-15](#ki-15重转写偶发失败的真因是并发写与异常被吞) | **中** | 重转写偶发失败**真因是并发写 + 异常被吞**（初次误判为机器负载 flake） | **记录永久停在 processing**（用户看到永不结束的「正在处理」） | **已修复**（2026-10-16） |
| [KI-16](#ki-16云端删除的文件永久留在本地幽灵占位符) | **中** | 云端删除的文件**永久留在本地**（幽灵占位符） | 用户体验（每次 Sync 都不清理，且点开报错） | **已修复**（2026-10-16） |
| [KI-17](#ki-17sysuserdept-缺-tableid导致按-id-查询整体-500) | **高** | `SysUserDept` 缺 `@TableId`，按 ID 查询**整体 500** | **下载与空间接口全挂**（单元测试永远测不到） | **已修复**（2026-10-16） |
| [KI-18](#ki-18控制面把雪花-id-序列化成字符串go-侧解析整个响应失败) | **高** | 控制面把雪花 ID **序列化成字符串**，Go 侧解析**整个响应失败** | **全部云盘读路径**：列表 / 挂载盘 / 占位符 / 快搜 / 回收站 | **已修复**（2026-10-16） |
| [KI-19](#ki-19健康探测的-300ms-预算把慢读成没有-core会拉起重复进程) | **高** | 健康探测的 300ms 预算把「慢」读成「没有 Core」，**会拉起重复进程** | **启动/watchdog 误拉重复 Core**，再以误导性的「端口冲突」收场 | **已修复**（2026-10-16） |
| [KI-20](#ki-20multipart-上传绕过了-cas且它的雪花-id-也没跟上-ki-18-的修复) | **高** | Multipart 上传**绕过 CAS**，且分片路径的雪花 ID 漏了 KI-18 的修复 | **≥32 MiB 文件**：既不防覆盖，又从来没跑通过真实控制面 | **已修复**（2026-10-16） |
| [KI-21](#ki-21雪花-id-跨-wails-边界变回-json-number被-js-静默截断) | **高** | 雪花 ID 跨 Wails 边界变回 JSON number，被 JS **静默截断** | **回收站还原/彻底删除对任何真实文件都失败**（13/13 真实 ID 全被改变） | **已修复**（2026-10-16） |
| [KI-22](#ki-22setfilevisibility-用-int64-收部门-id多个部门会塌成一个) | **中** | `SetFileVisibility` 用 `[]int64` 收部门 ID，多个部门会**塌成一个** | **潜在越权**：多部门可见性静默变成另一个部门（**当前无调用方**，预防性修复） | **已修复**（2026-10-16） |
| [KI-23](#ki-23分片上传的挂死连接永远不失败重试逻辑因此形同虚设) | **高** | 分片上传的挂死连接**永远不失败**，重试逻辑**形同虚设** | **≥32 MiB 上传永久卡住**（进度条不动，实测 6 次挂 2 次） | **已修复**（2026-10-16） |
| [KI-24](#ki-24单请求上传-32-mib既无停滞检测也无重试且重试方式本身有个坑) | **高** | 单请求上传（< 32 MiB）**既无停滞检测也无重试**，且重试方式本身有坑 | **< 32 MiB 上传挂死无自愈**（8 次里 1 次）；重签会虚增版本破坏 CAS | **已修复**（2026-10-16） |
| [KI-25](#ki-25活栈用例的清理写在函数末尾测试一失败就清不掉) | **中** | 活栈用例的清理写在函数末尾，**测试一失败就清不掉** | 本机累积 30 个孤儿对象 + 32 行测试残留（用户可见的回收站垃圾） | **已修复**（2026-10-16） |
| [KI-26](#ki-26cas-拒绝时会话不会被中止永久停在-uploading) | **中** | CAS 拒绝时**会话不会被中止**，永久停在 `uploading` | S3 分片白占容量 + 库里累积永不结束的会话（**每跑必增**） | **已修复**（2026-10-16） |
| [KI-27](#ki-27活栈清理在跨包并发下会偶发漏清测试卫生非产品缺陷) | **低** | 活栈清理在**跨包并发**下会偶发漏清 | 测试卫生（**已排除产品缺陷**） | **已定位并记录**（2026-10-16） |

严重度口径：**高** = 阻塞既定路线的下一阶段；**中** = 影响可用性或可诊断性，但不阻塞开发；**低** = 体验瑕疵。

---

## KI-1：`cloudEnabled` 不反映存储可达性

**现象**　RustFS 未启动时，`GET /api/status` 仍返回 `"cloudEnabled": true`，前端「文件」页据此显示「云端文件存储」为已连接；但任何实际操作立即失败，`GET /api/cloud/files` 返回 `502 cloud_list_failed`。用户看到的是「显示正常但一用就报错」。

**根因**　该字段只表示「云盘 Service 对象是否被构造过」，与后端可达性无关：

- `internal/api/server.go:198` —— `server.status.CloudEnabled = service != nil`
- `cmd/core/main.go:64-80` —— `s3store.New` 只构造 AWS SDK 客户端，不发起任何网络请求，因此 RustFS 未运行时它同样返回成功，`ConfigureCloud` 照常被调用。

**影响**　误导性状态展示，且违反 [`client-workbench.md`](client-workbench.md) 第 4.5 条「能力由后端声明，前端不猜测」——前端拿到的 capability 与真实能力不一致。排障时也会把注意力从存储层引开。

**修复方向**　Core 侧对存储做一次轻量探活（如 `HeadBucket`）并把结果并入状态；状态应区分「未配置」与「已配置但不可达」，而不是单一布尔。探活需带超时且不可阻塞 Core 启动。

**已修复（P2）**　修法与上述方向不同，因为前提变了：P2 之后 Core 不再直连对象存储，探活无从做起。个人云盘归控制面管，故该字段改由桌面端在 `app.go` 的 `GetSnapshot` 里按「配了 `PlatformBaseURL` + 已登录且持有 token」覆盖（`cloudAvailable()`）。`api.Status.CloudEnabled` 的注释已注明该字段由桌面端覆盖。仍是单一布尔——「已配置但控制面不可达」尚未区分，留待需要时细化。

**关联**　[`architecture.md`](architecture.md) 第 8 节；[ADR-0006](adr/0006-rustfs-self-hosted-object-storage.md)；[P2 迭代记录](iterations/2026-08-29-account-p2-storage-isolation.md)。

---

## KI-2：云端凭据编译期硬编码，且可从二进制明文提取

**现象**　对象存储的 endpoint、AccessKey、SecretKey、bucket 全部是编译期常量，且以明文形式留在发布产物中。对 `build/bin/easyshare-core.exe` 执行一次字符串搜索即可原样得到三项凭据。

**根因**

- `internal/cloud/defaults.go:6-13` —— 六个常量，含 `DefaultAccessKeyID`、`DefaultSecretAccessKey`，并且 `DefaultAllowInsecureHTTP = true`
- `cmd/core/main.go:64-70` —— 直接引用上述常量构造 `s3store`，不读取 `config.json` 的 `cloud` 段（该段实际已废弃，仍留在配置文件中）

**影响**　当前 endpoint 是 `http://127.0.0.1:9000`，攻击面仅限本机，因此**现阶段不构成实际风险**。但一旦 endpoint 指向可路由的服务器，性质改变：任何持有客户端二进制的人都获得该 bucket 的完全读写删除权限；同时 `AllowInsecureHTTP` 使凭据与文件内容在公网明文传输。这是「把 RustFS 搬到服务器」这条路线的**硬阻塞**。

此外，凭据分散在三处且必须保持一致（改动时需同步且重新编译 Core）：`internal/cloud/defaults.go`、`deploy/rustfs/.env`、`scripts/create_bucket.go:17-23`。

**修复方向**　客户端不得持有长期存储凭据。由控制面在身份校验后签发短期预签名 URL 或临时凭据，客户端只拿到有效期受限的授权。这与 [`client-workbench.md`](client-workbench.md) 第 13.3 节「UI 不持有云端凭据、不直接调用 RustFS」一致，也是 [ADR-0006](adr/0006-rustfs-self-hosted-object-storage.md) 生产门禁第 7 条（TLS、最小权限凭据、密钥轮换）的前提。

**注意**　这不是配置问题，属于需要单独 ADR 的架构决策，不应通过「换一组更长的常量」绕过。

**已修复（P2）**　按上述方向落地，决策见 [ADR-0007](adr/0007-account-control-plane-ruoyi.md)：

- `internal/cloud/defaults.go` **已删除**；`cmd/core/main.go` 不再构造 `s3store`，Core 不持有任何 RustFS 凭据。
- 桌面端经 `internal/drive` 向控制面换取短期预签名 URL 再直传/直取，见 [P2 迭代记录](iterations/2026-08-29-account-p2-storage-isolation.md)。
- 凭据真值只在被 gitignore 的 `deploy/rustfs/.env`，控制面启动时 source 进进程环境。

验收证据——用 `.env` 里的真实凭据对产物做字节搜索，两个二进制均不含：

```text
easyshare.exe   11.5MB   含 AccessKey=False   含 SecretKey=False
core.exe        10.6MB   含 AccessKey=False   含 SecretKey=False
```

原文「凭据分散在三处且必须保持一致」的约束**已作废**：`defaults.go` 已删，`scripts/create_bucket.go` 改为从环境变量读并在缺失时退出，唯一来源是 `deploy/rustfs/.env`。

---

## KI-3：对象 key 直接使用文件名，无身份与用户隔离

**现象**　上传对象的 key 就是经过清洗的原始文件名，不含任何用户、目录或版本前缀。两个用户上传同名文件会互相覆盖，且所有对象处于同一平铺命名空间。

**根因**

- `internal/cloud/service.go:55` —— `key := sanitizeKey(filepath.Base(localPath))`
- `internal/cloud/service.go:78` —— `key := sanitizeKey(name)`
- `internal/cloud/service.go:152-160` —— `sanitizeKey` 只做去空白、反斜杠归一和 `..` 剔除，不添加任何前缀

文件夹上传通过 `X-Object-Key` 头传入含相对路径的 key（`internal/api/server.go:536-538`），仍然落在同一命名空间内。

**影响**　直接违反 [ADR-0006](adr/0006-rustfs-self-hosted-object-storage.md) 不变量 3「RustFS 中的对象 key 不包含用户名、文件名或用户路径」。后果是重命名会使分享链接、任务引用和后续知识索引全部失效，也无法承载文件版本。这是多用户与 C2「稳定文件身份」切片的**硬阻塞**。

**修复方向**　按 [`client-workbench.md`](client-workbench.md) 第 4.4 节，业务身份改用稳定的 `fileId + versionId`，object key 由身份派生而非由文件名派生；文件名退回为展示属性，保存在元数据而非 key 中。

**部分修复（P2）**　「无用户隔离」这一半已解决，「由文件名派生」这一半仍在：

- 已做：对象键落在 `users/{userId}/` 命名空间下，前缀由控制面依 `LoginHelper.getUserId()` 强制，客户端只认相对路径、无法指定用户。跨用户隔离已可复现验收（`scripts/verify-drive-isolation.sh` 9/9，含三种路径穿越拒绝）。同名文件不再互相覆盖——不同用户落在不同前缀下。
- 仍未做：前缀之后的部分依然是文件名（或「文件夹名/相对路径」），因此**重命名仍会使分享链接与任务引用失效，仍无法承载文件版本**。稳定文件身份 `fileId + versionId` 属 C2 切片，未随 P2 落地。

因此本条降为「多用户」不再阻塞、「文件版本」仍阻塞。

**关联**　[`client-workbench.md`](client-workbench.md) 第 14.1 节 `FileItem` 契约；[`progress.md`](progress.md) 待开始项；[P2 迭代记录](iterations/2026-08-29-account-p2-storage-isolation.md)。

---

## KI-4：预览测试依赖本机注册表的 MIME 映射

**现象**　`go test ./internal/cloud` 的 `TestPreviewInfoClassifiesAndReadsText` 在部分开发机上必然失败：

```text
--- FAIL: TestPreviewInfoClassifiesAndReadsText
    preview_test.go:31: unexpected preview: {... ContentType:text/markdown ...}
```

该用例断言 `notes/readme.md` 的 `ContentType` 为 `text/plain`，实际得到 `text/markdown`。失败与代码改动无关——在未改动的基线副本上同样复现。

**根因**　`internal/cloud/service.go:162-172` 的 `detectContentType` 使用标准库 `mime.TypeByExtension`。该函数在 Windows 上会查询注册表，`.md` 不在 Go 的内置类型表中，因此结果取决于本机 `HKEY_CLASSES_ROOT\.md` 的 `Content Type` 值。装过 Markdown 编辑器的机器上该键被写为 `text/markdown`，未装过的机器上取不到值而回落，测试因此表现不一致。

复现与确认：

```powershell
# 本机实际映射
Get-ItemProperty 'HKCR:\.md' | Select-Object -ExpandProperty 'Content Type'
```

**影响**　测试结果依赖开发机的软件安装历史，不可重现。`scripts/build.ps1` 第一步即 `go test`，因此在受影响的机器上**整条完整构建流水线被阻断**，进而挡住迭代交付前的验收。CI 环境（干净镜像）不受影响，问题只在本地暴露，容易被误判为「本次改动引入的回归」。

**修复方向**　两条路，取其一：

1. 预览分类不依赖系统 MIME 数据库，改用项目自有的扩展名到类型映射表。预览能力本就只支持有限格式（图片、PDF、≤1 MiB UTF-8 文本），固定表反而更贴合语义，也让行为在所有平台一致。
2. 若保留 `mime.TypeByExtension`，则测试改为断言分类结果（`Kind == PreviewText`）而非具体 `ContentType` 字符串，避免把环境相关的值写进断言。

倾向第 1 条：`detectContentType` 的结果会随宿主机变化，本身就不适合作为业务判定依据。

**已修复（P2）**　取的是第 2 条而非倾向的第 1 条：断言改为 `Kind == PreviewText` + 内容完整 + `ContentType` 以 `text/` 开头，不再钉死具体字符串。选第 2 条的理由是它只动测试、不改产品行为——而第 1 条会改变所有文件的 MIME 判定结果，属于需要单独权衡的行为变更，不该搭在存储隔离这一轮里。第 1 条仍是更彻底的方向，保留待后续。

本机实测映射为 `text/markdown`（装过 Markdown 编辑器），且**在未改动的基线副本 `easy-share-不可动，当做比较` 上同样复现**，确认与改动无关。

**关联**　`internal/cloud/preview_test.go:30-37`；[`troubleshooting.md`](troubleshooting.md)（本地构建失败的排查入口）；[P2 迭代记录](iterations/2026-08-29-account-p2-storage-isolation.md)。

---

## KI-5：Core 侧云盘路由已无调用方，但仍可绕过用户隔离

**现象**　P2 把桌面端云盘全部改走控制面后，Core 的 `/api/cloud/*` 七个路由与其背后的 `cloud.Service` 已无生产调用方，但仍注册在 mux 上、仍在编译产物里。这些处理函数**只按对象键鉴权，不认用户身份**——若谁重新调用 `ConfigureCloud` 把它们接上，就会绕过 `users/{userId}/` 命名空间边界。

**根因**

- `internal/api/server.go:116-122` —— 七个 `/api/cloud/*` 路由照旧注册
- `internal/api/server.go:198` —— `ConfigureCloud` 仍存在；生产路径已不调用，当前只有 `internal/api/cloud_preview_test.go:113` 在调
- `internal/api/cloud_preview.go:52-62` —— `/api/cloud/preview/content` 不经 `server.auth`，靠 HMAC 票据（5 分钟 TTL）。票据只绑对象键与过期时间，**不绑用户**——单用户 Core 时代的设计
- 连带死代码：`internal/cloud/service.go`、`internal/cloud/webdavfs/`、`internal/drive/cloud_service.go`

**影响**　当前**不构成实际风险**：`server.cloud` 为 nil，七个路由一律返回 `503 cloud_disabled`，且 Core 已无凭据可用。风险是回归性的——这些代码看起来是可用的云盘实现，后来者容易误接，而误接的后果是静默绕过隔离（不会报错，只会看到别人的文件）。

**修复方向**　删除而非留存：摘掉七个路由注册、删 `cloud.Service` 与 `ConfigureCloud`、删 `webdavfs` 与 `drive/cloud_service.go`。需保留的是仍在服役的部分——`cloud.File`、`cloud.Preview`、`DetectPreviewKind`、`ContentTypeForKey`、`FillTextPreview`，以及 `objectstore` 抽象（`s3store`/`memory` 对控制面之外的用途仍有价值）。约 900 行的删除面，且要一并处理 `cloud_preview_test.go`，故未搭在 P2 里做。

若 P4 的按用户挂载决定复用 `webdavfs`，则该文件不删、改为接 `internal/drive` 客户端（只需 `Store` 十个方法中的五个）。两条路互斥，先定 P4 方案再动手。

**已修复（[批次收尾迭代](iterations/2026-08-31-account-plane-closure.md)，2026-08-31）** P4 已随外部批次落地为 `internal/spacedav`（建在 `internal/drive` 控制面客户端之上，与 `webdavfs` 无关），删除分支生效，净删约 1390 行：

- `internal/api`：七条 `/api/cloud/*` 路由、`ConfigureCloud`/`ConfigureCloudDrive`/`StartCloudDrive`/`StopCloudDrive`、五个 handler 与 `cloud_preview.go`（HMAC 内容票据）及其测试；
- `internal/cloud`：`service.go`（`cloud.Service`）与 `webdavfs/` 整包删除；`File` 结构体迁入 `preview.go`，预览辅助（`DetectPreviewKind`/`ContentTypeForKey`/`FillTextPreview`）与 `objectstore` 保留；
- `internal/drive/cloud_service.go` 与 `internal/desktop/client.go` 的 Cloud* 客户端方法（调用已删路由，均无调用方）。

删除后不存在任何"不经控制面"的云盘路径，隔离边界只能由控制面裁决。验收：`go build ./...`/`go vet`/`go test ./...` 18 包全绿，`vue-tsc`/`vitest` 33 条通过，`wails build` + Core 重编通过。

**关联**　[P2 迭代记录](iterations/2026-08-29-account-p2-storage-isolation.md)「已知限制」；[ADR-0007](adr/0007-account-control-plane-ruoyi.md) 不变量 1、2。

---

## KI-6：LLM 供应商限流时 `/query` 整体 500，不降级纯检索

**现象**　LLM 供应商返回 429（如 SenseNova `inference tpm exhausted`）时，`POST /query` 整体返回 500——检索阶段已成功完成（contexts 已构建），挂在生成阶段 `services.generator.generate()` 异常上抛，前端拿到的是报错而非降级答案。2026-09-01 权限切片冒烟时真实复现一次。

**根因**　`app/api/routes.py` 的 `/query`：`generator is None` 时有降级（返回纯检索片段），但 generator 已配置而**运行时调用失败**（限流/网络/超时）没有降级路径——异常直接冒泡成 500。

**影响**　公司多人使用高峰 TPM 用尽时，问答服务整体不可用，且用户看到的错误不可理解。Embedding 侧已有同类降级先例（BM25 fallback，1.9 切片），生成侧缺失。属于可用性问题，不阻塞功能路线，严重度中。

**修复方向**　生成失败时捕获并降级：返回 `（LLM 暂不可用，以下为检索到的相关片段）` + contexts（复用 `generator is None` 的响应形态），日志记录异常详情。可选增强：连续失败次数进 `/health`。

**关联**　[权限迭代记录](iterations/2026-09-01-permission-aware-retrieval.md)「已知问题」；[`troubleshooting.md`](troubleshooting.md) Python 文档处理任务异常节（现场处置）。

---

## 登记约定

1. 只登记**已确认**的缺陷，附可复现证据（`文件:行号` 或复现命令）。推测和待验证问题不进入本表。
2. 每条包含：现象、根因（带证据）、影响、修复方向。**不在此处写实现方案**——方案属于迭代记录或 ADR。
3. 修复后把状态改为「已修复」并注明修复所在的迭代记录，保留条目一个版本周期后再移除，便于回溯。
4. 若某条升级为需要跨版本权衡的架构决策，在 `adr/` 建立对应 ADR，本表只保留指向该 ADR 的链接。

---

## KI-7：彻底删除回收站旧行会误删同路径的当前文件

**状态**　**已修复（2026-10-16）**。发现于静态审查（读在制品「回收站+版本」代码时发现）。

**证据强度**：三条代码事实（下方「根因」）+ **一条可执行的对照测试**
`DriveFileServiceTest.purgeLacksTheOccupancyCheckThatRestoreHas`——
它在**同一前提下**断言 restore 抛冲突而 purge 照常返回，把两者的不对称钉住：

```
cd platform-drive && mvn -o -q test -Dtest=DriveFileServiceTest   # 通过
```

即：**「还原有占用检查、彻底删除没有」这条不对称已被测试固化**（不是推断）。
测试同时充当**修复后的验收点**——补上检查后，该用例应改为断言 purge 抛冲突。
**但真实的数据丢失仍无端到端复现**（需要控制器 + Storage + 真实对象存储），
所以「用户会在生产上丢数据」这半句属于**由代码推演**，未经观察。

**现象**　下面这串操作会让**用户在用的文件被删掉**，且删的是「已经删过的旧文件」时发生的：

| 步骤 | 用户操作 | `es_file` | RustFS 对象 `users/1/a.txt` |
| --- | --- | --- | --- |
| t0 | 上传 `a.txt` | `{fileId=1, path='a.txt', state=active}` | 内容 A（**只有这一份**） |
| t1 | 删除 `a.txt` | `{fileId=1, path='a.txt', state=recycled, originPath='a.txt'}` | 内容 A（软删不动对象） |
| t2 | **重新上传** `a.txt`（内容 B） | 新建 `{fileId=2, path='a.txt', state=active}` | **内容 B（覆盖了同一个键）** |
| t3 | 在回收站对 `fileId=1` 点「彻底删除」 | 行 1 被摘除（正确） | **键被删 → 内容 B 没了** |

t3 之后：`es_file` 里 `fileId=2` 仍是 `active`，但**对象已不存在**。
列表以存储为准，于是它从列表里消失——用户看到「我明明刚传的文件不见了」。

**根因**　三条实现事实叠加：

1. **对象键由路径派生、不含 fileId**：`DriveKeys.keyAt(prefix, relative) = prefix + relative`
   （`DriveKeys.java:55-57`），且存储路径对 fileId 一无所知。
2. **软删不搬对象**（设计如此，见 `easyshare-file-recycle.sql` 注释「还原时同键复位，无需搬对象」），
   于是同一路径的历史内容与新内容**共用同一个键**，新上传直接覆盖旧内容。
3. **purge 按 `row.getFilePath()` 删对象，且不检查该路径是否还有 active 行**：
   `DriveController.java` 的 `recyclePurge` 里 `storage.deleteAt(prefix, row.getFilePath())`
   是全路径上唯一的删除动作，之前没有任何占用检查。

第 3 条是**触发条件**，第 1、2 条是**使它能致命的前提**。
换句话说：软删的设计在「路径 = 对象键」这个前提下**本身就不闭合**。

**影响**　**静默数据丢失**。严重度评「高」而非「中」，因为：
- 丢失的是**用户当前在用的数据**，不是回收站里的旧数据；
- 用户不会把「删除回收站里的旧文件」和「当前文件消失」联系起来，**归因困难**；
- 触发路径是回收站功能的**正常操作**（彻底删除），不需要任何异常条件。

**已采用的修复**（方向 1）

`DriveFileService.purgeFromRecycle` 现在返回 `PurgeResult(row, deletable)`；
`DriveController.recyclePurge` 据此决定要不要 `storage.deleteAt`。
**行照常摘除（用户意图达成），对象只在安全时删**——这是唯一既不丢数据又不拦用户的选项。

**⚠️ 判据修正：第一版修复是错的（只查了 active）**

初版查的是「同路径还有没有 **active** 行」。但唯一索引是**部分索引**
（`WHERE state='active'`），所以「两条 **recycled** 行同路径」是**合法状态**：

```
传 a.txt → 删（行1 recycled）→ 重传（行2 active）→ 再删（行2 也 recycled）
此刻两条行同路径、都是 recycled
  → 删行1 时只查 active ⇒ 查不到 ⇒ 误判"路径空闲" ⇒ 删掉对象
  → 行2 还指着那个键，用户对它点「还原」得到一个对象已不存在的幽灵文件
```

**正确判据**：查「同路径、排除自己的**全部行**」（不论状态）。
改用 `fileMapper.exists(...)` 一次查询覆盖所有状态——将来新增状态也自动覆盖。

回归测试三条（`DriveFileServiceTest`）：

| 用例 | 场景 | 期望 |
| --- | --- | --- |
| `purgeSkipsObjectDeletionWhenPathIsReoccupied` | 同路径有 active 行 | `deletable=false` |
| `purgeRefusesDeleteWhenAnotherRecycledRowSharesPath` | 同路径有**另一条 recycled 行** | `deletable=false` |
| `purgeAllowsObjectDeletionWhenPathIsFree` | 该路径确无别的行 | `deletable=true`（否则回收站永不释放空间） |

**后两条都验证过「能失败」**：把检查退回 active-only，两条用例同时报
`expected: <false> but was: <true>`。

**语义本质**：这是**引用计数**。对象键由路径派生，所以「键归谁」= 「还有多少
`es_file` 行指着这个路径」。检查写成 `ne(fileId, 自己)`，即「我走了之后还有人吗」：

- 还有 → 键不释放（**不误删别人正在用的数据**）
- 没有了 → 键释放（**不泄漏空间**）

所以两次 purge 的行为是：第一条不删对象、第二条（最后一条引用）才删。
`purgeReleasesObjectWhenLastReferenceGoes` 把这两步都钉住了——
只测「不许删」会漏掉泄漏，必须两侧都测。

**以下是最初记录的三个候选方向，供后续根治参考**

1. **purge 前检查同路径 active 行**（最小改动，**已验证可行**）：若存在同路径
   active 行，就**不删对象**，只摘行——因为那个键现在归新文件所有。
   旧内容在 t2 覆盖时就已经丢了（见下方「仍未解决」），但至少不再误删新数据。

   > 可行性已核对：`DriveFileService.selectActiveByPath(spaceType, ownerId, path)`
   > **已经存在**（`restoreFromRecycle` 正在用它做占用检查），而
   > `purgeFromRecycle` 里**没有调用它**。即缺的是一个已有方法的调用，
   > 不是新机制。修法可以完全对称于 restore。
   >
   > 注意：这个改动落在**在制品**上，需与「回收站+版本」那一片一起提交，
   > 不要在它未收尾时单独改（避免同文件多处改动互相覆盖）。
2. **对象键改用 fileId**（根治）：`users/1/{fileId}` 而非 `users/1/{path}`。
   这样历史版本与当前版本天然不共享键，回收站与版本语义才真正成立。
   代价是要动所有读写路径与存量数据迁移，与路线图**切片 1「写路径改走 file_id」**是同一件事。
3. **软删时把对象搬到回收站前缀**（折中）：`users/1/.trash/{fileId}`。
   每次删除多一次对象复制（S3 的 copy 是服务端操作，成本可接受），
   但换来干净的所有权划分。

> ⚠️ **无论选哪条，都还有一个未解决的问题**：t2 的重新上传**本身**就已经覆盖了
> t1 的历史内容（对象存储没有版本）。也就是说 **「回收站保留原对象」这个承诺在当前
> 对象键设计下不成立**——还原回来的可能是新内容，而不是删除时的内容。
> 这与路线图**切片 6「内容哈希与秒传」/ 切片 1「版本列」**要一起解决。
> 详见 [`plans/2026-10-16-drive-production-roadmap.md`](plans/2026-10-16-drive-production-roadmap.md)。

**为何不要在本清单里写实现方案**　按本文件约定，此处只登记现象、根因、影响与方向；
具体设计与切片排期在路线图与迭代记录里。

**关联**　[`plans/2026-10-16-drive-production-roadmap.md`](plans/2026-10-16-drive-production-roadmap.md)
（切片 1「写路径改走 file_id」、切片 4「回收站」）；
在制品迭代记录 [`iterations/2026-10-14-drive-recycle-bin.md`](iterations/2026-10-14-drive-recycle-bin.md)；
[ADR-0001](adr/0001-file-identity-and-version-model.md)（文件身份模型）。


---

## KI-8：回收站还原的测试从未真正运行，且现在失败

**状态**　**已修复（2026-10-16）**。修复内容见文末。

**现象**　`platform-drive` 的 Java 测试**从来没有真正执行过**：

```
$ cd platform-drive && mvn -o test
[INFO] maven-compiler-plugin:3.15.0:testCompile
[INFO] Not compiling test sources
[INFO] maven-surefire-plugin:3.5.5:test
[INFO] Tests are skipped.
[INFO] BUILD SUCCESS          ← 退出码 0，看起来"通过"
```

**根因**　上游 RuoYi 的父 POM（`platform/pom.xml:78`）设了
`<maven.test.skip>true</maven.test.skip>`（注释「打包默认跳过测试」，对上游是合理默认）。
它同时**跳过编译与执行**，于是本模块的 50 个测试既没编译也没跑，
而 Maven 仍然返回 `BUILD SUCCESS`——**"通过"是假的**。

**影响**　比单个测试失败严重得多：

1. **在制品「回收站+版本」的验收结论没有依据**。迭代记录里若写过「测试全绿」，
   那句话在本次修复之前不成立。
2. **KI-7 之所以能藏住，正是因为测试不跑**。修复它时如果没发现这件事，
   我新写的回归测试也会同样"静默通过"——**我确实是先被这一点骗过一次**：
   第一次跑 `mvn -o -q test` 得退出码 0，我据此写了「43 个测试全绿」，
   直到做「回退修复看测试是否真能失败」的自我检查时才发现根本没跑。
3. `target/` 下**没有任何 surefire 报告**，光看目录会以为测试产出被清理了。

**附带发现（同时暴露的既有失败）**　真正跑起来后，50 个测试里有 **1 个错误**：

```
DriveFileServiceTest.restoreResetsToOrigin
  » MybatisPlusException: can not find lambda cache for this entity [EsFile]
    at DriveFileService.restoreFromRecycle(DriveFileService.java:179)
```

`restoreFromRecycle` 用 `new LambdaUpdateWrapper<EsFile>().set(EsFile::getFilePath, ...)`
构造更新语句，而**单测环境里 MyBatis-Plus 没有初始化实体→列名的 lambda 缓存**
（该缓存通常由 `TableInfoHelper` 在 Mapper 扫描时构建，纯 mock 单测里不存在）。
`purgeFromRecycle` 里我新加的查询同样走 `LambdaQueryWrapper`，所以**我那条新用例
在这个点上与既有用例是同一种风险**——它现在能过，只是因为 `selectOne` 被 mock 了、
没有真正触发 lambda 求值；而 `LambdaUpdateWrapper.set(...)` 在**构造期**就要求缓存。

**已实施的修复**

1. `platform-drive/pom.xml` 加 `<properties><maven.test.skip>false</maven.test.skip></properties>`
   覆盖父 POM——**默认 `mvn test` 现在真的跑测试**（50 个，之前是 0 个）。
2. `restoreResetsToOrigin` 的断言原来写的是 `verify(mapper).updateById(restored)`，
   与实现（`update(null, LambdaUpdateWrapper)`）**不符**——因为测试从没跑过，
   这个不一致一直没暴露。已改为断言实际调用，并补 `never().updateById`。
3. 测试类加 `@BeforeAll` 手工初始化 MyBatis-Plus 的实体元数据
   （`TableInfoHelper.initTableInfo` + 最小 `MybatisConfiguration`），
   否则 `LambdaUpdateWrapper.set(...)` 会抛 "can not find lambda cache"。

**验证**：`cd platform-drive && mvn -o clean test` → `Tests run: 50, Failures: 0, Errors: 0`。

4. **补进验证流程**：`/verify` 与 `development.md` 此前都只覆盖三层（Go/Python/前端），
   **Java 完全在流程之外**——这正是 KI-8 能长期藏住的结构性原因。
   两处都已补上 Java 层，并要求**汇总表填写用例数**（判据不是退出码，是 `Tests run: N`）。
   **后续补充（2026-10-16 晚）**：又加了**第 5 层「活栈」**——
   同理，KI-18～KI-23 那六个缺陷**没有一个是四层能发现的**，
   因为单测里的 `fileId`/`sessionId` 是**自己编的小数字**，
   而真实控制面发的是 19 位雪花 ID（超 JS 安全整数、被序列化成字符串）。
   同一类结构性盲区，换个方向又出现了一次：**验证流程缺哪一层，哪一层就长期烂着**。

**四层真实用例数（2026-10-16 实测，全部确认真的执行）**

| 层 | 命令 | 用例数 |
| --- | --- | --- |
| Go | `go test ./...` | 20 个包全绿（`internal/drive` 单包 53 个） |
| Python | `knowledge/.venv/Scripts/python.exe -m pytest -m "not integration"` | 178 passed, 1 skipped |
| 前端 | `npm --prefix frontend test` | 45 passed (8 files) |
| Java | `platform-drive` → `mvn -o clean test` | 50 passed（**修复前为 0**） |

**踩到的坑（值得记）**：`mvn test` **增量**构建会给出陈旧结果——
改了源码后不 `clean` 时，两次得到与源码不符的失败/通过。
**结论：判断测试结果前先 `clean`**，否则可能对着旧 class 下结论。

**以下是最初记录的修复方向，留档备查**（编号顺延）

1. **先让测试能跑**：给 `platform-drive/pom.xml` 加显式覆盖
   （`<maven.test.skip>false</maven.test.skip>` 或 `<skipTests>false</skipTests>`），
   否则一切"测试通过"的结论都不可信。**这是本文里优先级最高的一条**——
   它决定其余测试结论是否成立。
2. **修 `restoreResetsToOrigin`**：单测里初始化 MyBatis-Plus 的实体元数据
   （如测试启动时注册 `TableInfoHelper`），或把 `LambdaUpdateWrapper` 换成
   不依赖 lambda 缓存的写法（`UpdateWrapper` + 显式列名）。
3. 顺带核对：本文其他提及「mvn ... 全绿」的历史记录，**是否也建立在这个假通过之上**。

**关联**　[`development.md`](development.md)（构建/测试命令）；
在制品迭代记录 [`iterations/2026-10-14-drive-recycle-bin.md`](iterations/2026-10-14-drive-recycle-bin.md)。


---

## KI-9：「此电脑」云盘无法下载任何文件（WebDAV GET 一律 HTTP 500）

**状态**　**已修复（2026-10-16）**。

**现象**　从「此电脑」打开云盘空间，**双击任何一个文件都失败**；
直接对 WebDAV 端点发 GET 也会拿到：

```
HTTP/1.1 500 Internal Server Error
seeker can't seek
```

受影响的不只是拖进度条或大文件——**是全部下载**。上传、列目录均正常，
所以表面上「盘能用」，只有真正打开文件才失败。

**根因**　`internal/spacedav` 的 `readFile.Seek` **漏掉了 `http.ServeContent` 的必需调用**。

`webdav.Handler` 的 GET 分支把文件交给 `http.ServeContent`（`x/net/webdav` 的
`webdav.go:238`），而 ServeContent 的调用序列是：

```
Read(512)              ← 探测 Content-Type，**先消费数据**
Seek(0, io.SeekStart)  ← "rewind to output whole file"，要求回退重放
Seek(0, io.SeekEnd)    ← 取大小
Seek(ra.start, ...)    ← Range 时定位
io.Copy(...)           ← 整份输出
```

**顺序与直觉相反：先读、后回退。** 旧实现只接受

- `offset==0 && whence==SeekCurrent`（查询位置）
- `whence==SeekEnd && offset==0`（取大小）

恰好**漏掉第 2 步的 `(0, SeekStart)`**。它一旦返回错误，ServeContent 立刻
`http.Error(w, "seeker can't seek", 500)`，于是**所有下载都挂**。

**为什么长期没被发现**　三条叠加：

1. `internal/spacedav` 原有的 12 个单测**没有一个走真实 `webdav.Handler`**——
   它们测 FS 层方法（Mkdir/Rename/RemoveAll/列举），**从不发 HTTP 请求**；
2. 该缺陷**只在 HTTP 层暴露**：FS 层单测全绿，Seek 单测也全绿（因为旧实现
   「本来就只支持那两种退化用法」，测它符合预期）；
3. 真机验收欠账里没覆盖「双击打开文件」这一条（见 `progress.md` 的已知阻塞）。

**修复**　`readFile` 增加 `fs`/`key` 字段用于重开流；`Seek(0, SeekStart)` 改为
**重开一条从 0 起的流**（需要客户端支持预签名，`*drive.Client` 满足）。
客户端不支持预签名时**如实报错**，延续「不假装成功」的取向——
假装成功会让上层拿到错位数据。

**验证**（`internal/spacedav`，走真实 `webdav.Handler`）

| 用例 | 断言 |
| --- | --- |
| `range_contract_test.go` / 整份读完整 | GET → **200**，字节数等于对象大小，内容逐字节相等 |
| `range_contract_test.go` / 区间读不静默丢数据 | Range → **206** 且区间精确（**Range 也顺带可用了**） |
| `seek_contract_test.go` / `TestSeekContractForServeContent` | `(0,Start)` / `(0,End)` / `(0,Current)` 全部被接受；回退后**能重读到开头数据** |
| `seek_contract_test.go` / `TestSeekUnsupportedClientFailsLoudly` | 无预签名能力时回退**报错**，不返回错位数据 |

**回归验证**：把 `files.go` 退回基线后，`TestSeekContractForServeContent` 立即失败并
指出 `Seek(0, SeekStart) 失败：invalid argument —— ServeContent 会因此回 500`。
即该用例**确实能抓到本缺陷**，不是陪跑。

**教训**　`http.ServeContent` 驱动的类型（`webdav.FileSystem` 的实现）
**必须有走真实 handler 的端到端测试**——单元测试测的是「我以为的调用方式」，
而真实调用方式可能与直觉相反（本例：先读后回退）。单测全绿完全掩盖了「下载全挂」。

**顺带补齐：HTTP 面三条主路径现在都有端到端覆盖**

修完 GET 之后，我把资源管理器会用的另外两条主路径也补了走真实 handler 的用例——
因为 KI-9 恰恰说明「一个分支对了不代表别的对」：

| 用例 | 覆盖 | 实测 |
| --- | --- | --- |
| `TestWebDAVRangeBehaviorContract` / 整份读 | **GET** | 200，字节数与内容逐字节相符 |
| `TestSeekContractForServeContent` | ServeContent 的 Seek 契约 | `(0,Start)`/`(0,End)`/`(0,Current)` 全接受，回退后能重读到开头 |
| `TestWebDAVPutThenGetEndToEnd` | **PUT**（拖文件上传） | 201，且**控制面确实收到**（断言 `uploads[path]` 与申报大小一致） |
| `TestWebDAVDirectoryListingEndToEnd` | **PROPFIND**（列目录） | 207 Multi-Status，响应含预期条目 |

> 取 PUT 的验证方式值得记：**只断状态码不够**，必须断言「控制面收到了正确的路径与字节数」——
> 否则一个静默丢弃写入的实现也能返回 201。

**再加一道防复发闸：生产装配方式本身也被钉住**

上面几条例用假客户端（自定义类型）。但 KI-9 的修复依赖
`f.client.(openRangeer)` 这个**运行期类型断言**——若生产用的 `*drive.Client`
哪天不再满足它（例如 `PresignGet` 改名），假客户端仍然全绿，
而**「此电脑」里双击文件又会打不开**。所以补了两条**针对真实生产类型**的用例：

| 用例 | 钉住什么 |
| --- | --- |
| `contract_test.go` / `TestDriveClientSatisfiesInterfaces` | 针对真实生产类型的接口断言 `var _ openRangeer = drive.New(...)` —— 不满足则**测试编译失败**（形态是函数内断言，不是包级）。不满足即无法通过编译，效果等同构建期信号 |
| `wiring_contract_test.go` / `TestProductionWiringCanSeek` | 用**生产那条装配**（`drive.New(base)` + `spacedav.New`）跑真实 HTTP：假控制面 + 假对象存储 → GET 200 且内容正确 |

> 后者是这一节里最接近「真机」的一条：它同时经过 **真实 `*drive.Client` 的
> HTTP 调用链**（列举 + 预签名）与**真实 `webdav.Handler`**，
> 只把两个外部服务换成 httptest——覆盖范围比纯假客户端大得多。

**关联**　[`iterations/2026-10-15-逆向对标与云盘生产级.md`](iterations/2026-10-15-逆向对标与云盘生产级.md)
的「已回退的改动」一节（同一条探索路径上的失败尝试与教训）；
[`wps-cloud-drive-benchmark.md`](wps-cloud-drive-benchmark.md)（WPS 桌面形态对标）。


---

## KI-10：删除后文件仍在列表里，且回收站永远无法还原

**状态**　**已修复（2026-10-16）**。

**现象**　在网盘页（或「此电脑」挂载盘）删除一个文件后：

1. 它**照旧出现在列表里**，看起来像「删不掉」；
2. 进回收站点「还原」，得到「原位置已有同名文件，请先处理现有文件后再还原」——
   但那个「同名文件」用户根本看不到、也删不掉，于是**永远还原不了**；
3. 点「彻底删除」返回成功，可**空间占用不下降**（对象没被删）。

三条表现是同一个根因的三种投影。

**根因**　软删（P1 回收站）只改目录行、**不动 RustFS 对象**——这是有意的，
为的是还原时同键复位、无需搬对象。但**读侧漏了对应的过滤**：

```
es_file          : state='recycled'          ← 已软删
RustFS           : 对象仍在（键 = 前缀 + file_path）
GET /objects     : storage.listAt(prefix)    ← 从存储列举，回收站的对象照样在里面
                   reconcileAndMap(...)      ← 缺行就 insert 一条新 active 行 ⚠️
```

`reconcileAndMap` 的惰性补账（「列表时发现存储里有、目录层没有 → 补一行」）
在软删场景下变成了**列表自己把删除撤销**：

- **① 删不掉**：新插入的 active 行让文件重新出现在结果里（原来的 recycled 行被
  `activeRowsByPath` 的 state 过滤挡在外面，但新 active 行畅通无阻）；
- **② 还原永远冲突**：`restoreFromRecycle` 检查「原位置是否被 active 占用」，
  占它的正是列表自己刚插入的那一行。用户看不到它（它就是被删的那个文件），
  于是进了死循环；
- **③ 空间不释放**：`purgeFromRecycle` 按「同路径还有没有别的行引用该键」判可否删对象
  （KI-7 的引用计数语义）。列表插入的 active 行成了新的「债主」，
  最后一个引用永远不会消失，对象永久泄漏。

**为什么长期没被发现**　软删是 2026-10-14 那批在制品引入的，而
**验收只覆盖了「删除后回收站里有没有这行」**——那一层确实是对的。
没有任何用例走过「删除 → 再列举一次 → 看列表」这条**读侧**路径。
这与 KI-9 是同一类错误：**写侧改对了，读侧没跟着改，而测试只测了写侧。**

**修复**　把「一次列举要用的两条信息」合成一次查询，并**把三步收成一个入口**：

`DriveFileService` 新增 `ListView`（`activeByPath` + `hiddenPaths`）与
**唯一公开入口** `list(spaceType, ownerId, uploadBy, objects)`，
内部按序做完「查库 → 藏掉软删 → 回填 fileId → 返回 `Listing`」；
`hideDeleted` / `reconcileAndMap` 一律降为 `private`。`GET /objects` 改为

```java
var listing = fileService.list(space, ownerId, userId, storage.listAt(prefix));
```

**为什么刻意收成一个入口**：这三步的**顺序与配套关系本身就是正确性的一部分**。
拆成三个公开方法让调用方自己串，等于把「漏一步」变成可能，而漏掉「藏」这一步的
症状（删了还在 / 还原永远冲突 / 空间永不释放）**全都不会报错**。
KI-9（`Seek` 漏 `(0, SeekStart)`）与 KI-10 是同一类错误：
**多个必须一起做的事，暴露成了多个可以忘记的调用**。
收成单入口后，「忘记」在编译期就不可能发生。

三个口径要点：

1. **只查一次库**：列表是全站唯一出口（网盘页/挂载盘/快搜文件路都走它），
   原来每列举一次要查两遍；两条信息来自同一批行，天然该一起取。
2. **白名单判状态**：只有 `active` 与 `state IS NULL`（列上线前的存量行）算在用，
   **其余一律按不可见处理**。用「等于 recycled 才隐藏」的黑名单写法，
   将来新增状态（`purged` 已预留）会默认漏出来——漏藏是「删不掉」，
   漏出是「删了还在且污染还原」，后者更贵。
3. **同路径并存时以 active 为准**：「删完又重新上传同名文件」是合法状态
   （部分唯一索引只约束 active），此时对象键属于新的那份，列表必须照常显示它。

**回归测试（4 条，均已验证「去掉修复即失败」）**

| 用例 | 钉住什么 |
| --- | --- |
| `reconcileDoesNotResurrectSoftDeletedPath` | 走生产入口 `list`：回收站对象不出现在结果里，且**断言绝不 insert** |
| `listViewHidesDeletedPathsButKeepsActiveOnes` | 软删路径进 hiddenPaths 且**不进** activeByPath |
| `listViewKeepsPathVisibleWhenActiveRowSharesItWithRecycle` | 同路径 active+recycled 并存时以 active 为准（走生产入口） |
| `listViewTreatsNullStateAsActive` | 存量 NULL 行按 active（否则升级后整盘文件集体消失） |
| `listViewHidesUnknownStatesByDefault` | 白名单口径：purged 与未知状态默认不可见且不回填 fileId |

**验证方式**　5 条用例全部**先证明「去掉修复即失败」**（临时还原 `hideDeleted`
调用与 insert 守卫 → `Tests run: 57, Failures: 2`），再确认修复后
`mvn -o clean test` → **57 passed, Failures: 0**。这是 KI-8 留下的纪律。

**方法论**　这次是**自查读侧**查出来的，与 KI-9 同一手法：
写完一个功能后，把它的**每个读路径**都问一遍「这条数据现在是软删的，你返回什么？」。
KI-9 漏的是 `Seek`，KI-10 漏的是列表过滤——**都是「写对了但读侧没跟上」**。

**关联**　在制品迭代记录 [`iterations/2026-10-14-drive-recycle-bin.md`](iterations/2026-10-14-drive-recycle-bin.md)；
KI-7（引用计数判据，本缺陷的③直接踩在它上面）；
[`plans/2026-10-16-drive-production-roadmap.md`](plans/2026-10-16-drive-production-roadmap.md) 切片 4；
对标 [`wps-cloud-drive-benchmark.md`](wps-cloud-drive-benchmark.md) §4.2 / §6-P1。


---

## KI-11：受限文件「看不见但拿得到」——预签名下载不查可见性

**状态**　**已修复（2026-10-16）**。

**现象**　共享空间里把某个文件的可见范围收到「只有部门 3、7 可见」后：

- 部门 8 的用户在列表里**确实看不到**这个文件——列表侧是对的；
- 但只要他知道路径（同名文件、别人转发、历史记录、快照缓存都可能暴露），
  调一次 `POST /easyshare/drive/presign-get` 就能**拿到合法签名的下载 URL**，
  把文件完整取走。

即：**「看不见」被当成了「拿不到」**，而这是两件不同的事。

**根因**　文档级可见性（片 2 数据面）只在**列表出口**做过滤：

```
GET  /objects      → fileService.filterSharedVisible(...)   ✅ 过滤了
POST /presign-get  → storage.presignGetAt(prefix, path)      ❌ 直接签发，什么都没查
```

`presignGet` 只校验「共享空间可读」（`checkSharedReadable`——成员行级别），
**没有校验行级可见性**。于是授权粒度在下载路径上退化成「有没有共享空间权限」。

同一个函数还漏了状态校验：**软删的文件同样能被签出 URL 下载**——
对象仍留在 RustFS（软删不动对象），所以只要不查 state 就一切照旧可读。

**影响**　这是**权限绕过**，不是展示问题：部门可见性对「拿到内容」不起作用。
在共享空间投产（多部门、有敏感文件）的语境下性质较重。

**修复**　把可见性判据抽成**两处共用的一份口径**，并在下载口显式校验：

- `DriveFileService.sharedRowVisible(row, deptId, userId)` —— 静态判据，
  列表过滤 `filterSharedVisible` 与下载校验共用同一份实现，
  **从结构上杜绝「两边判据漂移」**；
- `DriveFileService.assertDownloadable(spaceType, ownerId, path, deptId, userId)` ——
  下载前校验：路径不在回收站 + 共享空间时至行级可见性。
  失败一律报「文件不存在或已删除 / 无权访问」而不区分两者，避免用错误信息探测存在性；
- `DriveController.presignGet` 改为先 `assertDownloadable` 再签发。

**为什么与前两条是同一类错误**　KI-9（读侧漏 `Seek`）、KI-10（读侧漏 state 过滤）、
KI-11（下载口漏可见性）都是**同一个形状**：
**一个「过滤/校验」被放在某一条路径上，而没有放在所有必经之路上。**
修法也统一为：不是「在这条路径上也加一份判断」，而是
**把判据抽出来，让所有路径共用同一份实现，并把判据收进唯一入口。**

**回归测试（4 条，均已验证「去掉修复即失败」）**

| 用例 | 钉住什么 |
| --- | --- |
| `assertDownloadableRejectsSoftDeletedPath` | 回收站路径不能签出下载 URL |
| `assertDownloadableRejectsSharedFileHiddenFromViewerDept` | 部门 8 被拒；部门 3 与上传者本人放行 |
| `assertDownloadableAllowsActivePath` | 个人空间 active 路径照常可读（不误伤） |
| `assertDownloadableAllowsUnrestrictedSharedFile` | visible_depts 为空的共享文件对全体可见（不误伤） |

**未覆盖**　本条修的是**控制面签发口**。对象存储的预签名 URL 一旦签出，
在有效期内（`easyshare.drive.get-expiry`）是**无状态可用的**——
若它被转发出去，控制面无从撤销。真正的短链撤销/审计需要独立的分享接口，
登记在 [`plans/2026-10-16-drive-production-roadmap.md`](plans/2026-10-16-drive-production-roadmap.md) 的后续切片。

**自查补上的一课：授权校验不能顺手扫全空间**　
第一版 `assertDownloadable` 为了「和列表共用同一份 `ListView`」直接调了 `listView(...)`，
而那是**全空间扫描**。下载是热路径，而且客户端**每开一个文件、每次 Seek 回退重开流
都会再签一次**（`internal/spacedav` 的 `readFile.reopen`）——拖一次视频进度条就是几十次
签发。代价会随空间文件数线性增长，在万级对象的空间上是明显的退化。

改为两次**索引点查**（`(space_type, owner_id, file_path)` 上的唯一索引）：

1. 先查该路径的 active 行——命中就直接判可见性（绝大多数情况，一次往返）；
2. 没有 active 行时，再 `exists` 一次确认该路径是否有**任何**行（回收站行）→ 已删除；
3. 两者都没有（存量对象尚未惰性补账）→ **放行**，与列表「未登记按可见处理」同口径，
   否则升级后那批存量文件会集体下载不了。

判据仍与列表**共用同一个** `sharedRowVisible`（这是防错位的根本），
变的只是取行方式。回归测试 `assertDownloadableUsesPointQueriesNotFullSpaceScan`
用 `verify(mapper, never()).selectList(any())` 把「别扫全空间」钉住——
已验证：换回 `listView(...)` 实现该用例即失败。

**关联**　[`iterations/2026-10-14-drive-recycle-bin.md`](iterations/2026-10-14-drive-recycle-bin.md)；
KI-10（同一批发现，同属「读侧/校验侧漏跟随」）；
`wps-cloud-drive-benchmark.md` §6-P1（文档级可见性）。


---

## KI-12：资源管理器改名会换掉 fileId，并留下一份回收站副本

**状态**　**已修复（2026-10-16）**。这是**软删改造引入的回归**，不是老问题。

**现象**　在「此电脑」的云盘盘里给文件改名（或拖动到别的目录）后：

1. 文件在新名字下**能正常打开**——所以表面看不出问题；
2. 但它的 **fileId 换了**：任何按旧 fileId 记录的引用（分享链接、任务引用、
   知识索引里的 `file_id`）全部指向一个**已不存在**的文件；
3. 旧名字那条记录进了**回收站**，且**对象仍占着空间**——改一次名
   空间占用翻一倍，用户不彻底删除就永远不释放。

**根因**　`internal/spacedav.Rename` 一直是「下载 + 上传 + 删除」的模拟实现
（控制面当时没有 rename 端点）。2026-10-14 的软删改造把控制面的删除从
**物理删**换成了**软删**（`state='recycled'`，对象保留），于是那条老路径上
「上传新键 + 删除源」的第二步语义变了：

```
旧：上传新键(新 fileId) → 物理删源        → 空间占用不变、fileId 已换
新：上传新键(新 fileId) → 软删源          → 空间占用 ×2、fileId 已换 + 多一条回收站行
```

**「空间翻倍」是新增的**；「fileId 换了」其实是老毛病，只是当时还没有软删，
看不出来，且 roadmap 早已把它列为切片 4 待办。

**影响**

- **引用失效**：`file_id` 是设计上的稳定身份（`EsFile` 类注、KI-3、
  ADR-0001），分享链接与知识索引都按它绑。改名换号直接违背这条不变量。
- **空间泄漏**：每次改名都留一份完整副本在回收站里。频繁整理目录的用户
  会看到用量莫名上涨，且只有手动进回收站「彻底删除」才回收。
- **流量与耗时**：2 GB 文件改名要把字节拉回客户端再传上去。局域网里尚可忍受，
  跨网就是灾难。

**修复**　控制面新增 **`POST /easyshare/drive/move`**：`CopyObject`（服务端复制，
字节不出对象存储）→ 改目录行 → 删源。编排收在 `DriveMoveService`（可单测），
控制器只做权限校验与转发。三处要点：

1. **fileId 不变**：目录行只改 `file_path`/`file_name`，`file_id` 原样保留——
   这才是「身份与路径解耦」的落点（钉盘取证 §3：`path` 只是属性）。
2. **顺序是「先占目录行、再复制、最后删源」**：目录行的唯一索引是**并发与冲突的
   闸门**，目标已存在时在这里就拒绝，**不会先复制出一个没人登记的垃圾**；
   源最后删，任一步失败都不丢数据。
   —— **但这个顺序有代价**：行先改，就存在「行已改、对象还没搬」的中间态，
   所以必须配**补偿**（见下一条）。最初我把这三行直接写在控制器里、**没有补偿**，
   被自查揪出来：复制失败会留下新路径的**幽灵行**，而旧路径的对象因没有行指向它，
   会被下一次列表惰性补账成**新 fileId**——正是本条要消灭的那个问题本身。
3. **冲突不覆盖**：目标已有 active 行则报「目标位置已有同名文件，未覆盖」，
   与回收站还原的冲突取向一致（破坏性选择交还用户）。

客户端侧 `internal/spacedav.Rename` 改为**优先走服务端 move**（可选能力接口
`mover`，`*drive.Client` 满足），仅在客户端不支持时退回旧路——
这样**升级顺序不被绑死**（客户端与服务端不必同时上线）。

**失败补偿（自查补上的一课）**　失败时不能只把异常往上抛，必须把状态退回一致点：

1. **行挪回原位**：否则新路径留下幽灵行（登记了但没对象，点开 404），
   旧路径的对象又没行指着——下一次列表会把它补账成新 fileId，旧引用全断；
2. **清掉目标处的副本**：复制成功、删源失败时目标已有一份，「两份都在」同样会被
   补账成重复文件；
3. **补偿顺序也是「先挪行、再删副本」**：反过来先删副本的话，中途异常会留下
   「行在新路径、对象也没了」——那才是真的丢数据；
4. **补偿失败不上抛**：原始错误（配额/权限/存储故障）对用户更有诊断价值，
   用补偿异常盖掉它只会让人查错方向，但要留 error 日志供人工介入（数据可能不一致）。

把这套编排从控制器搬进 `DriveMoveService` 也是同一条理由：**补偿是「平时不执行、
出事才执行」的代码**，写在控制器里（依赖 `LoginHelper` 静态方法）就没法单测，
而正因为平时不跑，它更需要测试。

**回归测试（11 条，全部验证过「去掉修复即失败」）**

| 层 | 用例 | 钉住什么 |
| --- | --- | --- |
| Java | `moveRegisteredKeepsFileIdAndUpdatesPath` | 改名后 **fileId 不变**，路径与文件名列同步更新 |
| Java | `moveRegisteredRefusesOccupiedTarget` | 目标被占用时拒绝，且**绝不 updateById** |
| Java | `moveRegisteredReturnsNullForUnregisteredPath` | 历史脏数据无行可改时返回 null，不报错 |
| Java | `moveOrdersRowThenCopyThenDeleteSource` | 编排顺序（`InOrder` 断言），顺序即正确性 |
| Java | `copyFailureRollsRowBackAndCleansTargetCopy` | 复制失败：行挪回 + 清副本，且**绝不删源** |
| Java | `deleteSourceFailureRollsRowBackAndRemovesCopiedTarget` | 删源失败：同样补偿，不留两份 |
| Java | `rollbackFailureDoesNotMaskOriginalError` | 补偿失败不掩盖原始错误 |
| Java | `occupiedTargetRejectsBeforeTouchingObjects` | 目标被占时**一个字节都不该复制** |
| Go | `TestRenamePrefersServerSideMove` | 有 move 能力时**优先走它**，且**断言没有发生上传/单独删源**（负断言：否则「也调了 move」掩盖不了仍然搬了一遍字节） |
| Go | `TestRenameFallsBackWhenServerMoveUnavailable` | 无该能力时退回旧路仍然可用 |
| Go | `TestRenameReportsServerMoveFailureWithoutTouchingSource` | move 失败**不得**退回搬字节、不得删源 |

> 验证记录：临时关掉补偿 → `Tests run: 69, Failures: 2`（两条补偿用例失败），
> 恢复后 → **69 passed, Failures: 0**。

另有**编译期断言** `var _ mover = (*drive.Client)(nil)`：生产类型哪天不再满足
`mover`（如 `Move` 改名），构建直接失败，而不是**静默退回搬字节的旧路**——
这与 KI-9 的 `var _ openRangeer` 是同一手法。

**仍未做**　

1. **目录改名仍被拒绝**（`os.ErrPermission`）：目录在对象存储里只是前缀，
   改名要放大成 N 组复制删除，中途失败留半改状态。需要**真正的目录对象模型**
   （钉子盘有 `local_id`/`parent_id` 树，见路线图切片 4），当前刻意不做。
2. **CopyObject 单次上限 5 GB**：超过需分片复制，当前不实现，也不静默降级。
3. **目录行的 `visible_depts` 不随 move 丢失**——目前是原地改行，天然保留；
   但若将来 move 跨空间（个人 ↔ 共享），需要重判可见性与配额，当前不支持跨空间。

**关联**　[`plans/2026-10-16-drive-production-roadmap.md`](plans/2026-10-16-drive-production-roadmap.md)
切片 4「服务端 rename/move」——本条把它落地；
[`iterations/2026-10-14-drive-recycle-bin.md`](iterations/2026-10-14-drive-recycle-bin.md)（软删改造，本条回归的来源）；
KI-3（稳定文件身份）；`ADR-0001`；钉盘取证 §3「身份不是路径」。


---

## KI-13：拖大文件进资源管理器会让 Core 吃掉等量内存

**状态**　**已修复（2026-10-16）**。

**现象**　在「此电脑」的云盘盘里拖入一个大文件（比如 5 GB 的虚拟机镜像）：

- **Core 进程内存瞬间上涨与文件等量**——5 GB 文件就是 5 GB 分配；
- 内存不够时 **OOM 崩溃**，而不是弹出可读错误；
- 桌面端是与用户其他程序**共享内存**的进程，所以这不只是「慢」，
  是把整台机器的可用内存一起吃光。

**根因**　`internal/spacedav` 的 WebDAV 写路径把整份内容攒在 `bytes.Buffer` 里：

```go
type writeFile struct {
    buffer bytes.Buffer   // ← 整份文件
    ...
}
```

**为什么当初这么写**（代码注释原文）：配额在签发预签名 URL 时判定，
而判定需要**确切字节数**；流式上传拿不到总长，只能上报 0，配额就等于失效。
攒到 Close 才知道真实大小——这是让配额生效的代价。

**为什么现在必须改**：那段注释同时写着「真正的解法是分块上传 + 分块配额核算，
那需要控制面先支持 multipart 签发」。而**那条前提早在 2026-09-06 就成立了**：

- 控制面：`/easyshare/drive/upload-session/create|part|complete|abort` 四端点；
- 客户端：`drive.Client.UploadFile` 已按 `multipartThreshold = 32 MiB` 分流到
  分片 + 断点续传，会话状态持久化在数据根 `upload-sessions/`。

也就是说，**能力早就具备，只是这份代码没跟上**——它仍然走单请求 PUT。
注释里的「已知代价」因此从「有理由的取舍」变成了**过期的理由**。
这类「注释里写着的前提已经失效」是最难靠读代码发现的一类欠账。

**修复**　写路径从「攒内存」改为「**落盘暂存**」，并把本地路径交给分片通道：

1. `writeFile` 用 `os.CreateTemp` 在暂存目录建临时文件，`Write` 直接写盘；
2. `Close` 时 `Sync()` 刷盘 → 交给可选能力 {@link fileUploader}
   （即 `*drive.Client.UploadFile`）按**本地路径**上传——
   字节由 client 自己分片读，**不经过本包内存**；
3. 客户端不支持该能力时（假实现/旧控制面）退回单请求 PUT，
   但也是**从磁盘流式读**，不再整份进内存；
4. **Close 无论成败都删暂存文件**（`defer`）：残留的每个文件都白占磁盘，
   而用户看不到任何提示（文件明明已经传完了）。

落盘顺带解锁了分片：有了本地路径，资源管理器拖进来的大文件**终于也能
分片 + 断点续传**了——在此之前这条能力只有客户端内「文件」页上传才用得上。

**暂存目录刻意不放系统临时目录**（`Options.SpoolDir`，生产装配指向数据根
`upload-spool/`）：拖进来的可能是几十 GB，而系统盘通常最小；放数据根，
用户至少有「清理数据盘」这一个直觉可循。

**回归测试（4 条，全部验证过「换回内存实现即失败」）**

| 用例 | 钉住什么 |
| --- | --- |
| `TestUploadSpoolsToDiskNotMemory` | 写 64 MiB 后**堆增长必须远小于写入量**（内存实现会让堆至少涨 64 MiB） |
| `TestUploadUsesSpoolPathForMultipart` | 有分片能力时走 `UploadFile`，且**断言不再走单请求 Upload**（否则等于传两遍） |
| `TestUploadSpoolIsCleanedUp` | Close 后暂存目录清空 |
| `TestUploadSpoolCleanedUpOnFailure` | **失败也要清**（失败路径一周才走一次，最容易漏） |

另有**编译期断言** `var _ fileUploader = (*drive.Client)(nil)`：生产类型哪天不再满足
该能力，构建直接失败，而不是**静默退回单请求 PUT**（大文件既不能分片也不能续传）。
这与 KI-9 的 `var _ openRangeer`、KI-12 的 `var _ mover` 是同一手法。

**验证过程的一个插曲**（值得记）　`TestUploadSpoolCleanedUpOnFailure` 第一版
**在内存实现下也通过**——因为它只断言「Close 后目录为空」，而一个根本不落盘的实现
同样留下空目录，于是这条用例平凡成立。补了「先证明写入后目录里确实有 1 个文件」
才让它真正生效。**「断言了某个现象」和「断言了那个现象只可能由正确实现产生」
是两回事**；四条用例重跑后全部能在内存实现下失败。

**未覆盖**

1. **暂存磁盘空间**：暂存与最终对象各占一份，峰值是文件大小的两倍。
   真正的解法是流式分片（边收边传），需要 WebDAV 层拿到 Content-Length
   （`x/net/webdav` 的 `handlePut` 不透出），属后续切片。
2. **暂存目录的孤儿清理**：进程被强杀（非正常退出）会留下 `.tmp`。
   启动时扫一遍删除是低成本加固，当前未做——已登记在
   [`internal/spacedav/README.md`](../internal/spacedav/README.md) 的
   「已知缺陷：孤儿暂存文件」一节（含判断依据为什么要小心：正在上传时也有 `.tmp`，
   直接全删会打断进行中的上传）。

**关联**　[`plans/2026-10-16-drive-production-roadmap.md`](plans/2026-10-16-drive-production-roadmap.md)
（切片 5b 双向同步的输入）；KI-9（同属 `internal/spacedav` 的下载路径）、
KI-12（同属「能力已具备但代码没跟上」）。


---

## KI-14：「只读」占位符实际可写，用户改动会静默丢失

**状态**　**已修复（2026-10-16）**。**这是接入 `app.go` 前拦下的缺陷**——
换言之，若直接按路线图把 cfmount 挂上去，用户第一天就会撞上它。

**现象**　`internal/cfapi` 与 `internal/cfmount` 的全部文档与代码注释都把切片 5 描述为
**「只读占位符」**（`cfapi/README.md`、`cfmount/README.md`、`cfapi_windows.go` 的
「第一片只做只读呈现」）。但那个「只读」**没有任何执行层依托**：

1. `Connect` 只注册了两个回调——`FETCH_DATA` 与 `CANCEL_FETCH_DATA`，
   **没有任何写回调**（`callback_windows.go` 的回调表）；
2. 占位符创建时 `FileAttributes = FILE_ATTRIBUTE_NORMAL`（`0x80`），
   **没有只读位**。

于是用户在资源管理器里可以随意编辑、重命名、删除占位符——
**系统不会回调我们，云端完全不知道**。

**实测（2026-10-16，本机）**

```
占位符对属主可写（Mode=?rw-rw-rw-）——「只读」不由文件属性保证
⚠️ 占位符可写：用户改动只落在本地，云端无感知
```

**后果链（三段都实测，不靠推断）**

| 步骤 | 实测结果 |
| --- | --- |
| ① 用户编辑并保存 | **成功**（`os.WriteFile` 无错）——用户看到「保存成功」 |
| ② 改动上传到云端 | **没有**（无写回调，无任何上传） |
| ③ 重新同步（全量重建） | 当前实现下 `CfCreatePlaceholders` 报 `0x800700B7`（ERROR_ALREADY_EXISTS）而**整批失败**；一旦补上 `SUPERSEDE` 让它"正常"重建，**本地内容被覆盖成 20 个 0 字节** |

第③步是关键：它说明「可写」不是隐患，是**会丢数据的路径**。
而且注意一个反直觉之处——**当前之所以还没丢，是因为重建本身是坏的**；
一旦有人去修"重建失败"这个看起来更显眼的 bug，数据丢失就会立刻发生。
两个缺陷互相掩盖，这是本轮最危险的一处。

**修复**　给占位符打上真正的 `FILE_ATTRIBUTE_READONLY`：

- `cfapi.PlaceholderSpec.ReadOnly` / `cfapi.CloudEntry.ReadOnly` 作为开关；
- `NewPlaceholders` 在 `IsDir`/`Normal` 基础上 `|= FileAttributeReadOnly`；
- `cfmount.Entries` **对全部条目（文件与目录）置位**——
  双向同步（切片 5b）落地时才连同写回调一起放开。

**实测验证（三条，均已确认「去掉修复即失败」）**

| 用例 | 断言 | 实测输出 |
| --- | --- | --- |
| `TestReadOnlyPlaceholderBlocksWrite` | 写入必须被拒 | `Access is denied.` |
| `TestReadOnlyPlaceholderStillHydrates` | **只读不得影响按需读取** | 读回 22 字节，内容正确 |
| `TestReadOnlyPlaceholderIsMarkedReadOnly` | 属性位真的落上了 | `attrs=0x401621`（含 `0x1`） |

> 第二条是**必须成对验证**的那一条：hydration 的本质是**内核往这个文件写数据**，
> 所以「打上只读」完全可能把「点开才拉」一起弄坏——为了防写把读也废掉。
> 实测证明两者不冲突（CfApi 的水合走内核通道，不经过用户可写的句柄），
> 但这一点**不实测无法确定**，不能靠推理。

**同时发现（未修）：`Sync` 声称可重复调用，实际不能**

`mount_windows.go` 的 `Sync` 注释写着「可重复调用（例如定时刷新）」，
但实测第一次刷新就失败：

```
Sync 不可重复——cfapi: 建树有 1 条失败: CreatePlaceholders 失败: 0x800700b7
```

根因：`BuildTree` 每条都用 `CreateFlagNone` 重建，而占位符已在磁盘上。
修法是给 per-info `PlaceholderCreateFlagSupersede`（常量已定义、当前未用）。

**「两个缺陷互相掩盖」的困局已解开（2026-10-16 同日晚）**

当时刻意不加 `SUPERSEDE`，正确。但后来发现**根本不该用 SUPERSEDE**——
WPS 取证 §9 第 5 条早就写了正解：`CfUpdatePlaceholder`（**只更元数据，绝不触发下载**）。

于是按三条依次验证后落地：

1. **先确认只读的覆盖范围**（`readonly_ops_windows_test.go`）：实测只读位挡住**内容改写**，
   但**不挡删除、不挡重命名、不挡新建**。这三条都**不丢内容**（云端对象仍在），
   所以不阻塞接入；但它们说明「只读占位符」的「只读」保护的是**内容**，不是**目录结构**。
   投产时需在切片 5b 注册 `NOTIFY_DELETE` 等回调。
2. **实现并按句柄语义验证元数据更新**（`update_probe_windows_test.go`）：
   实测 `CfUpdatePlaceholder` 改大小/时间**不下载内容**（磁盘分配保持 0），
   **已水合内容完好**，且**只读位保留**。
3. **接入 `BuildTree`**：`CfCreatePlaceholders` 报 `ERROR_ALREADY_EXISTS` 时改走元数据更新。

**其中一条实测抓到了我自己的错**：`OpenPlaceholderHandle` 第一版用 `GENERIC_READ` 打开句柄，
**仅打开就触发了整份水合**（磁盘分配 0 → 37）——若这样接进去，
「元数据同步」会变成「每次同步下载整个文件」，`列表零下载` 的核心承诺直接失效。
改用 `FILE_READ_ATTRIBUTES`（只读属性、不要数据访问权）后分配保持 0。
**这不是省权限的小优化，是功能正确性问题。**

**另一处修正**：`isAlreadyExists` 第一版只处理**条目级** `Result`，
而实测该错误是从**调用级**返回的（首个条目即失败时上抛）。
两条路径都要处理——只挑一条的话刷新仍然报错。

至此 `Sync` 可重复：`TestSyncIsRepeatableWithoutLosingContent` 验证
「第二次 Sync 成功 + 内容完好 + 未触发下载 + 只读保留 + 大小跟上云端」。

**关联**　[`internal/cfapi/README.md`](../internal/cfapi/README.md)、
[`internal/cfmount/README.md`](../internal/cfmount/README.md)；
[`plans/2026-10-16-drive-production-roadmap.md`](plans/2026-10-16-drive-production-roadmap.md) 切片 5 / 5b；
WPS 取证 §2.1b（回调表——WPS 也只注册读回调，但它的占位符由自家引擎管理写意图）。


---

## KI-15：重转写偶发失败的真因是并发写与异常被吞

**状态**　**已修复（2026-10-16）**。本条正文经过一次**重大更正**：
初次登记把它判成「机器负载导致的 flake」，那个判断是错的。

**现象**　`knowledge/tests/test_meetings.py::test_retranscribe_resets_blocks_and_requeues`
偶发失败。实测一次：

```
1 failed, 177 passed, 1 skipped
```

**初次误判（留档，因为它比缺陷本身更值得记）**　当时观察到三条「像环境问题」的
证据——①单独跑必过；②紧接着重跑全量也过；③该文件本轮未被改动——于是登记为
「4 秒墙钟预算不够、机器忙时排不到时间片」，修复方向写的是「放宽轮询」。
**这三条证据全都是真的，但它们只证明「与本次改动无关」，不证明「与代码无关」。**
按那个方向改，等于把症状盖住。

**戳破它的一步**　把墙钟预算从 4 秒放宽到 **30 秒**，**它照样失败**，且
`status` 恒为 `processing`。这条一测出来，「负载不够」就被排除了——
真负载下 30 秒足够，而线程转储显示 worker 线程**空闲地等着队列**
（不是被占住）。即：异常已经发生，并被 `concurrent.futures` 吞进了 Future。

**根因（两个，缺一不可）**

1. **`MeetingStore._write_meta` 与读并发**：写路径是「写临时文件 → `Path.replace`」
   的原子替换，而 **Windows 不允许替换一个正被打开读的文件**，
   此时 `Path.replace` 抛 `PermissionError`。当时 `get()` / `list()` **都不持锁**，
   于是「前端轮询列表」与「后台管线写记录」并发时**必然偶发**这个异常
   （POSIX 上 rename 可以覆盖被打开的文件，所以这个竞态只在 Windows 出现——
   而本仓主线恰好是 Windows）。
2. **`_process` 缺兜底**：区块抛错时异常穿过 `_process` 被线程池吞掉，
   于是后面的 `status="completed"` **永远执行不到**——记录**永久停在 `processing`**，
   用户看到的是永不结束的「正在处理」。

**这就是「偶发」的真正含义**：不是结果随机，而是**触发条件与负载相关、
但失败是确定的**。第 1 条制造异常，第 2 条把它变成不可见的永久卡死。

**修复**

| 位置 | 改动 |
| --- | --- |
| `MeetingStore._lock` | `Lock` → **`RLock`**：写路径是「持锁 → 调 `self.get()` → 写」，而 `get()` 自己也要持锁，普通 `Lock` 会**自死锁** |
| `get()` / `list()` | **也持锁**——这是 Windows 上的正确性要求，不是性能取舍 |
| `_write_meta` | 加**重试退避**（50 次 × 5~25ms ≈ 最长 1.2 秒），兜跨进程占用；仍失败则如实抛出（不无限重试——那会把可诊断的错误变成静默挂起） |
| `_process` | 加**兜底 `try`**：异常必须落 `status="failed"` + 可读原因；连失败状态都写不进去时至少留日志。**绝不能让记录停在 `processing`** |

**验证**　两条新用例均先证明「去掉修复即失败」：

| 用例 | 钉住什么 |
| --- | --- |
| `test_write_meta_survives_concurrent_readers`（4 读线程 + 300 次写） | 修复前**必然**撞上 `PermissionError` |
| `test_process_marks_failed_instead_of_stuck_processing`（注入 `PermissionError`） | 修复前 `status` 恒为 `processing`，修复后为 `failed` 且原因可读 |

**连续 8 次全量跑 `test_meetings.py` 零失败**（此前约每 2~3 次失败一次）。

**影响**　比这条缺陷更值得记的是它**污染验收口径**：本轮结论一直以「四层全绿」
呈现，而这条偶发红会让那句话在某个时刻不成立，且**看起来像回归**。
排查它花了额外一轮往返——这正是「假阴性」的代价。

**方法论**　**「偶发失败」≠「环境问题」**。判定 flake 至少要做一件事：
**证明它在放宽约束后仍然失败**。做到这一步，它就自动从「环境问题」
变成「待查缺陷」。这条与 KI-8（测试没跑）/ KI-17（被 mock）/ KI-18（输入自编）
同属「测试与验收可信度」家族——**它污染的不是代码，是结论**。

**关联**　[`development.md`](development.md)（四层测试口径）；
[`../knowledge/app/meetings/store.py`](../knowledge/app/meetings/store.py)（重试注释）；
[`../knowledge/app/meetings/service.py`](../knowledge/app/meetings/service.py)（兜底注释）；
本仓 `/verify` 命令（同时跑四层时最容易触发）。


---

## KI-16：云端删除的文件永久留在本地（幽灵占位符）

**状态**　**已修复（2026-10-16）**。

**发现方式**　修好 KI-14 的 `Sync` 可重复性之后**立刻暴露**——
这正是「修一个缺陷会让下一个变得可达」的典型：
`Sync` 此前第一次刷新就报 `ERROR_ALREADY_EXISTS`，所以
「云端变了、本地跟不跟」这条路径**根本走不到**。

**现象**　云端删掉一个文件后：

- 本地占位符**仍然存在**（`Sync` 不动它）；
- 点它会报错（索引已按新清单重建，删掉的文件不在索引里）；
- 而且**每次 Sync 都不会清理它**——永久留着。

即用户看到一个「删不掉、点不开、反复出现」的幽灵文件。

**根因**　`BuildTree` 只做「创建 / 更新」两件事，**没有任何删除逻辑**。
这不是疏漏，而是当时 `Sync` 只能跑一次、这段代码从未被真正使用；
一旦可重复，缺口立刻显现。

**实测确认**

```
⚠️ 云端已删除的文件**仍留在本地**（…\gone.txt）——BuildTree 只做创建/更新，
   没有删除逻辑。用户会看到一个点开就报错的幽灵文件，而且它会**永久**留着
```

**⚠️ 修复时发现的真正风险：不能无脑删**

在动手删之前先测了「幽灵文件里到底有什么」，结果决定性：

```
云端已删，本地仍持有 34 字节（磁盘分配 34）——直接删掉它就是销毁本地仅存副本
```

**已水合**的幽灵占位符在本地持有**完整内容**，而云端已经删了——
那份内容是**本地仅存的一份**。若无脑清理，就是**销毁用户数据**，
且用户毫无察觉。

**修复**　只删**未水合**的（那种占位符不持有任何字节，删了不丢数据）；
**已水合的保留并计数**（`TreeStats.SkippedHydrated`），由上层决定怎么提示用户。

判据用 `GetCompressedFileSize > 0`（未下载的占位符返回 0，这正是
「列表零下载」的观测口径）。**查询失败或判断不了时按「有水合内容」处理**——
保守不删，因为**代价不对称**：少删一个幽灵只是碍眼，误删一个水合文件是真丢数据。

清理时机在 `BuildTree` **之后**（先建/更新出云端仍存在的条目，再清理剩下的；
反过来会把本轮刚建出来的也当成幽灵删掉）。

**回归测试（3 条，均已验证「去掉修复即失败」）**

| 用例 | 钉住什么 |
| --- | --- |
| `TestSyncRemovesLocallyDeletedCloudFile` | 未水合的幽灵**被清理**，且**不误伤**云端仍存在的文件 |
| `TestGhostCleanupSparesHydratedFile` | **已水合的幽灵必须保留**（去掉 `isHydrated` 判断则该用例报「这是数据丢失」） |
| `TestHydratedGhostRetainsLocalContent` | 记录事实：水合幽灵确实持有内容（修复决策的依据） |

**未覆盖**

1. **已水合幽灵的用户提示**：现在只是保留并计数，**没有告诉用户**
   「这个文件的云端版本已删除，本地这份是仅存的」。上层接入时应提示，
   否则用户一直以为它还在云端。
2. **目录幽灵**：目录不持有内容，直接递归删；但若目录里全是已水合幽灵，
   递归下去不会删它们（`isHydrated` 拦住），目录因此**删不掉**——
   表现为一个空壳目录里留着几个保留文件。这是可取的行为，但需在接入时
   确认资源管理器里的观感（未做 GUI 观察）。

**关联**　[`internal/cfapi/README.md`](../internal/cfapi/README.md)；
KI-14（本条的可达性由它的修复带来）；
[`plans/2026-10-16-drive-production-roadmap.md`](plans/2026-10-16-drive-production-roadmap.md) 切片 5。


---

## KI-17：`SysUserDept` 缺 `@TableId`，导致按 ID 查询整体 500

**状态**　**已修复（2026-10-16）**。

**发现方式**　**只有把控制面真跑起来才会暴露**——这是本仓迄今第一个
「单测全绿但服务一跑就 500」的缺陷。与 KI-8（测试从未运行）不同：
这里的测试**真的在跑**，只是它们把 Mapper 当 mock 用，
`selectById` 返回 stub 值，**永远不经过 MyBatis**。

**现象**　凡是走到「按 userId 查部门归属」的接口全部 500：

| 接口 | 修复前 |
| --- | --- |
| `GET /easyshare/space/mine` | 500「发生未知异常」 |
| `POST /easyshare/drive/presign-get`（**下载任何文件**） | 500 |
| `GET /easyshare/drive/objects` | 200（不经过该路径） |

**根因**

```
org.apache.ibatis.binding.BindingException:
  Invalid bound statement (not found):
  org.dromara.easyshare.drive.mapper.SysUserDeptMapper.selectById
```

`SysUserDept` 实体没有 `@TableId`。MyBatis-Plus 在「实体没有标注主键」时
**无法为 `selectById` 生成 WHERE 子句**，于是该语句根本没被注册——
调用时报 `Invalid bound statement`，**而不是返回 null**。

**影响面比看上去大**　`SpaceService.deptIdOf()` 被三处调用：
`/space/mine`、`/drive/objects`（共享空间分支）、`/drive/presign-get`。
其中**下载签发**是致命的——用户点开任何文件都会失败。

**为什么现在才发现（两个原因叠加）**

1. **单测里 Mapper 是 mock 的**：`SysUserDeptMapper` 在测试中被 mock，
   `selectById` 返回设定值，MyBatis 的语句绑定从未参与；
2. **真实联调一直没做**：验收口径要求连服务器，而服务器长期不可达
   （见 `progress.md` 已知阻塞），直到 2026-10-16 用本机 Docker 环境跑起来。

**补充说明**：本条**不是本轮改动引入的**。`deptIdOf` 来自
`f1a8a31 feat: 部门级权限片 1`，`SysUserDept` 自那时起就缺注解
（`git diff` 为空可证）。但本轮的 KI-11 修复**扩大了它的影响面**——
KI-11 在 `/presign-get` 里新增了对 `deptIdOf` 的调用，
把原本只影响 `/space/mine` 的缺陷扩散到了**下载路径**。
这是「修一个缺陷可能放大另一个已存在缺陷」的实例。

**修复**　给 `SysUserDept.userId` 加 `@TableId(value = "user_id")`，
与同包已正确标注的 `SysConfigRow`（`@TableId(value="config_id")`）保持一致。

**验证（真实控制面 + 真实 PostgreSQL，非 mock）**

| 接口 | 修复前 | 修复后 |
| --- | --- | --- |
| `GET /easyshare/space/mine` | 500 | ✅ 200 |
| `POST /easyshare/drive/presign-get`（个人） | 500 | ✅ 200，签出真实预签名 URL |
| `POST /easyshare/drive/presign-get`（共享，无权限） | 500 未知异常 | ✅ 可读的「没有共享空间的访问权限」 |

**方法论**　这条与 KI-8 合起来说明一件事：
**「测试通过」有三种不同程度的含义**——
①测试根本没跑（KI-8）；②测试跑了但把被测对象替换成 mock，
于是真实 SQL/绑定/序列化从未执行（本条）；③测试真的端到端跑了。
本仓此前长期停留在 ①，修好后是 ②，**直到本轮才第一次达到 ③**。

**关联**　`docs/development.md`（四层测试）；
KI-8（同类：测试可信度）；KI-11（本条的放大器）；
`deploy/ruoyi-db/README.md`（本机控制面启动手册）。


---

## KI-18：控制面把雪花 ID 序列化成字符串，Go 侧解析**整个响应失败**

**状态**　**已修复（2026-10-16）**。

**现象**　对真实控制面调用 `Objects()` 直接报：

```
json: cannot unmarshal string into Go struct field Object.fileId of type int64
```

注意这不是「某个字段变成 0」——`encoding/json` 在**类型不符**时会让
**整个 `Unmarshal` 失败**。因此后果是：

| 受影响能力 | 表现 |
| --- | --- |
| 网盘页文件列表 | 拿不到数据 |
| 「此电脑」挂载盘（`spacedav`） | 列目录失败 |
| CfApi 占位符（`cfmount`） | 列清单失败 |
| 快搜「文件路」 | 该来源静默为空 |
| 回收站列表 | 同样失败（`RecycleEntry` 同类字段） |

**根因**　RuoYi 的 `JacksonConfig` 给所有 `Long` 注册了 `BigNumberSerializer`：

```java
// platform/ruoyi-common/ruoyi-common-json/.../BigNumberSerializer.java
private static final long MAX_SAFE_INTEGER = 9007199254740991L;  // 2^53-1
if (value.longValue() >= MIN_SAFE_INTEGER && value.longValue() <= MAX_SAFE_INTEGER) {
    super.serialize(value, gen, provider);   // 小数值 → 数字
} else {
    // 超出范围 → 序列化成字符串（保护前端 JS 的 Number）
}
```

这本身是**正确**的——雪花 ID（`2110952103974817794`）远超 2^53-1，
直接发数字会被 JS 静默截断精度。问题在 **Go 侧**：
`internal/drive` 把它们声明成 `int64`，于是解析必失败。

实测的真实响应（`/easyshare/drive/objects`）：

```json
{"path":"a.txt","size":11,"fileId":"2110952103974817794","versionId":1}
```

**为什么单测全绿**　所有单测都用**自己构造的** `drive.Object`，
或 httptest 里**手写的** JSON——手写时自然写数字，而真实服务端发字符串。
这与 KI-8（测试没跑）、KI-17（关键对象被 mock）构成第三种形态：
**测试跑了、也没全用 mock，但输入是自己编的，与真实契约不符**。

**修复**　引入 `drive.FlexInt64`（实现 `json.Unmarshaler`），
**数字与字符串两种形态都收**：

- 数字走 `strconv.ParseInt`；
- 字符串去引号后解析（服务端超范围时的形态）；
- `null` / 空串 → 0（对应「未登记」语义，`cfmount` 已有 `fileId=0` 的防碰撞处理）。

应用到 `Object.FileId` / `Object.VersionId` / `RecycleEntry.FileId|Size|VersionId`。
两种形态都必须支持，因为 `BigNumberSerializer` 是**按值**决定的——
同一个字段在不同响应里可能是数字也可能是字符串。

**顺带澄清（避免误伤）**　`internal/account` 的空间/配额接口
（`MySpaces` / `ListSpaces` / `GetCapacity` / `ListUsers` / `SharedMembers` / `ListDepts`）
**全部正常**——新增活栈探针 `live_json_probe_test.go` 逐个验证通过。
它们的 `SpaceId` / `OwnerId` 声明为 `string`，本来就与字符串形态一致。
即：**问题只在 `internal/drive` 一侧**，不是全仓性的。

**回归测试**

| 用例 | 钉住什么 |
| --- | --- |
| `TestObjectUnmarshalsStringFileId` | 真实响应形状（字符串 fileId）必须能解析 |
| `TestObjectStillAcceptsNumericIds` | 数字形态（未超范围时服务端发数字）也必须能解析 |
| `TestLiveCASRejectsStaleVersion`（活栈） | 走真实控制面；顺带覆盖回收站列表解析 |

**关联**　[`ai-architecture.md`](ai-architecture.md)（前后端契约）；
KI-8 / KI-17（同属「测试可信度」家族：没跑 / 被 mock / 输入自编）。



---

## KI-19：健康探测的 300ms 预算把「慢」读成「没有 Core」，会拉起重复进程

**状态**　**已修复（2026-10-16）**。

**发现方式**　它在**连续 8 次全量回归里失败了 1 次**：
`internal/desktop` 的 `TestCoreHealthyVerifiesIdentityAndProof` 报
「CoreHealthy rejected a matching Core」，耗时恰好 **0.30s**。

这是本仓最容易被随手关掉的一类信号——「偶发失败」「环境问题」「机器忙」。
按 KI-15 立下的规矩，**先证明它在放宽约束后仍然失败**，再决定要不要当缺陷：
单跑必过、连续 6 次全量也全过，所以「测试写错了」不成立；于是转去量它。

**量出来的事实（都是实测，不是推断）**

| 条件 | 结果 |
| --- | --- |
| 空载，300 次探测 | 最慢 **11ms**，0 次失败 |
| 外部负载（14 个忙循环 / 20 逻辑核）+ `GOMAXPROCS=2`，300 次 × 10 轮 | 多轮出现失败；**每次失败的耗时都卡在预算上**：307 / 312 / 315 / 320 / 325ms |
| Core 真的不在（连接被拒），20 次 | 最慢 **14ms**——与预算无关 |
| Core 活着但响应 400ms | 被判成**不健康**（300.8ms 返回 false） |

第三、四行是这条缺陷的关键：**预算既不决定「Core 不在」的判定速度**
（连接被拒是毫秒级的），**却决定「Core 活着但慢」会不会被误杀**。

**根因**　`internal/desktop/process.go` 的 `CoreHealthy` 把三种不同的结论
压成了一个布尔：

```go
client := http.Client{Timeout: 300 * time.Millisecond}
response, err := client.Get(options.BaseURL + "/health?nonce=" + nonce)
if err != nil {
    return false        // ← 超时（说不清）与连接被拒（确定不在）都走这里
}
```

而调用方拿 `false` 做的是**不可逆**决策：

| 调用方 | 拿到 false 会做什么 |
| --- | --- |
| `EnsureCore`（`app.go` 启动、watchdog 重启） | **拉起一个新 Core 进程** |
| `cmd/core` 启动时 | 认为「没有兼容的 Core」，继续走到绑端口 |
| `cmd/core` 端口冲突后 | 判断「不是竞争失败」→ `log.Fatal` |
| watchdog | 累计失败，够 3 次就重启 |

于是「CPU 忙导致探测晚了 7ms」的后果是：**去拉一个重复的 Core**，
那个进程抢不到端口，最后留一条把原因指向「端口冲突」的致命日志——
真正的毛病（老 Core 只是慢）反而被这条日志盖住。

**为什么这不是「测试环境问题」**　触发条件是**用户机器上真实存在的**：
杀软全盘扫描、OneDrive/WPS 同步、装大包、视频转码——任何一个都能让
一次 HTTP 往返滑到 300ms 以外。生产代码里也有同样的 300ms，
所以这条缺陷在真实机器上**不需要「四层同时跑」也能触发**，
只是本机没同时压那么重而已。

**修复**　把布尔换成三态，并且只在**确定不在**时才允许拉起：

```go
type coreState int

const (
    coreStateHealthy coreState = iota // 我们的 Core，身份与证明都对
    coreStateAbsent                   // 确定没在监听（连接被拒）——可以安全拉起
    coreStateUnknown                  // 说不清（超时）——**不是**「不在」的证据
)
```

三处配套改动：

1. **预算 300ms → 1s**。依据是上表的第三行：真正「不在」时是连接被拒
   （14ms），放宽预算**几乎不花钱**，却把调度抖动的误杀面大幅收窄；
2. **超时单独识别**（`errors.As(err, &net.Error)` + `Timeout()`）→ `unknown`，
   其余错误（连接被拒等）→ `absent`；
3. **`EnsureCore` 连续不确定时拒绝拉起**，并返回一条能读懂的错误
   （「无法确定 Core 是否在运行：不拉起重复进程」），而不是当作「不在」照常 `command.Start()`。

**验收**

| 用例 | 钉住什么 |
| --- | --- |
| `TestProbeTimeoutIsNotAbsent` | Core 活着但响应 400ms，**不许**判成 absent |
| `TestProbeTimeoutBeyondBudgetIsUnknown` | 慢过预算时必须是 unknown，不是 absent |
| `TestWaitCoreAbsentRefusesToSpawnOnUnknown` | 连续超时必须**拒绝拉起**并给出原因 |
| `TestWaitCoreAbsentOnRefusedConnection` | 真不在时仍快速判定可拉起（且没白等整个预算） |
| `TestWaitCoreAbsentSeesHealthyCore` | 已有健康 Core 时返回「不要拉起」 |
| `TestCoreHealthyStillDistinguishesIdentityAndProof` | 原有身份/证明语义不被三态改造破坏 |

前三条**已验证「回退修复即失败」**（把预算改回 300ms、
把超时重新并入 absent 后，三条全部转红，报错正是「会拉起重复的 Core」）；
后三条在新旧两种实现下都通过——这是对的，它们守的是不该变的行为。

**回归**　在**原先复现失败的那组条件**（14 个忙循环 / 20 逻辑核 + `GOMAXPROCS=2`）
下连跑 10 遍：**10/10 全绿**（修复前同样条件下约每 2~3 轮出现一次失败）。

**方法论**　这条与 KI-15 是同一条规矩的第二次收获：
**「偶发失败」不是可以先关掉的噪音，它是关于代码的信息**。
两次的路径一模一样——先假设是环境，再用「放宽约束后照样失败」把它证伪。
区别只在于：KI-15 的根因是并发写 + 异常被吞，这条的根因是
**一个布尔承担了三种语义，而调用方拿它做不可逆决策**。

**关联**　[`known-issues.md`](known-issues.md) KI-15（同一套判定规矩）；
`internal/desktop/process.go`（三态与预算的注释）；
`app.go`（watchdog）与 `cmd/core/main.go`（重复进程的两条路径）。


---

## KI-20：Multipart 上传**绕过了 CAS**，且它的雪花 ID 也没跟上 KI-18 的修复

**状态**　**已修复（2026-10-16）**。本条含**两个独立缺陷**，
它们叠在同一条路径上（≥32 MiB 文件的分片上传），互为掩护。

**发现方式**　两条都不是靠读代码找到的，是**按「把能力接上生产路径」的直觉去实测**
撞出来的。起点是一个**已知的洞**：CAS 只在 `/presign-put` 上，
而 `TestLiveUploadPathDoesNotUseCAS` 早就把「Upload 不带版本」记成了可复现事实。
本轮的问题是「那分片那条路呢」——**答案从来没人问过**。

### 缺陷一：分片路径完全不看 `expectedVersion`

`/upload-session/create` 复用的是同一个 `PutBo`，而 `PutBo` 里**本来就有**
`expectedVersion`，注释还写着「传了就做乐观并发校验」。
但 `uploadCreate` 只透传了 `space/path/size`，**那个字段被无声丢掉**。

实测（真实控制面）：已存在 `versionId=1` 的文件，送 `expectedVersion=1000` 建会话
→ **HTTP 200**。比「不支持」更坏的是「假装支持」：
调用方拿到 200，会认为校验已经做过，于是放心覆盖别人的修改。

**影响面比单请求路径更大**：`≥32 MiB` 的文件**只走这条路**
（`multipartThreshold = 32 MiB`，见 `upload_session.go`）。
也就是说——**文件越大越没有保护**，而大文件恰恰是误覆盖代价最高的那类。

**修复**

| 位置 | 改动 |
| --- | --- |
| `es_upload_session` | 加 `expected_version BIGINT`（可空 = 旧调用方不校验，向后兼容） |
| `DriveUploadService.create` | 接收并**寄存**版本（不判定——见下） |
| `DriveUploadService.complete` | 校验，且**校验先于 S3 聚合** |
| `DriveFileService.assertVersion` | 抽出「只校验不登记」，给出可读的错误归属 |
| Go `UploadFileIfVersion` / `UploadIfVersion` | 与单请求同一套语义的入口 |

**⚠️ 校验点必须在 complete，不能只在 create**：Multipart 的真实写入发生在
complete（大文件可能几分钟），create 时校验只能证明「开始时版本一致」，
对「写入时是否已被别人改动」毫无保证——那是一种**虚假的安全感**。
版本因此随会话寄存在服务端，等字节真正要落地时再比。

**⚠️ 顺序是正确性所在（本轮自己踩到并修正）**：第一版把 `assertVersion`
写在 `storage.completeUpload` **之后**。但 `completeUpload` 会把分片聚合到
目标键上——**聚合即覆盖**。先聚合再校验，对方的改动已经被物理覆盖，
此时报「版本冲突」只是事后通知：数据没了。
顺序必须是「先占版本（条件 UPDATE），再让字节落地」。
这一条由 `completeWritesToObjectStorageOnlyAfterVersionGatePasses`
用 `InOrder` 钉住；**已验证回退顺序即 3 条用例转红**。

### 缺陷二：分片路径的两个雪花 ID 没跟上 KI-18

修完 CAS 后，同一个用例立刻撞出第二条：

```
解析云盘数据失败：json: cannot unmarshal string into Go struct field
sessionCreateData.sessionId of type int64
```

`sessionCreateData.SessionID` 与 `sessionCompleteData.FileID` 都是**雪花 ID**，
必然超 JS 安全整数，被 `BigNumberSerializer` 序列化成**字符串**——
这正是 KI-18 的形状，**而 KI-18 修 `Object`/`RecycleEntry` 时漏掉了这个文件**。

后果：**分片上传（≥32 MiB）在真实控制面上从来没跑通过**。
单测里 `sessionId` 是自己编的小数字，永远撞不到。
这与 KI-18 的教训是同一个：**输入是自己编的，与真实契约不符**。

**修复**：两个字段改用 `drive.FlexInt64`（两种形态都收）。

**验收（真实控制面 + PostgreSQL + RustFS，非 mock）**

| 判据 | 结果 |
| --- | --- |
| 33 MiB 文件走真分片首传（`expectedVersion=0`） | ✅ 成功，回填 `versionId=1` |
| 用过期版本再传（`expectedVersion=1000`） | ✅ **被拒**：「文件已被其他人修改（你看到的版本 1000，当前版本 1）…」 |
| 被拒后对象内容 | ✅ **仍是 'A'**——没被覆盖（「报了错」不等于「没写」） |
| Java 用例 | **84**（+5：寄存/兼容/先校验后落盘/闸门拒绝时中止会话/顺序断言） |
| 活栈用例 | `TestLiveMultipartRejectsStaleVersion`（真分片 + 真对象存储，4.2s） |

**顺带更正一处说法**　路线图切片 1 此前写着「静默覆盖被消灭」。
准确的说法是：**单请求路径上被消灭了；分片路径直到本轮才消灭**。
「接口已支持」与「生产路径在用」是两件事——本仓在这个差别上
已经栽过两次（KI-18 的输入自编、本条的能力未接线）。

**关联**　[`known-issues.md`](known-issues.md) KI-18（同一形状的漏网之鱼）、
KI-15 / KI-19（测试与验收可信度家族）；
[`plans/2026-10-16-drive-production-roadmap.md`](plans/2026-10-16-drive-production-roadmap.md) 切片 1；
`internal/drive/live_multipart_cas_test.go`。


---

## KI-21：雪花 ID 跨 Wails 边界变回 JSON number，被 JS **静默截断**

**状态**　**已修复（2026-10-16）**。

**现象**　回收站「还原」与「彻底删除」**对任何真实文件都失败**。
不是报权限、不是报冲突，而是后端压根找不到那一行。

**根因**　`RecycleEntry.FileId` 是雪花 ID（19 位，如 `2110992378747158530`），
以 **JSON number** 交给前端，而 JS 的 `number` 只能精确表示到 `2^53-1`——超出部分**静默截断**：

```
后端发出 JSON : {"fileId":2110992378747158530}
前端持有      : 2110992378747158500     ← 变了（+30）
回传 JSON     : {"fileId":2110992378747158500}
库里真实值    : 2110992378747158530     ← 查不到这一行
```

**这不是理论风险，是纯确定的算术**：19 位整数在 JS 里只有字符串能无损表示。

**为什么 KI-18 修过"雪花 ID"却漏了这里**　KI-18 修的是 **Go ← 控制面** 方向
（`FlexInt64` 让 Go 能收下服务端的字符串）。本条是**前端 ← Go** 方向：
Go 侧老老实实把 `int64` 交出去，Wails 用 `encoding/json` 编码成 **number**
（已核对 `internal/binding/boundMethod.go` 用 `json.Unmarshal`/`Marshal`），
JS 一收就丢精度。**同一个"雪花 ID 不能当 number"的道理，
在相反方向上是另一个缺陷**——修好一侧不代表另一侧自动成立。

**量化（实机，不是推演）**　取本机库里全部 13 行真实 `file_id`：

| 项 | 数 |
| --- | --- |
| 经一次 JS number 往返**保持不变** | **0** |
| 被改变 | **13（全部）** |
| 其中撞到**另一真实行**的 | 0 |
| 其中变成**查不到**的 | 13 |

诚实结论：当前 13 行里没有出现"撞到别人那一行"的样本，
所以现实后果是**操作失败**（安全一侧），而不是"删错文件"。
但**这靠的是巧合而非设计**——只要 ID 分布凑巧，就会变成误操作对象。
安全边界不该建立在"碰巧没撞上"上。

**修复**　JS 边界上只用**十进制字符串**：

| 位置 | 改动 |
| --- | --- |
| `drive.RecycleEntryView`（新） | 前端形态，`FileId string`；`Size`/`VersionId` 仍为数值（不参与寻址、且量级安全） |
| `RecycleEntry.View()` | 雪花 ID → 十进制字符串 |
| `drive.ParseFileID(raw string)` | 字符串 → int64；**非法值报错而不是返 0** |
| `App.CloudRecycleList` | 出参改 `[]drive.RecycleEntryView` |
| `App.CloudRecycleRestore/Purge` | 入参改 `string`，先 `ParseFileID` 再下发 |
| 前端 `RecycleEntry.fileId` | `number` → `string`（Wails 重新生成绑定后为 `arg1:string`） |

`ParseFileID` **刻意不把非法值当 0**：0 在控制面语义上是「未登记」，
吞掉错误继续走就等于拿 0 去查库/删东西——宁可报错。

**验收**

| 判据 | 结果 |
| --- | --- |
| `TestLiveRecycleFileIDSurvivesJSBoundary`（活栈，真实雪花 ID） | ✅ `2110992378747158530` 无损往返 |
| 「回退即失败」验证 | ✅ 把 View 改回「经浮点截断」的等价行为后，用例立刻报 `…528 vs …530` |
| `TestParseFileIDRejectsGarbage` | ✅ 空/非数字/0/负数/小数一律拒绝 |
| 活栈全量（drive + account） | ✅ 全绿 |

**方法论**　这是「**同一个道理在相反方向上仍是新缺陷**」的实例。
KI-18 让 Go 能*收下*字符串，本条要求 Go 能*交出*字符串——
两个方向共享同一个前提（雪花 ID 超出 JS 安全整数），
却是两处独立的代码。只修一侧会让"雪花 ID 已处理"这句话**看起来成立**。

**关联**　[`known-issues.md`](known-issues.md) KI-18（同一前提的反方向）、
KI-20（同类：分片路径也是"看起来已修"）；
`internal/drive/client.go`（`RecycleEntryView` / `ParseFileID`）；
`internal/drive/recycle_id_boundary_test.go`。


---

## KI-22：`SetFileVisibility` 用 `[]int64` 收部门 ID，多个部门会**塌成一个**

**状态**　**已修复（2026-10-16）**。当前**无活跃调用方**，属**预防性修复**——
按「同一缺陷类别是否还有别的落点」扫出来的第三个实例。

**现象（机制已实测复核）**　部门 ID 也是雪花 ID。本机 `sys_dept` 里是 18 位：

```
1761000000000000100  XXX科技
1761000000000000101  深圳总公司
1761000000000000102  长沙分公司
1761000000000000103  研发部门
1761000000000000104  市场部门
1761000000000000105  测试部门
```

经一次 JS `number` 往返（Wails 用 `encoding/json` 编码，JS 端 `number` 只有 53 位精度）：

```
1761000000000000100 -> 1761000000000000000
1761000000000000101 -> 1761000000000000000
1761000000000000102 -> 1761000000000000000
1761000000000000103 -> 1761000000000000000
```

**4 个不同部门塌成同一个值**（本机 4/4 全部）。

**为什么这个坏法比 KI-21 更隐蔽**　KI-21 是「查不到那一行」，会**报错**——
用户看得见。本条是「用了一个**错的但合法**的 ID」：
`visibleDepts` 变成一个别的部门，服务端**照常 200**。
用户以为「只给研发和市场看」，实际变成「给某个不相干的部门看」，
**没有任何错误提示**。这是权限边界上的静默错误。

**为什么是预防性修复**　`SetFileVisibility` 目前**没有前端调用方**
（`rg` 全仓确认）。所以现在没有真实损害。
但它是 `App` 上的导出方法 = 已经在 Wails 绑定里
（`SetFileVisibility(arg1, arg2, arg3: Array<number>)`），
任何前端代码或插件写一行就能碰到。修掉它的成本是几分钟，
而留着它的成本是「某天有人接上时静默越权」——**代价不对称，取便宜的一侧**。

**修复**

| 位置 | 改动 |
| --- | --- |
| `App.SetFileVisibility` | `deptIDs []int64` → **`[]string`** |
| `drive.SetFileVisibilityByRef`（新） | 收字符串，逐个 `ParseFileID` 后转 int64 下发；非法值报错 |
| `drive.SetFileVisibility` | 保留（Go 内部调用方传 int64 仍可用） |

**顺带统一了表示法**：前端**已有的**部门契约本来就是字符串
（`frontend/src/types/core.ts` 的 `Dept.deptId: string`）。
`SetFileVisibility` 用 `[]int64` 是同一件事的第二套表示法——
现在两边一致，不再有「同一个部门 ID 在契约里有两种类型」这种坑。

**验收**

| 判据 | 结果 |
| --- | --- |
| `TestVisibilityDeptRefsRejectSnowflakeCollision` | ✅ 先断言测试数据确实超 JS 安全整数（否则用例无意义），再复现 4/4 塌缩，最后守住字符串逐位无损且互不相同 |
| `ParseFileID` 拒绝非法值 | ✅ 复用 KI-21 的守卫（含 0/负数，避免静默当成「未登记」） |
| Wails 重新生成绑定 | ✅ `Array<string>` |

**方法论**　这条不是靠「读代码找 bug」找到的，是靠**把一类缺陷的判据写下来，
再拿它去扫所有同类落点**：
「凡是雪花 ID 跨越 JS 边界的地方」→ 逐个检查 → 三个实例（KI-19/20/21 之外）
→ 前两个已有实际损害，这个是**潜在**的。
区别对待是对的（一个是修复、一个是预防），但**都要修**：
留着的代价与修掉的代价差着一个数量级。

**关联**　[`known-issues.md`](known-issues.md) KI-18（Go ← 控制面方向）、
KI-21（前端 ← Go 方向，同类同向）、KI-20（分片路径）；
`internal/drive/client.go`（`SetFileVisibilityByRef`）；
`internal/drive/recycle_id_boundary_test.go`。


---

## KI-23：分片上传的挂死连接**永远不失败**，重试逻辑因此形同虚设

**状态**　**已修复（2026-10-16）**。

**发现方式**　补 KI-21 的活栈回归时，`TestLiveMultipartRejectsStaleVersion` 开始
**间歇性卡死**：单独跑多数能过，连跑必挂。第一反应是「测试不稳定」——
按本仓的规矩（KI-15 / KI-19）先证伪，结果**它每次都卡在同一处**：
`putPartWithRetry` 等一次分片 PUT，给 3 分钟也不返回。

**量化（实测，本机控制面 + RustFS，33 MiB / 5 片）**

| 条件 | 结果 |
| --- | --- |
| 正常 | 整包 **~0.9s**（单片 ~150ms，5 片） |
| 异常 | **某一片永久停住** |
| 连续 6 次 | **2 次卡死**，分别卡在第 4 片与第 1 片 |
| 卡住时给 3 分钟 | **仍不恢复**（`context` 超时才被强行中断） |

**根因：重试的触发条件永远等不到**

```go
resp, err := c.transfer.Do(req)   // transfer 客户端超时 = 30 分钟
if err != nil {
    lastErr = err
    continue                       // ← 重试只在这里发生
}
```

- `transfer` 的 **30 分钟**是给「整次传输」的（可能几十 GB）；
- 对**一个 8 MiB 分片**是**上万倍的余量**——健康时它只要 150ms；
- 连接挂住时 `Do()` 会一直等到 30 分钟才返回错误；
- 于是 `partUploadRetries = 3` **一次都轮不到**，用户看到的是
  「上传进度条永远不动」，而不是「失败后自动重试」。

**这是「防御措施存在但不生效」的典型**：代码里明明有三次重试、
有指数退避，读起来很稳妥；但**触发它的条件被另一个更宽松的超时挡在外面**。

**修复过程中的一次自我更正（值得留档）**　第一版我给每个分片加了
**1 分钟的总时长上限**：能解决挂死，跑 8 次全过（此前 6 次挂 2 次）。
但复核时发现它对**慢链路**是错的：

- 8 MiB 分片在 1 Mbps 链路上要 **60 多秒**，正好撞线；
- 而重试是**从头再传这一片**——于是**越慢越传不上去，永远完不成**。

「挂死」与「慢」的差别**不在花了多久，而在还有没有字节在动**。
所以最终改成**停滞检测**（`stallReader`）：读侧每成功一次就重置计时器，
彻底不动（30 秒无进展）才判定连接死亡并触发重试。
健康单片 150ms，30 秒是 **200 倍**余量；而它只要求「有字节在流动」，
慢链路因此不受影响。

**验收**

| 判据 | 结果 |
| --- | --- |
| `TestStallReaderKeepsSlowButMovingTransfer` | ✅ 161ms 的慢传输在 20ms 停滞窗口下**不被误杀**（fixture 算术先自证前提成立） |
| `TestStallReaderFiresWhenNoProgress` | ✅ 彻底不动时**必须**触发（否则挂死无解） |
| 连续 8 次分片上传 | ✅ **8/8** 通过（修复前 6 次挂 2 次） |
| 全部活栈用例 | ✅ 通过，整轮 **7.3s**（修复前该项单独就要 60~300s） |
| Go 全量 + gfmt | ✅ 全绿 |

**方法论**　这是本仓第三次「**偶发失败其实是确定性缺陷**」：
KI-15（并发写）、KI-19（300ms 探测预算）、本条（超时预算吃掉重试）。
三次的路径完全一致——先把「偶发」当线索而不是噪音，
再证明它在放宽约束后仍然失败。区别在于：这次**修复本身也踩了一脚**，
而「慢 ≠ 挂死」这条判据是靠**想清楚重试的语义**（从头再传）
而不是靠跑测试发现的。**通过测试不等于判据正确**——
8/8 全绿的第一版依然是错的。

**关联**　[`known-issues.md`](known-issues.md) KI-15 / KI-19（同一族）；
KI-20 / KI-21（本轮同一批发现的雪花 ID 问题）；
`internal/drive/upload_session.go`（`partUploadStall` / `stallReader`）；
`internal/drive/stall_test.go`。



---

## KI-24：单请求上传（< 32 MiB）既无停滞检测也无重试，且**重试方式本身有个坑**

**状态**　**已修复（2026-10-16）**。

**发现方式**　KI-23 修完分片路径后，顺着问了一句：
**另一条上传路径（< 32 MiB 走单请求）是不是也有同样的毛病？**
实测（本机控制面 + RustFS，8 MiB，跑 8 次）：

| 次数 | 结果 |
| --- | --- |
| 7 次 | 成功，~170ms |
| 1 次 | **挂死**——给 60 秒也一动不动，只能靠外部 context 打断 |

单请求路径原本**既没有停滞检测、也没有重试**：一次 `transfer.Do()` 挂在
30 分钟的客户端超时上。也就是说它比 KI-23 那条更差——
分片路径至少有三次重试（虽然触发不了），这条连重试代码都没有。

**修复方案里最关键的一点：重试必须复用同一个预签名 URL**

这不是风格问题，而是**会破坏 CAS 语义**的功能性问题。控制面在
**签发预签名时就登记并推进版本**（`registerOnPresign`）。实测确认：

```
连签两次、一个字节都没写  →  数据库里 version_id = 2
```

所以「重试时重新签发」看起来更稳妥（新 URL、新的有效期），实际会：

1. **版本虚增**——一次上传推两次版本，CAS 的「我看到的第 n 版」随次数漂移；
2. **旧 URL 仍在有效期内且指向同一对象**，两条路径都能写，语义更乱。

正确做法：**只签一次，重试复用那个 URL**。登记只发生一次，版本只推进一次。
（分片路径不受此约束：每个 part 有独立 URL、不做版本登记，重签是安全的。）

**修复**　抽出 `putURLWithRetry`（分片与单请求**共用同一套停滞判据**），
单请求路径改为「签一次 → 在同一个 URL 上重试 3 次」，并保留 `singlePutRetries`。

**⚠️ 第一版用例是错的（值得留档）**

第一版用例走 happy path：注入什么都没做，第一次尝试就成功返回。
**把实现回退成「每次重签」的错误版本，它依然通过**——因为根本没走到重试分支。
「测试通过」在这里只说明「第一次尝试成功」，与「重试用哪种 URL」毫无关系。

改法：加一个**测试专用注入点** `SetTransferTransportForTest`，
用一个 `RoundTripper` 让**第一次 PUT 必失败**，强制走重试；
断言上传仍成功、内容正确、且**版本正好为 1**。

再验证一次：把实现改回「在重试循环内重新签发」（即真实错误形状），
用例立刻报：

```
一次单请求上传（含一次重试）后版本应为 1，实得 2
——说明重试重新签发了预签名 URL，版本被虚增（CAS 语义会失真）
```

**这里踩了两次**：第一次「验证用例能失败」时，我把回退写成
「签一次、再调一个内部自带循环的函数」——那不是错误形状，用例自然还通过；
第二次才写成「**在重试循环内**签」，这才真正等价于那个错误。
**「回退即失败」本身也要验证回退是否真的回退了**。

**验收**

| 判据 | 结果 |
| --- | --- |
| `TestLiveSinglePutRetryDoesNotInflateVersion` | ✅ 注入一次传输故障 → 自愈成功、内容正确、**版本正好 1** |
| 回退验证 | ✅ 改成「循环内重签」后立刻报「实得 2」 |
| Go 全量 + gofmt | ✅ 全绿 |
| 活栈全量（drive + account） | ✅ 通过 |

**关联**　[`known-issues.md`](known-issues.md) KI-23（分片路径的同一族缺陷，本条是它的镜像）、
KI-15 / KI-19（同为「偶发失败实为确定性缺陷」）；
`internal/drive/client.go`（`putURLWithRetry` / `SetTransferTransportForTest`）；
`internal/drive/upload_session.go`（`uploadStall` / `stallReader`，两条路径共用）。


---

## KI-25：活栈用例的清理写在函数末尾，**测试一失败就清不掉**

**状态**　**已修复（2026-10-16）**。

**现象**　本机库里累积了 **30 个孤儿对象 + 32 行 es_file（31 条 recycled + 1 条 active）
+ 1 条 uploading 会话**，全部属于 `live-*` / `live-cas/*` 路径。
用户打开回收站能看到一屏测试垃圾。

**根因**　5 个活栈用例**全部**把清理写在函数末尾，`t.Cleanup` 用了 **0 次**：

```go
func TestLiveX(t *testing.T) {
    ...
    _ = client.Delete(ctx, token, drive.SpacePersonal, path)   // ← 就在末尾
}
```

而活栈用例失败时多以 `t.Fatalf` 退出，**Fatalf 会立即终止当前 goroutine**——
末尾那几行**根本不会执行**。于是「清理」的真实语义变成了
**「只在一切正常时才清理」**，而恰恰是出问题时最需要它。
每失败一次就多叠一层垃圾，越跑越脏。

**修复**　新增 `cleanupPath`，用 `t.Cleanup` 注册：

- 用**独立 context**（`context.Background()` + 超时）：测试自己的 ctx 可能已超时/取消，
  用它清理会立刻失败，等于没清；
- 先 `Delete` 再遍历回收站 `RecyclePurge`（软删只改 state，行还在）；
- 清理失败**只记日志不 panic**：清理是收尾，不该把「原本的断言失败」盖成另一个错。

5 个用例全部改为注册式清理，函数末尾的清理代码删除。

**验证（关键：只有失败路径能证明它）**

| 判据 | 结果 |
| --- | --- |
| 故意 `t.Fatalf` 的临时用例（上传成功后立即失败） | ✅ 失败后库里**该路径 0 行残留** |
| 一次性清理脚本走真实 API（RecyclePurge 同时删行与对象） | ✅ 清掉 **31 条**回收站残留 + 1 条 active |
| 清理后再查 | ✅ `live-*`/`live-cas/*` 残留 **0**；未完成会话 **0** |
| 活栈全量连跑 5 轮 | ✅ 5/5 通过，且**每轮结束无新增残留** |

> 为什么必须用「故意失败」来验证：happy path 本来就会走到末尾清理，
> 用它验证等于没验证——第一版正是如此（这与 KI-24 里
> 「happy path 测不出重试方式」是同一个形状的错误）。

**顺带记录一条未解释的间歇失败（不掩盖）**　修复过程中活栈全量有 **1 次 FAIL
（耗时 437s）**，之后**连跑 5 轮全部通过（9 用例全绿）**，未能复现。
按本仓规矩（KI-15/19/23 的教训：偶发失败是线索不是噪音），
**本条不结案**——已记在此处，下次再出现时按「它每次都卡在同一处」的思路查，
重点怀疑分片路径的停滞重试把单次运行拖长后触发整体超时。

**关联**　[`known-issues.md`](known-issues.md) KI-15 / KI-19 / KI-23 / KI-24
（同为「偶发 vs 确定性」与「happy path 测不出真问题」）；
`internal/drive/live_cas_test.go`（`cleanupPath`）。


---

## KI-26：CAS 拒绝时**会话不会被中止**，永久停在 `uploading`

**状态**　**已修复（2026-10-16）**。**这条是我自己上一轮改动引入的**。

**发现方式**　清点环境时发现「每跑一次活栈分片用例，库里就多一条
`uploading` 会话」——不是历史遗留，而是**每跑必增**。
对着 `complete()` 一看就清楚了。

**根因（一行 `try` 的位置）**　KI-20 给 `complete()` 加了两道 CAS 校验：

```java
fileService.assertVersion(...);        // ← 在 try 之外
try {
    fileService.registerOnPresign(...); // ← 只有这道被 try 包住
} catch (RuntimeException ex) {
    abortQuietly(prefix, session);      // 中止会话 + 清 S3 分片
    throw ex;
}
```

两道校验**都会**在版本过期时抛异常，但只有第二道在 `try` 里。
于是 `assertVersion` 一抛，就**跳过了 `abortQuietly`**：

- 会话永久停在 `uploading`（永远不会有 complete/abort 来推它）；
- S3 端已传的分片白占容量，没人回收；
- 同路径的下一次 `create` 还得先碰上 `abandonStaleSessions` 才走得下去
  ——等于把「当前请求的收尾」欠给了「未来某个不相关的请求」。

**实测**　跑一次「过期版本被拒」的分片上传：
修复前 **uploading 计数 +1**（且只增不减）；修复后 **保持 0**。

**修复**　把 `assertVersion` 也收进同一个 `try`。
两道校验共用一份收尾逻辑——这本来就是同一个语义（「版本没过，这次上传作废」），
不该因为异常来自哪一行而走不同的收尾路径。

**验收**

| 判据 | 结果 |
| --- | --- |
| `completeAbortsSessionWhenAssertVersionRejects`（Java 单测） | ✅ 断言 `abortUpload` 被调用、对象存储未被写、会话状态变 `ABORTED` |
| **回退即失败** | ✅ 把 `assertVersion` 移回 `try` 外，**只有这一条**转红（其余 13 条仍绿） |
| 真机验证（重建 jar + 重跑） | ✅ 跑前 1 条 uploading → 跑后 **0 条** |
| Java 全量 | ✅ 14 个用例（+1），0 失败 |
| 活栈全量 | ✅ 8 PASS / 2 SKIP / 0 FAIL，跑后**零残留** |

**为什么值得单独记一条（而不是并进 KI-20）**　这是「**异常处理的边界画错一行**」
的典型：`try` 的范围看起来是细节，实际决定了「失败时会不会留下垃圾」。
而且它**只有在真机跑完整链路时才会显形**——单测里 `abortUpload` 是 mock，
调用没调用都不会让测试变红。KI-20 的用例覆盖的是「聚合前先校验」，
完全没有覆盖「校验抛异常之后谁收尾」。

**方法论**　这条是「清点环境」逼出来的：我本来只是想把测试残留擦干净，
结果发现残留**每跑必增**——**残留的增长率本身就是一条线索**。
「历史遗留」与「持续泄漏」是两码事，前者擦掉即可，后者得修根因。

**关联**　[`known-issues.md`](known-issues.md) KI-20（本轮引入它的那次改动）、
KI-25（同一次清点中发现；那条是测试侧清理，这条是产品侧收尾）；
`platform-drive/src/main/java/org/dromara/easyshare/drive/service/DriveUploadService.java`。


---

## KI-27：活栈清理在**跨包并发**下会偶发漏清（测试卫生，非产品缺陷）

**状态**　**已定位并记录**（2026-10-16）。**不是产品缺陷**——这一点已用实验排除，
但它会污染环境，所以必须留档。

**现象**　`go test ./internal/drive/ ./internal/account/ ./internal/spacedav/` 连跑时，
**偶发**留下一条残留（例如 `live-spacedav/roundtrip.txt` 仍为 `active`）。
连跑 6 轮：前 4 轮残留 0，**第 5、6 轮各留 1 条**（同样的路径，持续出现）。
单跑 `./internal/spacedav/` 连 4 轮则**零残留**。

**先排除产品缺陷（这一步是关键，不能省）**
用一个临时诊断直接问「`RemoveAll` 到底删没删」：
写入 → **立刻**（不等缓存过期）`RemoveAll` → 直接查控制面列举。

结果：**控制面里确实没有了**。即 `RemoveAll` 立即生效，**不是产品缺陷**。
如果跳过这一步，看到「文件还在」很容易误判成「删除功能坏了」——
那会是本仓第 N 次「把症状当根因」。

**根因（测试侧）**　活栈用例的清理是「各自 `RecycleList` → 按路径 `RecyclePurge`」。
三个包**并行执行**时：

1. 每个清理的花名册是**自己那一刻查出来的快照**；
2. A 包的清理软删了文件、B 包同一时刻的 `RecycleList` **可能还没看见那一行**；
3. 于是 B 认为「没我的东西」直接结束，而那条行就留在了回收站/active。

这与 KI-25 是同一族（清理不可靠），但**成因不同**：KI-25 是「失败路径不执行清理」，
本条是「清理查的是快照，并发下可能查漏」。两者叠加才解释了为什么
「修了 KI-25 之后仍然偶尔有残留」。

**为什么不急着修**　它只影响测试环境的整洁，**不影响用户功能**；
而稳健修法（清理改为「重试直到自己的路径确实不在」）需要给每个用例加轮询与上限，
改动面比收益大。**已如实记录**，下次再遇到残留时不必重新查一遍。
留给未来的做法：把清理做成「按前缀反复扫 + 有上限重试」，
或在活栈跑完后加一步独立的全量清扫。

**方法论**　这条的价值在于**把「先排除产品缺陷」这一步显式写下来**：
看到「删了还在」的第一反应应当是「这是不是删除功能坏了」，
而验证它的成本只有一次实验。**先证明不是产品缺陷，再谈怎么修测试**——
顺序反了就会去修一个不存在的问题。

**关联**　[`known-issues.md`](known-issues.md) KI-25（同族：清理在失败路径不执行）；
`internal/spacedav/live_test.go`（`registerLiveCleanup`）、
`internal/drive/live_cas_test.go`（`cleanupPath`）。