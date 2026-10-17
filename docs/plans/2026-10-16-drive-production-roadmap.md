# 云盘生产级路线图 — 逆向对标收敛

日期：2026-10-16
状态：**待用户拍板**（本文件只出路线，不动代码）
依据：[`intelligence/2026-10-15-dingpan-file-storage-reverse.md`](../intelligence/2026-10-15-dingpan-file-storage-reverse.md)（钉盘，下称**钉盘取证**）、[`intelligence/2026-10-15-wps-cfapi-placeholder-reverse.md`](../intelligence/2026-10-15-wps-cfapi-placeholder-reverse.md)（WPS CfApi，下称**WPS 取证**）、[`wps-cloud-drive-benchmark.md`](../wps-cloud-drive-benchmark.md)（WPS 元数据）、[`cloudreve-benchmark.md`](../cloudreve-benchmark.md)（服务端架构）
方法论：[`toolchain-rea.md`](../toolchain-rea.md) §8

## 0. 一句话

两份取证独立指向同一组缺口，且**缺口之间有硬依赖**：没有**稳定文件身份 + 版本**，增量、冲突、回收站、占位符全都建在沙上。路线按依赖排，**不按"哪个看起来酷"排**——CfApi 占位符收益最大但排在最后，正因为它依赖前面全部。

## 0b. 中文口径

要让网盘"像成品"，先得让每个文件有个**不会变的编号**，再让服务端记住**每个编号改过几次**。有了这两样，才好做三件事：只取变化（不用每次重列整盘）、两边同时改时按规则合并（不静默覆盖）、删掉的能还原（而不是消失）。最后才是**本地只放空壳、点开才下载**——那是最显眼的一步，却必须建在前面的基础上。

反过来做（先上占位符）会得到一个"看起来很像网盘、但一改就乱"的东西。

---

## 1. 三份独立证据的交集

| 缺口 | 钉盘取证 | WPS 取证 | Cloudreve 对标 | 交集强度 |
| --- | --- | --- | --- | --- |
| **稳定文件身份 + 路径解耦** | `local_id`/`remote_id`，路径只是属性 | `fsFileId`（NTFS FileId）+ `cloudFileId` 双绑 | `fileId` | **三方一致**（我们已有 `fileId`，但写路径仍按 path） |
| **版本 / CAS** | `dentryVersion` + `preDentryVersion` | `file_version_state_table`（三值 + action） | — | **两方一致** |
| **增量列举** | `spaceVersion` + `listRedologs` + `nextToken` | `data_ver_table`（一表一版本号） | — | **两方一致** |
| **回收站 / 可还原删除** | 系统回收站 + `addDentryByHistory` | `recyclebin_metadata_table` + 双 parentId | P1 首项 | **三方一致** |
| **冲突策略** | 9+ 条写死规则矩阵 | — | — | 单方（但钉盘证据极硬） |
| **按需占位符** | CfApi placeholder | CfApi（`KCfApiSyncFolderEngine`） | — | **两方一致** |
| **内容哈希 / 秒传** | `md5` + `md5_reliable` + 服务端 `genMd5` | `fsha1` | — | **两方一致** |
| **错误三件套** | `TEC_*` 错误码家族 | `errorCode`+`errorMsg`+`winErrorCode` | §3.6 | **三方一致** |

**结论**：三方交集 = ①身份 ②版本 ③增量 ④回收站 ⑤哈希 ⑥错误可诊。**这六项是"生产级"的定义本身**，不是可选项。

---

## 2. 依赖图（为什么必须这个顺序）

```
[切片 1] es_file 加版本/哈希/状态列 + 写路径改走 file_id
   ├──> [切片 2] 增量列举（spaceVersion + 变更日志 + nextToken）
   │        └──> [切片 5b] 双向同步（需要"增量搬元数据"的能力 + 切片 3）
   ├──> [切片 3] 冲突策略（CAS 靠切片 1 的 version 列）
   ├──> [切片 4] 回收站 + 服务端 rename/move（靠切片 1 的状态列与身份）
   └──> [切片 6] 内容哈希与秒传（哈希列在切片 1 落地）

[切片 5] CfApi **只读占位符** —— 不依赖切片 1（见下方更正）
```

