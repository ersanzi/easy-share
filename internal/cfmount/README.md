# cfmount —— 控制面云盘 → Windows 按需占位符 的适配层

把 `internal/drive`（控制面 HTTP 客户端）翻译成 `internal/cfapi`（CfApi 绑定）
需要的输入，是**路线图切片 5** 里「云端清单 → 本地占位符树」的那座桥。

## 为什么单独一个包

| 包 | 职责 | 不该知道的 |
| --- | --- | --- |
| `internal/drive` | 控制面 HTTP 客户端（列举/预签名/上传下载） | 占位符、回调、句柄 |
| `internal/cfapi` | 纯 CfApi 绑定（注册/连接/占位符/回调） | 控制面、HTTP、JSON |
| **`internal/cfmount`** | **两者之间唯一的桥** | 两边各自的实现细节 |

分开的好处：`cfapi` 能用**模拟数据**完整测试（已有 20+ 用例，不需要起服务），
`drive` 也不必知道占位符的存在。

## 与 `internal/spacedav` 的关系（重要）

两者是**同一份云盘内容的两种本地呈现**：

| | `spacedav`（现有） | `cfmount`（本包） |
| --- | --- | --- |
| 本地形态 | WebDAV 网络位置（注册表 NameSpace 指 UNC） | **CfApi 同步根 + 按需占位符** |
| 数据流 | 每次读取走 HTTP → 控制面 → 预签名 → RustFS | 列表只搬元数据；**点开才拉** |
| 磁盘占用 | 全量物化（读多少占多少） | **列表零字节** |
| 改名 | 下载 + 上传 + 删除（非原子） | 占位符可直接被系统改名（本片未接回调，改名会丢同步） |

**生产上应当只启用其一**，否则用户在「此电脑」里会看到两个入口指向同一批文件。
切换时机取决于 `cfmount` 补齐到什么程度（见下）。

## 已实现

| 部分 | 说明 |
| --- | --- |
| `Entries(objects)` | 扁平文件列表 → 含**推导出的目录**的清单。控制面不返回目录对象（对象存储无目录），必须自己补，否则逐层建树找不到父目录 |
| `List(cfg)` | 产出 `cfapi.ListFunc`；未登录时返回错误（而不是显示成空目录） |
| `Open(cfg)` | 产出 `cfapi.OpenFunc`；按路径取内容 |
| `Config.ProviderID` | 提供方 GUID。**个人/共享空间必须用不同 GUID**，否则系统会把两个根当成同一提供方的两个根 |

## 验证

```
go test ./internal/cfmount/ -v
--- PASS: TestAncestorDirs
--- PASS: TestEntriesInfersDirectories
--- PASS: TestEntriesFileIdZeroNoCollision
--- PASS: TestMountThroughAdapter
        全链路通过：清单 1 次、按需拉取 [docs/a/deep.md]        # 回退路径（无 Presigner）
--- PASS: TestMountThroughAdapterRange
        Range 路径实测：[deep.txt bytes=0-17]（整份读取次数 0）  # 生产路径（有 Presigner）
```

### 两条路径都有端到端覆盖（这一点曾漏掉）

`Open` 是靠**类型断言** `cfg.Client.(Presigner)` 分流的。早先只有一个假客户端
（**不实现** `Presigner`），所以端到端用例**只覆盖了回退路径**——
而生产用的 `*drive.Client` 是**走 Range 那条**。等于最重要的路径没被测到。

现在两个假客户端并存：`fakeClient`（无预签名 → 回退路径）、
`rangeClient`（有预签名 → **生产路径**），各自断言：

- 建树阶段**零请求**；
- 读文件时走的是预期那条分支（Range 用例断言 `bytes=0-17` 且整份读取次数为 **0**）。

`TestMountThroughAdapter` 是**全链路**验收，走的是与生产一致的路径
（只把 HTTP 客户端换成内存假实现）：

```
drive.Object 列表 → Entries（推导目录）→ cfapi.MountSpace
    → 建占位符树 → 读文件 → FETCH_DATA → Open → 内容
```

断言：目录被推导出来、**建树阶段一次都没拉内容**、读深层文件时**只拉了那一个**。

## 一处**已修复的静默数据错误**（值得记下来）

对接真实控制面契约时发现的边界，**表现是"打开 A 读到 B 的内容"这类静默错误**：

控制面的 `FileVo.fileId` 是**装箱类型 `Long`**，在「存储里出现不合规路径
（历史脏数据）」时返回 **null**（见 `DriveFileService` 的 `list` → `reconcileAndMap`
私有步骤里的 catch 分支），
Go 侧反序列化后就是 **0**。

若直接把 0 编码进身份，**所有这类文件会共享身份 `esf:0`**——而 fetch 回调正是
按身份反查路径的，于是它们会互相指错。

**修复**：fileId 不可用时退回按路径派生的身份（前缀 `esp:`，与 fileId 的
`esf:` 明确区分，便于日志里看出是哪一类）。这类身份不跨会话稳定，
但它们本来就是脏数据兜底路径。

**并且验证了这个测试真的能抓到它**（把修复回退后跑）：

```
--- FAIL: TestEntriesFileIdZeroNoCollision
    cfmount_windows_test.go:131: 身份碰撞：dirty-a.txt 与 dirty-b.txt 共用身份 "esf:0"
```

