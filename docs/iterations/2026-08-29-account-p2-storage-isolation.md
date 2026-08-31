# 2026-08-29 账号控制面 P2：按用户隔离的存储授权

> 关联：[ADR-0007](../adr/0007-account-control-plane-ruoyi.md)、[P1 桌面登录](2026-08-29-account-p1-desktop-login.md)、[KI-2](../known-issues.md#ki-2云端凭据编译期硬编码且可从二进制明文提取)、[KI-3](../known-issues.md#ki-3对象-key-直接使用文件名无身份与用户隔离)

## 用户问题

P1 打通了登录态，但云盘还是 P0 之前的样子：所有人共用一个平铺命名空间。用户要的是「A 登录后只看到 A 的文件，B 看不到 A 的」。这背后是两个高危缺陷——客户端二进制里带着 RustFS 长期凭据（KI-2），对象键不含任何身份前缀（KI-3）。

## 目标与非目标

### 目标
- 客户端**不再持有任何 RustFS 凭据**；字节传输凭控制面按登录用户签发的短期预签名 URL 完成。
- 对象键落在 `users/{userId}/` 命名空间下，前缀由控制面强制，客户端只认相对路径。
- 桌面端云盘的列举/上传/下载/删除/预览全部改走控制面。
- 跨用户隔离可复现验收，而不是「看起来对」。

### 非目标
- **不做**按登录用户命名空间的资源管理器挂载（原 19081 云盘 WebDAV 本轮下线，理由见下）。
- **不做**长效可撤销分享链接（控制面统一约束预签名有效期，`CloudShare` 暂不支持自定义小时数）。
- **不做**稳定文件身份 `fileId + versionId`（KI-3 的完整形态，仍属 C2 切片）。
- **不做**管理员入口 / 注册开关（P3）。

## 完成记录

### 控制面（Java）

新增模块 `platform-drive/`（顶层、纳入版本管理，刻意不放进被 gitignore 的 `platform/`），包 `org.dromara.easyshare.drive`：

| 文件 | 职责 |
| --- | --- |
| `DriveKeys.java` | 纯函数，安全核心：命名空间前缀、相对路径归一、绝对键换算 |
| `DriveStorage.java` | 懒加载 S3 客户端/签名器，`presignPut/presignGet/list/delete` 一律收相对路径 |
| `DriveController.java` | `@SaCheckLogin` 类级鉴权，四个端点 |
| `DriveProperties.java` | `easyshare.drive.*` 配置绑定 |
| `DriveObject.java` | `record(path, size, lastModified)` |
| `DriveKeysTest.java` | 13 个用例，含 `users/1` 与 `users/11` 前缀相似的边界 |

四个端点 `GET /objects`、`POST /presign-put`、`POST /presign-get`、`DELETE /object` 一律从 `LoginHelper.getUserId()` 取身份，**不接受任何客户端传入的用户标识**。`normalizeRelative` 拒空串、拒前导 `/`、拒尾随 `/`、折叠 `//`、逐段拒 `""`/`.`/`..`、拒 `< 0x20` 与 `0x7F` 控制字符。

配置 `deploy/ruoyi-db/easyshare-drive.yml` 只写 `${RUSTFS_ACCESS_KEY}` / `${RUSTFS_SECRET_KEY}` 占位；真值只在被 gitignore 的 `deploy/rustfs/.env`，由启动脚本 source 进进程环境。

AWS SDK Java v2 需 `forcePathStyle(true)` 与 `RequestChecksumCalculation.WHEN_REQUIRED`——RustFS beta 不接受 CRC32 分块校验和。预签名**刻意不签 `Content-Type`**，这样客户端可自由带该头而不破坏签名。

### 桌面端（Go）

- **新增 `internal/drive/`**：`client.go`（`Objects`/`PresignPut`/`PresignGet`/`Delete`/`Upload`/`Open`）+ `upload.go`（`UploadFile` 带进度）。控制面调用与字节传输分两个 `http.Client`（15s vs 30min）。RuoYi 把业务错误放在 HTTP 200 的 body 里，故 `call` 按 `code` 而非 HTTP 状态判失败。14 个单测。
- **`app.go`**：新增 `driveClient()`（未配置控制面地址或未登录直接拒）与 `uploadClients()`（一次取齐 Core 与控制面两个客户端）。云盘六个方法全部改走控制面；**字节直传 RustFS，任务进度仍写 Core**（ADR-0007 不变量 3）。`CloudPreview` 里图片/PDF 交给 WebView2 直接加载预签名 URL，文本经新增的 `cloud.FillTextPreview` 限量内联。
- **删除 `internal/cloud/defaults.go`**，并从 `cmd/core/main.go` 摘掉 `s3store` 构造——Core 不再持有任何 RustFS 凭据。

### 19081 云盘挂载随之不可用（本轮的代价，非"顺手清理"）

**因果必须写清楚：是移走凭据导致挂载失去数据源，不是挂载本身多余。**原 `cmd/core/main.go` 用同一组静态凭据起了个云盘 WebDAV（19080+1），以 bucket 根为挂载点。修 KI-2 把凭据从 Core 拿走后，它没有数据源可用；同时键迁到 `users/{userId}/` 之后，以 bucket 根挂载会把所有用户的空间暴露成一个顶层 `users` 目录，语义也已经错了。

因此本轮：Core 不再启动云盘 WebDAV；`registerNamespace` 只注册「共享」，并主动 `Unregister` 旧的「网盘」条目，避免留一个双击进不去的死快捷方式。

**这是一个未经确认就缩减了用户可见功能的范围决定，属流程错误。**正确做法是动手前说明「修 KI-2 会让网盘挂载不可用，需等空间授权模型定稿后按账号重做」，由决策方选择本轮是否接受该代价。

**为什么它不能简单"按登录用户重做"就完事**：挂载要绑账号，而按产品意图，**用户空间由管理员设置、共享空间的权限由管理员把控**。这意味着挂载依赖一个"谁能挂哪个空间、配额多少"的授权模型，而该模型的载体是 P3 的管理面板。ADR-0007 目前只写到「管理员界面 = RuoYi 自带后台，客户端仅加入口按钮」，**没有覆盖空间授权与配额这一层**——这是 P3 需要补的设计缺口，不是照搬 plus-ui 能解决的。故挂载重做的前置是 P3，而非 P4 的界面开关。

### 顺带修掉的两条 KI

- **KI-1**（`cloudEnabled` 不反映可达性）：该字段原先只表示「Service 对象是否构造过」，恒为 true。Core 已不直连对象存储、无从判断，改由桌面端在 `GetSnapshot` 里按「配了控制面地址 + 已登录」覆盖。
- **KI-4**（预览测试依赖本机注册表 MIME）：按该条「修复方向」的第 2 条，把断言从具体 `text/plain` 放宽为 `text/` 前缀，保留对 `Kind` 与内容的断言。**该失败在未改动的基线副本上同样复现**，与本轮改动无关。

### 凭据来源收敛

KI-2 原文记「凭据分散在三处且必须保持一致」。现在 `internal/cloud/defaults.go` 已删、`scripts/create_bucket.go` 改为从环境变量读，**唯一来源是 `deploy/rustfs/.env`**，那条三处同步的约束随之作废。

## 验证结果

```text
go build ./... / go vet ./...                     通过
go test ./...                                     全部通过
前端 npx vitest run                                19 通过
前端 npx vue-tsc --noEmit                          干净
wails build                                       成功（11.5MB）

scripts/verify-drive-isolation.sh（活栈）           9 通过 / 0 失败
  A 换预签名并直传 RustFS                            ✓（凭据未下发）
  A 能在自己空间看到该文件                            ✓
  B 的列表不含 A 的文件                              ✓ KI-3 隔离
  B 用同名相对路径落到自己空间（404 而非 A 的内容）      ✓
  三种穿越路径全被拒（../、../../users/{id}/、/）      ✓ 不变量 2
  未登录访问被拒(401)                                ✓
  A 能删自己的文件                                   ✓
```

KI-2 的硬证据——用 `.env` 里的真实凭据做字节搜索：

```text
easyshare.exe   11.5MB   含 AccessKey=False   含 SecretKey=False
core.exe        10.6MB   含 AccessKey=False   含 SecretKey=False
```

新增 `internal/drive/live_integration_test.go`：对活控制面跑真实 JWT → 上传 → 列举 → 直取 → 跨用户隔离 → 删除，补上单测 httptest 假桩覆盖不到的部分。默认跳过，需活栈时显式开启：

```bash
EASYSHARE_LIVE_DRIVE=1 EASYSHARE_LIVE_BASE=http://127.0.0.1:8090 \
EASYSHARE_LIVE_USER_A=admin EASYSHARE_LIVE_PASS_A=admin123 \
EASYSHARE_LIVE_USER_B=test  EASYSHARE_LIVE_PASS_B=666666 \
go test ./internal/drive/ -run Live -v -count=1
```

该测试额外断言**列举结果不得泄漏 `users/` 前缀**——命名空间对客户端不可见是 ADR-0007 不变量 2 的一部分。

## 踩的坑

1. **重复实现且更差**。我只读了上次会话最后 30 条消息（截至 19:03），没看文件 mtime——上次会话其实干到 ~19:23，`platform-drive/` 早已建好。我另写了 5 个 Java 文件塞进 `ruoyi-system`，还往 `sys_oss_config` 插了明文凭据行，违反不变量 1。已全部删除并 `DELETE FROM sys_oss_config WHERE config_key='rustfs'`。**教训：接续会话先比文件 mtime，别只读对话尾巴。**
2. **jar 不可执行**。上次会话只跑了 `jar` 没跑 `spring-boot:repackage`，产物 63 条目、无 `Main-Class`。`./mvnw -o clean package -DskipTests` 后 645 条目、`JarLauncher` 就位。
3. **验收脚本 JSON 被污染**。本机 curl 不解析 `-w` 里的 `\xNN` 转义，原样吐出 `\x1f` 混进 JSON（`od -c` 确认）。改为不用 `-w`，走 python 取字段。
4. **两个假 FAIL**。其一：RuoYi 业务错误是 HTTP 200 + body 里 `code:401`，我断言了 HTTP 状态。其二：Git Bash/MSYS2 把 argv 里的 `/iso-x.txt` 重写成 `D:/Git/iso-x.txt`，那条穿越用例其实从未被执行——产品代码一直是对的。改为经 stdin 传路径。
5. **父 POM 的 tag 过滤吃掉测试**。`ruoyi-vue-plus` 的 surefire 配了 `<groups>${profiles.active}</groups>`，隔离测试不带该 tag 会被跳过。`platform-drive/pom.xml` 用 `combine.self="override"` 覆盖。
6. **Maven reactor 跨出父目录**。`platform-drive` 在顶层而父 POM 在 `platform/`，靠 `<relativePath>../platform/pom.xml</relativePath>` + `<module>../platform-drive</module>` 串起来。

## 已知限制与待验收项

- **`CloudShare` 不遵守 `expiryHours`**：控制面统一约束有效期（`get-expiry` 默认 10 分钟）。长效外链需要控制面提供独立的可撤销、可审计的分享接口。
- **无单对象 stat 接口**：`CloudPreview` 取文件大小靠列举后匹配相对路径。当前量级可接受，对象多了要加端点。
- **Core 侧遗留死代码**：`/api/cloud/*` 七个路由、`internal/cloud/service.go`、`internal/cloud/webdavfs/`、`internal/drive/cloud_service.go` 已无生产调用方（`ConfigureCloud` 只剩测试在调），但仍在编译产物里。**这些路由只按对象键鉴权、不认用户身份，若被重新接上会绕过隔离。**已登记为 KI-5。
- **手工交互验收未做**：真机点登录 → 上传 → 切换账号看列表，需要真实鼠标操作。本轮的隔离结论来自控制面活栈验收与 Go 客户端集成测试，桌面端 UI 链路只验证了编译与类型。

## 回滚方式

改动集中在新增 `platform-drive/`、`internal/drive/`，以及 `app.go` 云盘方法与 `cmd/core/main.go` 的删减。回滚需同时恢复 `internal/cloud/defaults.go` 与 `cmd/core/main.go` 的 `s3store` 构造——但那会把 KI-2 一并带回，不建议。控制面侧只需不加载 `easyshare-drive.yml`，`platform-drive` 模块即空转。

## 后续

P3 管理员 + 注册开关（含 plus-ui 后台、管理入口按角色显隐）→ 设置页账号资料（改昵称/头像）→ P4 悬浮窗滑动开关 + 按登录用户命名空间的资源管理器挂载。

P4 挂载的技术路线已摸过底：`internal/cloud/webdavfs` 只用到 `objectstore.Store` 十个方法中的五个（`Put`/`Get`/`Head`/`List`/`Delete`），其中四个可直接映射到 `internal/drive` 客户端，`Head` 可由列举派生。两个待决问题：`Mkdir` 的语义（S3 零字节 `dir/` 标记与 `normalizeRelative` 拒尾随 `/` 冲突），以及挂载生命周期如何绑登录/登出。