**切片 1 是「写语义」的硬前置**：切片 2/3/4/6 都改远端写入语义，没有稳定身份 + 版本，它们建在沙上。

> ⚠️ **2026-10-16 更正（本路线图自身的错误）**：初稿把**切片 5（只读占位符）也列为依赖切片 1**，这是错的。**只读占位符所需的最小输入，我们现有接口已经全有了**：
> - `CfCreatePlaceholders` 需要 `RelativeFileName` / `FileSize` / `LastWriteTime` / `FileIdentity`——
>   对应 `drive.Object` 现有的 `Path` / `Size` / `LastModified` / `FileId`（`internal/drive/client.go`，**四个字段一个不缺**）；
> - 占位符是**本地表现层**，不写远端、不做 CAS、不管冲突——`dentry_version` 对它没有用处；
> - `FileIdentity` 用 `FileId` 即可（WPS 用的是自己生成的 32 字节代理 ID，我们直接复用控制面的雪花 ID 更简单且天然稳定）。
>
> **结论：切片 5 可以立刻开工，不必等切片 1。** 真正依赖切片 1 的是 **5b 双向同步**（要按版本对账、要冲突策略）——所以切片 5 拆成 **5（只读，现在可做）** 与 **5b（双向，等 1+3）**。
>
> 这条更正也改变了排期建议：**若目标是"先让用户体验到生产级形态"，切片 5 + 切片 4 是收益最快的一组**（用户直接看得见/摸得着），而切片 1–3 是"看不见但必须做对"的底层。两条线**可以并行**（切片 5 动 `internal/namespace/` + 新增 `internal/cfapi/`，与切片 1 动的 `platform-drive/` + `internal/drive/` 不抢文件）。

> ⚠️ **与在制品的冲突提示**：用户工作区有一片未提交的「回收站 + 版本」在制品（`docs/iterations/2026-10-14-drive-recycle-bin.md`，`es_file` 已加 `state/deleted_at/origin_path/origin_parent_id/version_id`）。**该片的列设计正是切片 1 的一部分**，应先收尾该片、把它的列作为切片 1 的基线，**而不是另起一套列**。

---

## 3. 切片明细（每片：做什么 / 验收 / 依据）