即用例不是"陪跑"的——它会以正确的信息失败。**教训：跨语言边界时，
「看起来不可能为 0」的 ID 字段要当成边界条件处理**，装箱类型的 null 尤其容易漏。

## 已知缺陷（接真实云端前必须处理）

### 0. ~~占位符可写，用户改动会静默丢失~~ → **已修（KI-14）**

cfapi **只注册读回调**（`FETCH_DATA` / `CANCEL_FETCH_DATA`），没有写回调：
用户编辑、改名、删除占位符都不会被上报。而占位符原先带的是
`FILE_ATTRIBUTE_NORMAL`——**「只读原型」的那个「只读」当时没有任何执行层依托**。

实测三段：①编辑保存**成功**；②改动**没上传**；
③重建一旦能正常跑（带 `SUPERSEDE`），**本地内容被覆盖成 0 字节**。

**修法**：`Entries` 对全部条目（文件与目录）置 `ReadOnly: true`，
落到 `FILE_ATTRIBUTE_READONLY`。实测写入被拒（`Access is denied`）且
**按需读取不受影响**（读回内容正确）——后者必须实测，因为水合本质是内核写这个文件。

双向同步（切片 5b）落地时，要连同写回调一起放开这里。

> ⚠️ **时序陷阱**：当前还没丢数据，只是因为 `Sync` 重建本身是坏的
> （`ERROR_ALREADY_EXISTS` 整批失败）。谁去修那个更显眼的 bug，
> 谁就会同时打开数据丢失这条路。详见 [`../cfapi/README.md`](../cfapi/README.md)。

### 1. ~~`Open` 每次读整份对象~~ → **已修（走 Range）**

原因是 CfApi 的回调要**任意区间**，而原先的实现是「整份下载 → 内存切区间」，
于是「读 1 GB 里 1 字节 = 下载 1 GB」。

**修法（纯客户端，没动控制面）**：控制面签发的预签名 URL **只签了 bucket + key**
（`DriveStorage.presignGetAt` 里没把任何请求头纳入签名）。SigV4 的语义是
**签名时未列出的头不参与校验**，所以客户端可以自由添加 `Range` 头。

于是 `Open` 现在优先走 `Presigner`（`*drive.Client` 满足，`PresignGet` 是导出方法）
自己发带 `Range` 的 GET；没有 `Presigner` 的客户端退回 `OpenWholeObject`（原行为）。

> 注意这与 `internal/spacedav` 的 `readFile.Seek` 注释相反——那里写着
> 「真正的随机读需要 Range 请求，而控制面的预签名 URL 没有暴露该能力」。
> **能力是有的，只是先前没试**；同一个修法可以直接搬过去解开 `Seek`。

**正确性不依赖服务端支持 Range**：若服务端忽略 Range 返回 200，就整份读再切区间
（只是慢），语义不变。两条路径都有测试覆盖：

```
--- PASS: TestRangeFetcherPartialContent   # 206：只取区间（断言服务端仅发 4 字节）
--- PASS: TestRangeFetcherFallbackOn200    # 200：忽略 Range，回退后内容仍正确
--- PASS: TestRangeFetcherOutOfRange       # 416：可读错误，不是 panic
--- PASS: TestRangeFetcherShortBody        # 206 但 body 短 → 报错，不静默截断
```

> ⚠️ **尚未对真实 RustFS 验证**（本机 Docker 未运行）。因此实现里保留了完整回退，
> 并在接入时应当实跑一次确认走的是 206 分支。

`TestRangeFetcherShortBody` 那条尤其值得留着：S3 兼容实现若返回 206 却给短 body，
上层会读到**静默截断**的数据——宁可报错，也不要让用户读到半个文件。

### 2. 目录没有稳定身份

控制面的 `es_file` 目录层尚未覆盖目录对象（路线图切片 4「目录对象」待做），
所以目录身份是**由路径哈希派生**的（`esd:<hash>`），只保证同一会话内一致，
**不承诺跨会话稳定，也不承诺改名后不变**。

文件的身份是稳的：`esf:<fileId>`，fileId 来自控制面（ADR-0001 文件身份模型）。

### 3. 继承 `internal/cfapi` 的同步回调局限

见 [cfapi/README.md](../cfapi/README.md) 的「已知局限」：取数在系统回调线程上**同步**完成。

⚠️ 那里原有一句「大文件可能撞 I/O 超时」，**经实测已被收窄**：20 秒以内的慢取数成功，未观察到超时；但等待时长**没有已知上限**，且回调期间无法处理其他请求。
与缺陷 1 叠加时确实更严重——每次读都要先把整份对象下下来。

## 下一步

1. ~~控制面支持 Range~~ → **已用纯客户端方式解决**（见缺陷 1）。
   顺带建议：**`internal/spacedav` 可以搬同一个修法**解开 `Seek` 的限制；
2. 接入 `app.go` 与登录态（挂载/卸载随登录态走）——**注意会与在制品「回收站+版本」有文件交集**；
3. 目录对象落地（缺陷 2），需要切片 4 先行；
4. 异步取数（cfapi 侧，目前隔离在 `experimental/`）。
