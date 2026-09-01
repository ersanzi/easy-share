# EasyShare 已知缺陷清单

> 登记已确认、尚未修复的实现缺陷。**只登记不复制**：细节和修复决策仍在各自的迭代记录、ADR 或代码注释中。
> 与相邻文档的分工：产品级能力边界见 [`../README.md`](../README.md) 的「当前限制」；阻塞下一步推进的事项见 [`progress.md`](progress.md) 的「已知阻塞」；可复现的环境故障与解法见 [`troubleshooting.md`](troubleshooting.md)。
>
> 最后更新：2026-09-01

## 登记表

| 编号 | 严重度 | 标题 | 影响面 | 状态 |
| --- | --- | --- | --- | --- |
| [KI-1](#ki-1cloudenabled-不反映存储可达性) | 中 | `cloudEnabled` 不反映存储可达性 | 前端状态展示 | 已修复（[P2](iterations/2026-08-29-account-p2-storage-isolation.md)） |
| [KI-2](#ki-2云端凭据编译期硬编码且可从二进制明文提取) | 高 | 云端凭据编译期硬编码，可从二进制明文提取 | 服务器化部署的前置阻塞 | 已修复（[P2](iterations/2026-08-29-account-p2-storage-isolation.md)） |
| [KI-3](#ki-3对象-key-直接使用文件名无身份与用户隔离) | 高 | 对象 key 直接使用文件名，无身份与用户隔离 | 多用户与文件版本的前置阻塞 | 部分修复（[P2](iterations/2026-08-29-account-p2-storage-isolation.md) 做完用户隔离；稳定文件身份仍未做） |
| [KI-4](#ki-4预览测试依赖本机注册表的-mime-映射) | 中 | 预览测试依赖本机注册表的 MIME 映射 | 阻断本机完整构建流水线 | 已修复（[P2](iterations/2026-08-29-account-p2-storage-isolation.md)，取修复方向第 2 条） |
| [KI-5](#ki-5core-侧云盘路由已无调用方但仍可绕过用户隔离) | 中 | Core 侧云盘路由已无调用方，但仍可绕过用户隔离 | 隔离边界的回归风险 | 已修复（[批次收尾](iterations/2026-08-31-account-plane-closure.md)） |
| [KI-6](#ki-6llm-供应商限流时-query-整体-500不降级纯检索) | 中 | LLM 供应商限流时 `/query` 整体 500，不降级纯检索 | 公司使用高峰的问答可用性 | 未修复 |

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