### 切片 1 — 文件身份与版本列（**P0，唯一硬前置**）
> ### CAS 已落地（2026-10-16）
>
> 第 3 条「预签名 PUT 引入条件提交」**已实现并在真机验证**：
>
> - `POST /presign-put` 新增可选字段 `expectedVersion`（0 = 我认为文件还不存在，n≥1 = 我看到的第 n 版）；
> - `DriveFileService.registerOnPresign` 做两道校验：先比版本（快失败），再做**条件 UPDATE**
>   （`WHERE file_id=? AND version_id=?`）——后者是必需的，只靠先读后比仍有 TOCTOU 窗口；
> - `GET /objects` 的 `FileVo` 新增 `versionId`，客户端据此知道自己「看到的是哪一版」；
> - Go 侧 `drive.Client.PresignPutIfVersion` + `Object.VersionId` 已接线；
>   `PresignPut` 保持原签名（不校验），**升级不必同时上线**。
>
> **真机实测（控制面 8090 + PostgreSQL 16 + RustFS）**：
>
> | 步骤 | 结果 |
> | --- | --- |
> | A 首次写（`expectedVersion=0`） | 200 |
> | B 也带 0（同版本） | **500 拒绝**：「文件已被其他人修改（你看到的版本 0，当前版本 1）…请刷新后重试」 |
> | B 刷新列表拿到 `versionId=1`，重试 | 200，版本推进到 2 |
> | 不传 `expectedVersion` 的旧调用方 | 200（向后兼容） |
>
> 即：**静默覆盖被消灭**，用户得到可读冲突提示而不是无声的数据替换。
>
> ⚠️ **实现中踩到的一个语义坑（值得记）**：CAS 第一版把新行的 `version_id` 留成默认 0，
> 于是「我期望版本 0（认为文件不存在）」与「文件已存在且处于版本 0」**碰撞**——
> 两个客户端都带 `expectedVersion=0` 连续首次写入时，第二个**不会被拒**
> （真机实测：A 200、B 200）。修法是**首次登记直接写 `version_id=1`**，
> 让语义唯一：0 = 不存在，n≥1 = 第 n 次写入。
> 这个坑是「乐观锁用 0 兼作『不存在』哨兵」时的通病，单测不会暴露——
> 因为单测里 mock 返回的行是自己构造的，不会有真实的「首次写入后仍是 0」。
>
> ### 生产路径已接入 CAS + 冲突副本（2026-10-16 晚）
>
> 上表“接口已支持”与“客户端已接线”之间的最后一段空档已补上：`app.go` 两处生产上传
> （单文件/文件夹）与 `spacedav` 写路径全部改为**先取版本 → 上传带回版本**。
> 版本冲突时按切片 3 的固定规则自动落 `原名 (冲突副本).ext`（占用则递增），**双方内容都保留**，
> 不弹窗、不覆盖。配额/权限/网络错误不做改名重试，原样返回。
> 活栈验收：`TestLiveSamePathUploadKeepsBothVersions`——A 上传、B 用过期版本再传被拒、
> 落冲突副本、两侧内容都可读回。
>
> ### 分片路径的 CAS 与雪花 ID（2026-10-16 补）
>
> 上表「静默覆盖被消灭」当时**只对单请求路径成立**。本轮把同一个问题问到分片路径上
> （≥32 MiB 文件走的那条），撞出**两个叠加缺陷**：
>
> - **`/upload-session/create` 完全不看 `expectedVersion`**：`PutBo` 里本来就有这个字段、
>   注释还写着「传了就做校验」，但 controller 只透传 `space/path/size`，字段被无声丢掉。
>   实测送 `expectedVersion=1000`（当前版本 1）→ **HTTP 200**。
>   比「不支持」更坏的是「假装支持」：调用方以为校验过了。即**文件越大越没有保护**。
> - **分片路径的两个雪花 ID 漏了 KI-18 的修复**：`sessionCreateData.SessionID` /
>   `sessionCompleteData.FileID` 仍是 `int64`，而它们是雪花 ID、会被序列化成字符串。
>   后果是**分片上传在真实控制面上从来没跑通过**——单测里 sessionId 是自己编的小数字。
>
> 修复：`es_upload_session` 加 `expected_version`（可空=向后兼容）；版本随会话**寄存**、
> 在 **complete** 校验（写入发生在那儿，create 校验只能证明「开始时一致」）；
> **校验先于 S3 聚合**——`completeUpload` 聚合即覆盖，顺序反了就是「先覆盖、后报冲突」；
> 两个 ID 改用 `FlexInt64`。
>
> 验收：33 MiB 真分片首传成功；带过期版本再传**被拒且对象内容未变**；
> Java 84 条（+5，含 `InOrder` 顺序断言，回退顺序即 3 条转红）；
> 活栈 `TestLiveMultipartRejectsStaleVersion`（4.2s，真分片 + 真对象存储）。
> 详见 [`known-issues.md`](../known-issues.md) KI-20。
>
> **仍未做**（切片 1 剩余）：`content_md5` / `md5_reliable` 两个哈希列
> （属切片 6 的内容哈希与秒传），以及**写路径全面改走 `file_id`**
> （目前预签名/删除已支持 fileId，但路径仍是主键之一）。
>
> ### 缺口实测（2026-10-16，改造前）
>
> 本切片的缺口**不只是理论上的**——
> 在本机控制面 + RustFS 上按「两个客户端先后写同一路径」跑了一遍，结果是
> **静默覆盖**：客户端 A 写入 `AAAA-written`（PUT 200，无任何错误），
> 客户端 B 随后写入 `BBBB-wins!!`（PUT 200），最终内容只剩 B 的，
> **A 的内容连同任何提示一起消失**。这正是本切片要消灭的那个行为。
>
> 顺便实测确认了 `version_id` 的现行语义（此后 CAS 要建在它上面）：
> 首次登记为 **0**，此后每次 `presign-put` 单调 +1（实测 0→1→2→3）。
> 但**目前没有任何代码读它**——它只在回收站视图里被透出，
> 不参与任何判定，所以现在拦不住上面那次覆盖。
> 即：**计数是对的，但还没接到闸门上**。

**做什么**
1. `es_file` 补齐：`content_md5`、`md5_reliable`、`dentry_version`（单调递增，替代现有 `version_id` 的语义）、`state`（active/recycled/purged）、`deleted_at`、`origin_path`、`origin_parent_id`。
2. **写路径全部改走 `file_id`**：预签名 PUT / 删除 / 列举回填，不再以 `file_path` 为唯一身份（`uk_es_file_path` 已改部分索引，方向正确，继续）。
3. 预签名 PUT 引入**条件提交**：请求带"我看到的旧版本"，服务端不匹配则拒绝（对标 `preDentryVersion`）。

**验收**
- 两个客户端同时上传同名文件：**两份都在**（后者落冲突副本），无静默覆盖。
- 改名/移动后 `file_id` 不变。
- 单测：并发撞版本 → 冲突分支；同名覆盖 → `dentry_version` 单调递增。

**依据**：钉盘取证 §3（`preDentryVersion` CAS）、§9「条目身份」「远端版本」两行；WPS 取证 §5.1（`fsFileId`+`cloudFileId` 双绑）。

---

### 切片 2 进展（2026-10-17）

- ✅ `es_space.space_version` + `es_file_change_log` DDL 已落地（幂等可重复执行）。
- ✅ `DriveChangeLogService`：条件 UPDATE 推进空间版本，事务内写日志；
  created/updated/deleted/restored/purged/moved 六个生命周期点全部接入。
- ✅ `GET /easyshare/drive/changes?space=&sinceVersion=`：
  不带 sinceVersion = 全量重建 + `newestSpaceVersion`；带则只返回变更。
- ✅ Go `drive.Client.Changes()`。
- ✅ 生产挂载盘（`spacedav.FS.objects()`）已接增量：缓存 `lastVersion`，过期后先 `/changes` 再合并；旧控制面自动退全量。
- ✅ `last_space_version` **跨进程**持久化已做（`diskVersionStore`，`space-versions.json` 落数据根，重启不退全量）。
- ✅ 日志裁剪策略已做（2026-10-17）：`DriveChangeLogService.trim` 全量重建时保留最近 10000 条；
  日志不足时客户端退全量重建（接口已支持该形态，`spacedav` 的失败退路已在）。
- ⏳ 裁剪未按空间分别保留（当前全局条数）；未做定时任务（仅全量重建时顺手裁）。

### 切片 2 — 增量列举（**P0，性能天花板**）

**做什么**
1. 空间加 `space_version`（单调整数）+ 变更日志表。
2. 接口出 `nextToken` + `hasMore`（**当前完全没有分页参数**，`DriveStorage.java:272-279` 在 S3 内部翻 1000 条聚合成单 List）。
3. 客户端持久化 `last_space_version`，只拉"上次之后"的变更。
4. **日志不足时退全量重建**（对标 `changed too much` → `full_rebuild`）——这是长期离线后的唯一出路，必须一起做。

**验收**
- 改 1 个文件 → 客户端只收到 1 条变更，不重列整空间。
- 断开 30 天后重连 → 走全量重建且结果正确。
- 万级对象空间的目录列表响应时间不随总量线性增长。

**依据**：钉盘取证 §4（`listRedologs` + `nextToken` + `newestSpaceVersion` + 全量兜底 + `mark_space_version_conflict`）；WPS 取证 §5.6（`data_ver_table`）。

---

### 切片 3 — 冲突策略（**P1**）

**做什么**：按钉盘取证 §6 的矩阵落地至少四条核心规则（**规则写死，不弹窗问用户**）：
| 情形 | 规则 |
| --- | --- |
| 远端新建 vs 本地已有、内容相同 | 认作同一文件（哈希去重） |
| 远端新建 vs 本地已有、内容不同 | 保双方，本地加标记改名 |
| 本地改 + 远端改 | 本地生成冲突副本，两份都留 |
| 移动目标已存在 | 冲突副本，不覆盖 |

**验收**：两侧同时改同名文件 → 生成 `xxx (冲突副本)`，两份内容都可取回。

**依据**：钉盘取证 §6 全表。

> 注：钉盘取证 §11-3 已确认 **`conflictStrategy` 的取值域在服务端**（客户端只做通用 JSON 读入，无按值分派）——所以我们是**自定义策略集合**，不必对齐钉盘的具体取值。

---

### 切片 3 进展（2026-10-16 晚）

- ✅ **本地改 + 远端改**：生产上传路径（`app.go` 与 `spacedav`）已全部接 CAS；
  版本冲突自动落 `原名 (冲突副本).ext`，双方内容保留。
- ✅ **移动/改名目标已存在**：`spacedav.Rename` 识别服务端的“目标位置已有同名文件”拒绝，
  重新列举后自动落到 `新名 (冲突副本).ext`，双方内容保留。
- ⏳ 其余两条规则依赖内容哈希去重（`content_md5` / `md5_reliable`），归切片 6。

---

### 切片 4 — 回收站 + 服务端 rename/move（**P1，在制品已起步**）

**做什么**
1. 收尾在制品的回收站三件套（列表/还原/彻底删除）。
2. 新增**服务端 rename / move 端点**：WebDAV 层不再"下载 + 上传 + 删除"模拟改名（现状见 `internal/spacedav/spacedav.go` 的 `Rename`，注释自认目录改名直接拒绝）。
3. 补救「空目录只活在内存」：`pendingDirs` 是进程内存（`spacedav.go`），重启即丢——需要目录对象落地。

> **进展（2026-10-16）**
> - ✅ **第 2 项已落地**：`POST /easyshare/drive/move`（`CopyObject` + 改行 + 删源，
>   **字节不出对象存储、`fileId` 不变**）；`internal/spacedav.Rename` 优先用它，
>   无该能力时退回旧路。**这是软删引入的回归**——旧「上传新键 + 删源」在软删之后
>   每次改名都留一份回收站副本（空间翻倍），且换掉 `fileId` 使分享/索引引用全断。
>   详见 [`known-issues.md`](../known-issues.md) KI-12。
> - ⏳ **第 1 项**代码完成，**已于本机等价环境（PG 16 + Redis + RustFS + 控制面）完成端到端**：
>   软删 → 列表消失（KI-10）→ 回收站出现 → 还原（**fileId 不变**）→ 再软删 → 彻底删除（列表与回收站都不再有）。
>   由 `TestLiveRecycleRestoreAndPurgeRoundTrip` 覆盖（0.74s）。**仍未做**：公司服务器 192.168.1.239 上的联调（不在同网段）。
> - ⏳ **第 3 项未做**：需要目录对象模型（钉盘用 `local_id`/`parent_id` 树）。

**验收**
1. 资源管理器里改名是**原子操作**且保留 `fileId`；
2. 新建空目录重启后仍在；
3. 改一次名**空间占用不变**（不留回收站副本）。

> 第 1、3 条**已由活栈用例在真实控制面 + 对象存储上钉住**
> （`TestLiveMoveKeepsFileIdAndLeavesNoRecycleCopy`：move 后 `fileId` 逐位不变、
> 内容完好、**回收站不留副本**——旧「上传+删源」会空间 ×2）。
> 补这条的理由：这两个卖点此前**只在单测里验过**，而单测里 mapper 与 `CopyObject` 都是 mock，
> 恰好把「同名副本」「新 fileId」这类副作用挡住了（KI-12 当初就是这么漏的）。
> 第 2 条待目录对象模型。

**验收**：资源管理器里改名是**原子操作**且保留 `fileId`；新建空目录重启后仍在。

**依据**：钉盘取证 §9「服务端改名/移动」「目录对象」；WPS 取证 §10 回收站行；`wps-cloud-drive-benchmark.md` §6-P1。

---

### 切片 5 — CfApi 只读占位符（**P2，收益最大也最重**）

> **开工状态（2026-10-16）**：已建 `internal/cfapi` 包并**真机验证核心机制**——
> 同步根注册/注销往返通过；`CfCreatePlaceholders` 建出的占位符
> **逻辑大小 12345678 / 磁盘分配 0**（`go test ./internal/cfapi/ -v` 可复现）。
> 即「列表零下载」这半段成立。剩余：`CfConnectSyncRoot` + fetch 回调（「点开才拉」）、
> 登录态联动、列表→占位符编排。
>
> 实现时的两条纪律（都已写进 `internal/cfapi/README.md`）：
> ①结构体照 Windows SDK `cfapi.h` 誊写、**不手写填充字段**；
> ②期望值由 `testdata/layout_check.c` 用真实 C 编译器取，**不手算**。
>
> **进展（2026-10-16 晚）**：第 1–4 步**已全部落地并真机跑通**
> （`internal/cfapi` + `internal/cfmount`，含 fetch 回调与按需拉取）。
> **接入 `app.go` 前自查拦下一个数据丢失缺陷（KI-14）**：切片叫「只读占位符」，
> 但那个「只读」**没有任何执行层依托**——cfapi 只注册读回调，占位符又是
> `FILE_ATTRIBUTE_NORMAL`。实测用户编辑**成功**、改动**不上传**、
> 重建一旦正常跑就**被覆盖成 0 字节**。已修：占位符打 `FILE_ATTRIBUTE_READONLY`，
> 实测写入被拒且**水合不受影响**。
> 同时发现 `Sync` 注释声称「可重复调用」实测不成立（`ERROR_ALREADY_EXISTS`），
> **刻意暂不修**——它会掩盖上面的数据丢失（详见 KI-14 的时序陷阱）。
> **第 8 步（读取节流）已完成（2026-10-16）**：进程名单（逐字取自 WPS 取证 §3.5）
> + 窗口限流器，接在 fetch 回调的 `open` 之前。用户交互进程零延迟、
> 后台扫描与**未知进程**都限流（名单永不完整，取保守一侧）；单次延迟封顶 200ms
> 因为回调是同步的。11 条用例，实测用户读取 18ms 未被拖慢。
> **第 5 步（远端改动只更元数据）也已完成（同日）**：`CfUpdatePlaceholder`
> ——占位符已存在时改元数据而不是重建。这解开了 KI-14 的「两个缺陷互相掩盖」：
> 原先 `Sync` 不可重复（`ERROR_ALREADY_EXISTS`），而让它"可重复"的直觉修法
> （`SUPERSEDE`）会清掉已水合内容。实测 `CfUpdatePlaceholder` 不下载内容、
> 内容完好、只读保留。**踩坑**：首版用 `GENERIC_READ` 打开句柄，
> **仅打开就触发整份水合**——改用 `FILE_READ_ATTRIBUTES`（功能性错误，非权限洁癖）。
> **第 7 步（本地元数据表）仍未做**，是切片 5 剩下的最后一项。

**做什么**：按 WPS 取证 §9 的**八步最小闭包**实现（该节已把每一步对应到证据）：
1. `CfRegisterSyncRoot` 注册同步根（置换成现在的注册表 Shell NameSpace 指 UNC）
2. `CfConnectSyncRoot` + 回调表（最小 2 项 + 终止项）
3. fetch 回调 → `CfExecute(TRANSFER_DATA)` + `CfReportProviderProgress`
4. `CfCreatePlaceholders`（**列表零下载**，`FileSize` 填云端真实大小）
5. 远端改动只 `CfUpdatePlaceholder` 更元数据
6. `CfSetPinState` 固定 / `CfDehydratePlaceholder` 释放
7. 本地 SQLite 元数据表（照抄 WPS 的 `fs_metadata_table` 列 + 三索引）
8. **读取节流**（WPS 的进程名单 + 限流器）——不做这步会被杀软和索引器打爆

**验收**
- 万级文件目录在本地占 **0 字节**，双击首开延迟可接受。
- 右键"始终保留在这台电脑上"/"释放空间"生效。
- 先做**只读**，双向同步另立项。

**依据**：WPS 取证 §9（八步闭包）、§3（状态机）、§5（数据模型）。

---

### 切片 6 — 内容哈希与秒传（**P2，依赖切片 1**）

> **进展（2026-10-17）**：全链路已落地（快速开发冲刺，编译过、测试待跑）——
> `es_file.content_md5/md5_reliable` 列 + presign-put 带哈希先判秒传（CopyObject + deduped 标记）+
> 分片路径 complete 时判定 + Go 客户端 `fileMD5` 整文件读算随签发上报。
> 秒传只认 `md5_reliable='client'`（整文件读算，碰撞安全前提）。
> 详见 [`iterations/2026-10-17-快速开发-秒传与日志裁剪.md`](../iterations/2026-10-17-快速开发-秒传与日志裁剪.md)。

**做什么**：上传前算 md5 并随请求上报；服务端已有同哈希对象则**秒传**（不重复占空间）。补 `md5_reliable` 语义（大文件边传边算 vs 读全量算，可靠性不同）。

**依据**：钉盘取证 §9「内容哈希/秒传」（`md5` + `md5_reliable` + 服务端 `genMd5`）；WPS 取证 §10（`fsha1`）。

---

### 切片 7 — 错误三件套（**P1，低成本，可随时插入**）

**做什么**：任务错误同时带**业务码 + 人读文本 + 系统码**（对标 `TEC_*` 与 WPS 的 `errorCode`+`errorMsg`+`winErrorCode`）。前端不再只显示"失败"。

**依据**：钉盘取证 §9「配额/策略落本地」；`wps-cloud-drive-benchmark.md` §4.6。

---

## 3b. 与 `progress.md` 阶段划分的对应

路线图的切片**不是新造一套编号**，而是落到 `progress.md` 已有的「主线一」阶段表上。对应关系（便于进度表与本文互查）：

| `progress.md` 阶段 | 现状 | 本文切片 | 关系 |
| --- | --- | --- | --- |
| 阶段 2 产品体验完善 | ✅ 2026-07-29 | — | 已过；切片 7（错误三件套）属其延续 |
| 阶段 3 安全加固（设备配对/TLS/凭据保护） | 🔄 进行中 | **不涉及** | 与本文并行，互不阻塞 |
| 阶段 4 云端 MVP（账号/文件元数据/持久任务） | 🔄 进行中 | **切片 1 / 2 / 3 / 4 / 6 / 7** | 这六片是**阶段 4 的收尾内容**——阶段 4 目前"云端 MVP"已完成账号与授权，缺的正是文件生命周期与协议层 |
| 阶段 5 同步与原生入口（CfAPI / FileProvider） | 待开始 | **切片 5** | 切片 5 就是阶段 5 的 Windows 首片（只读占位符）；macOS FileProvider 不在本文范围 |
| 阶段 6 云端与内网融合 | 待开始 | **不涉及**（仅记录输入） | 见下 |

**三条对进度表的建议**（不在本轮改动，供用户拍板）：

1. **阶段 4 的完成判据应显式包含切片 1–4 + 6/7**——否则"云端 MVP 完成"会停在"能传能取"，与生产级口径不一致。
2. **阶段 5 的开工前置是切片 1**（身份与版本列）——占位符要按 fileId + 版本建元数据，没有这两列就无从对账。当前阶段表把阶段 5 列为"待开始"，但**没有标注它依赖阶段 4 的哪一部分**，建议补上。
3. **阶段 6 的两条输入**已由本轮取证定稿：钉盘的 redolog 订阅制（切片 2 的自然延伸，用于多端增量同步）与 WPS 的 `startpagerecent.lastTerminalType/oriTerminalType` 端点溯源（`wps-cloud-drive-benchmark.md` §4.7）。**建议在阶段 6 条目下引用本文与钉盘取证 §4**，不要另起文档。

**与两份既有对标文档的关系**：本文**取代** `wps-cloud-drive-benchmark.md` §6 的四步 P1 排期（§6 原四步已被拆解重排，见 `progress.md` 第 8 条下的更新注），但**不取代**该文档的取证结论本身——它的 §4.1–4.7 仍是回收站/标签/上传通路/失败诊断的原始依据。

---

## 4. 明确不做（附理由）

| 不做 | 理由 |
| --- | --- |
| 对齐钉盘的 4 MiB 分片 | 分片大小是**可协商参数**（钉盘取证 §3.1.1：客户端提议、服务端裁定）。值得抄的是**"提议/裁定"这次往返**，不是数值 |
| 抄钉盘的 `conflictStrategy` 取值集合 | 已确认取值域在服务端（钉盘取证 §11-3），我们自有策略集合即可 |
| 抄 WPS 的 Qt UI | 与 Wails/Web 技术栈无可比性（`wps-cloud-drive-benchmark.md` §10 已判定） |
| 团队空间 / 设备管理面板 | `wps-cloud-drive-benchmark.md` §7 已判定：与当前单组织规模不匹配 |
| 双向同步（本阶段） | 切片 5 先做**只读**占位符；双向同步需冲突策略（切片 3）+ 事件队列，是下一个大阶段 |

---

## 5. 与在制品的对接（重要）

用户工作区当前有**未提交**的「回收站 + 版本」在制品（迭代文档「结果」栏为空，代码已成型）：

- 它的 `es_file` 列（`state/deleted_at/origin_path/origin_parent_id/version_id`）**就是切片 1 的一半**。
- **建议先收尾并提交该片**，让切片 1 站在它上面继续补（`content_md5` / `md5_reliable` / `dentry_version` 语义合并），**不要另起一套列**。
- 该片的验收口径已含真实服务器端到端（192.168.1.239），与切片 1 的验收可合并执行。

### 5.1 在制品的**当前可编译/可测状态**（2026-10-16 实测，供拍板参考）

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| 全仓编译 | `go build ./...` | ✅ 通过（EXIT=0） |
| 全仓 Go 测试 | `go test ./...` | ✅ **19 个包全绿**（含 `internal/drive` 4.1s、`internal/spacedav`） |

**结论**：在制品**不是半截烂尾**，它处于「代码完成、未提交、未做真实服务器端到端」的状态。因此推进建议是：

1. **优先收尾**（不是重做）——补真实服务器联调 + 回填迭代文档「结果」栏，即可作为切片 1/4 的基线；
2. 若要继续动 `es_file` 列，**在它的列上追加**（`content_md5` / `md5_reliable` / `dentry_version` 语义合并），**不要另起一套**；
3. 切片 2–7 可在该片提交后并行展开，彼此不抢同一批文件（切片 2 动 Go 控制面 + 服务端存储、切片 5 动 `internal/namespace`）。

> 上述两项检查是**只读**的（不写文件、不启服务、不碰数据库），不影响在制品状态。

---

## 6. 本路线图的证据强度与边界

**强**：三份证据来源独立（钉盘原生 PE 反编译 / WPS 本机数据 + PE 反编译 / Cloudreve 源码），交集结论可信度高。

**弱（必须知道）**：
1. **全部为静态证据**——两份取证都**没有任何运行时观察**（无抓包、无进程级观测）。"分支存在"不等于"运行时真走到"。
2. **钉盘数值结论**（如 4 MiB）来自反编译，属【G】级（无 REA Evidence ID），详见 `toolchain-rea.md` §8.5「取证通路的取舍」。
3. **WPS `qingnse64.dll`（16.7 MB）未做完整分析**——CfApi 状态机与覆盖图标的中间映射表仍缺（WPS 取证 §11-6、§11-7）。
4. **本机账号未启用过钉盘同步**，`sync.db` 工作树全空——`remote_history.event_type` 与 9 值枚举是否同一编码**未坐实**。

**因此**：本路线图可用于**排期与设计**，但涉及具体协议字段的切片（1、2、3）在实现前**应补一次运行时验证或服务端侧确认**。
