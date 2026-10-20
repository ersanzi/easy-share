# 钉盘「文件存放」逆向取证 — 索引模型 / 增量协议 / 按需占位符

日期：2026-10-15 　目标：钉钉 Windows 客户端 钉盘同步引擎
产物：`%APPDATA%\DingTalk\DingpanSyncUpgrade\1.260917.1139\`

| 产物 | 大小 | SHA-256 |
| --- | --- | --- |
| `sync_frame.dll` | 10 374 656 | `4697e02d0210f80fdac9c22e409e58a21b112f83c2e2ed8332f84df80161efc4` |
| `DingSyncShellExtension.dll` | 1 739 264 | `d55cf1fc869c4c09faf29c2d2487e32369b92c2fa37adada4a626da6069ce550` |
| `DingpanSyncService.exe` | 2 041 856 | `e4eca5ab742db8fad8a59adae26946ce8e07a6c29364132a13b1853e70d4b8d3` |

> 边界先说清楚：`main/current_new/web_content.dat`（H5 前端）是钉钉主动设置的 ZIP 加密容器，本轮不走破解；`AlibabaProtectCon.exe` / `AliProtectCollina.dll` 加固与反调试不做脱壳绕过。取证只用静态产物与本机自己的账户数据。

---

## 0. 一句话结论

钉盘把"文件存放"做扎实靠的不是界面，是四件底层事：**本地三棵树索引（含锚点树）+ 远端 dentry 版本号、`space version` + `redolog` 的增量复制协议、Windows Cloud Filters 按需占位符（本地只留壳、点开才下载、可固定可释放）、一套写死的冲突矩阵**。这四件我们一件都没有——我们的网盘是"一次全量 list + 扁平 key 前端推目录 + 同名静默覆盖 + 文件整份进内存再上传"。

---

## 0b. 中文口径（不看证据也想得通）

钉盘的网盘用起来"稳"，是因为它把四件事做在了前面。

**一，每个文件和文件夹都有终身编号。** 你改名、挪目录，系统认的还是那个编号，不会产生"旧的一份删掉、新的一份建出来"这种动作，所以历史、分享、回收站都接得上。我们是以路径当身份，改名等于重做一个文件，旧的那份痕迹全丢。

**二，只看清单变动，不重看整盘。** 每个空间有一个变更流水号，客户端每次只问"上次那个号之后发生了啥"，拿回一小串变动就够了，几十万文件也不会卡。而且支持只订阅某几个空间、只展开到某几层。我们现在每次都是把整个空间的清单全拉一遍，文件一多就慢，搜索更是两个空间全量拉回。

**三，云端文件在电脑里先只留一个空壳。** 列表里看得见、占零字节，双击才真下载；也可以右键"始终保留在这台电脑上"或者"释放空间"。这才是网盘该有的样子，也解释了为什么钉盘客户端不占地方。我们现在的做法是把整份文件先读进内存再传，大文件就按等量的尺寸吃内存。

**四，两边同时改一个文件时，规则写死在程序里，不弹窗问用户。** 内容一样的认作同一个文件，直接合并身份；内容不一样的给本地那份自动加标记改名，两份都留着。我们现在是后写的人把前一份覆盖掉，而且不告诉任何人。

顺带还有三项：上传前先报文件大小和分片数，拿到上传编号后逐片传，最后提交事务，所以断了能接着传；删除会进电脑回收站，云端那份也留着可恢复的历史版本；容量不足、单文件超 100 GB、企业禁止下载这些限制都会变成看得懂的提示，只读空间还会直接把本地文件夹的权限改掉。

---

## 1. 证据等级与可复现方法

`sync_frame.dll` 在 2026-10-15 当日**没有拿到 REA Evidence**；**2026-10-16 已补齐**，原因也已查清——**不是目标体积超预算，是 REA 的默认启动超时（330 s）不够**。

- 2026-10-15 两次失败：第一次路径组件准入被钉钉进程占用 `%APPDATA%\DingTalk` 撞共享冲突（把 DLL 复制到中性目录后 SHA-256 不变）；第二次跑到 `list_documents` **`provider_timeout` 330 000 ms** 超时。
- 2026-10-16 复查 REA 源码（Node 应用 `C:\Users\lishi\tools\rea\node_modules\rea-agents`）定位到 `dist/config/ghidraStartupTimeout.js` 的 `DEFAULT_GHIDRA_STARTUP_TIMEOUT_MS = 330_000`，对应环境变量 **`REA_GHIDRA_STARTUP_TIMEOUT_MS`**（`dist/config/environment.js:34`）。
- 设 `REA_GHIDRA_STARTUP_TIMEOUT_MS=1800000` 后**同一条命令、同一 SHA-256 的产物、一次跑通**：

| Evidence ID | 操作 | 对象 | 结论 |
| --- | --- | --- | --- |
| `ev_dc508b2cc52ed00486b4f95ed854ea019224bc9b1e1e513db6794aebc9da97b8` | `analyze`（provider rea-workflow / ghidra 12.1.4） | `sync_frame.dll` | **11 段、32 337 个函数、9 621 条串**；`.text` 7 596 032 B、`.rdata` 2 154 496 B；`import_mode: ephemeral-source-immutable`、`authority: shipped-artifact`、`confidence: derived` |

> 该 Evidence 的 `limitations` 照抄：`Derived by an REA composed workflow.`

**方法学更正**：上一轮把这条通道记为「10 MB 级目标超预算」，并据此把 `partSizeOfClient` / `event_type` / `conflictStrategy` 三项列为「本轮取不到」。**其中前两项本轮已用另一条更经济的通路取到**（见 §3、§4 增补）；正确的做法是 **REA 用于出正式 Evidence，自建持久化 Ghidra 工程用于多轮定向挖掘**，详见 [`toolchain-rea.md`](../toolchain-rea.md) §8。

**REA Evidence（累计）**：

| Evidence ID | 操作 | 对象 | 结论 |
| --- | --- | --- | --- |
| `ev_dc508b2cc52ed00486b4f95ed854ea019224bc9b1e1e513db6794aebc9da97b8` | `analyze`（2026-10-16） | `sync_frame.dll` | **11 段、32 337 个函数、9 621 条串**（详见上节） |
| `ev_e8edcc100cc1eb505874dcd25b9347df5d2a9fbbc386000934fac4f11cae5621` | `analyze`（provider ghidra 12.1.4） | `DingSyncShellExtension.dll` | 12 段、**4 249 个函数**、1 054 条串；`.rsrc` 独占 703 488 B |
| `ev_6cf534251cc7e9bd7416f8ed3b64b664696722207ae5145d999e52ad5b67dc98` | `inspect-pe-resources`（无需引擎） | 扩展 DLL | 覆盖完整（examined_entries 77）：**37 条资源、4 个 RT_GROUP_ICON**，每组 8 个尺寸（16/24/32/48/64/96/128/256，32 bit） |

REA 自报的限制照抄：非 `RT_GROUP_ICON` 的载荷不解析；语言回退、图像解码、以及"资源管理器是否真的加载了这些图标"均未观察。

因此其余结论标签为：**【S】**= 二进制字符串扫描（`D:\Develop\_dl\dingpan\strings.py`、`strings2.py`，ASCII + UTF-16LE 双通道）；**【D】**= 本机 DingpanSync SQLite 实测副本；**【R】**= REA Evidence；**【G】**= **自建持久化 Ghidra 工程 + 自写脚本**的反编译产出（本仓 `scripts/reverse/`（+ `output/` 下的运行产物），可复现但**无 REA Evidence ID**，见 [`toolchain-rea.md`](../toolchain-rea.md) §8.3）；**【推】**= 由前述做的推断；**【?】**= 未知。字符串是编译进产物的字面量，属硬证据；但**日志串只能证明存在该分支，不能证明运行时真的走到**（全文无运行时观察）。另外中文文案不在同步引擎也不在扩展里（UTF-8 / UTF-16LE 中文串扫描命中 **0**，产物内文案为英文），文案层【?】。

复现：

```bash
# 字符串面（本轮实际用的两个过滤器集，输出 12 / 11 个语义分块）
python strings.py  sync_frame.dll  sync_frame_strings.txt
python strings2.py sync_frame.dll  sync_frame_strings2.txt
# 本地库（必须连 -wal 一起拷；**删掉 -shm** 让 SQLite 从 WAL 重建索引，见 §2.1）
cp "$APPDATA/DingTalk/<uid>_v2/DingpanSync/sync.db"{,-wal} <中性目录>/
rm -f <中性目录>/sync.db-shm
```

2026-10-16 增补用的通路（**大目标多轮定向挖掘的推荐姿势**，详见 [`toolchain-rea.md`](../toolchain-rea.md) §8）：

```powershell
# REA 出正式 Evidence（关键：放宽默认 330 s 启动超时）
$env:REA_GHIDRA_STARTUP_TIMEOUT_MS = '1800000'
rea analyze output\sync_frame.dll --provider ghidra --format json

# 自建持久化 Ghidra 工程（一次性），之后每次定向查询 8–24 s
& "$env:GHIDRA_INSTALL_DIR\support\analyzeHeadless.bat" output\ghidra-proj dingpan -import output\sync_frame.dll -overwrite
$env:PROBE_OUT = "$PWD\output\probe.txt"
& "$env:GHIDRA_INSTALL_DIR\support\analyzeHeadless.bat" output\ghidra-proj dingpan -process sync_frame.dll -noanalysis `
    -scriptPath scripts\reverse -postScript ProbeStrings.java 'LocalModify' 'RemoteModify'
```

---

## 2. 本地三棵树：同步引擎的索引层【D】【S】

`%APPDATA%\DingTalk\<uid>_v2\DingpanSync\sync.db`（WAL 模式，实测主库 4 KB + WAL 655 KB）。表名与源文件一一对上，源码树路径直接泄在串里【S】：`D:\sync_main\src\sync_frame\storage\{cached_local_db,cached_remote_db,cached_edit_locked_db,remote_history_db,sync_record_db}.cpp`、`tasks\{cached_local_filesystem,cached_remote_filesystem}.cpp`。

| 表 | 字段 | 作用【推】 |
| --- | --- | --- |
| `cached_local` | `local_id, parent_local_id, path, size, modify_time, is_dir, md5` | 本地磁盘快照（以 `local_id` = NTFS fileid 为身份，不以路径为身份） |
| `cached_remote` | `remote_id, parent_remote_id, **version**, name, size, modify_time, md5, **md5_reliable**, path, **thumbnail**` | 远端 dentry 快照 |
| `sync` | `path, local_id, remote_id, parent_remote_id, version, size, modify_time, md5` | **锚点树**：上次三方一致的状态。判"谁变了"全靠它 |
| `remote_folder_sync_status` | `remote_id, space_id, status, **depth**` | 每个订阅目录的同步步进与展开深度 |
| `remote_history` | `remote_id, parent_remote_id, event_type` | 远端事件历史（版本/回收站的本地账本） |
| `sync_record` | `finish_time, action_type, resource_name, local_path, remote_id, size` | 传输记录，UI 侧走 `rpc: get_recent_sync_record` / `clear_sync_record`【S】 |
| `cached_edit_lock` | `local_id, remote_id, **lock_expire_time**` | 编辑锁（带过期），服务端配套 `tryGetEditLock` / `releaseEditLock` / `batchInfoEditLock`【S】 |

设计要点，按重要性：

1. **身份不是路径**。三棵树都有 `local_id` / `remote_id`，`path` 只是可改属性；改/移只动 `parent_*_id`，引擎有 `cache_modify_parent_id batch update` 与"子树重挂"逻辑【S】。我们的 `es_file` 唯一键是 `(space_type, owner_id, file_path)`——路径就是身份，重命名必然是删+建（`internal/spacedav/spacedav.go:274-313` 改名确实是 下载+上传+删除）。
2. **`md5_reliable` 是一等列**。引擎承认"哈希可能不可信"（远端没算出来 / 只信本地 / 只信服务端），配套 `genMd5`（让服务端代算）、`reportMd5NotMatching`、`notify_md5_mismatch`、`local remove on remote file md5 miss`【S】。
3. **锚点树是冲突判定的前提**。没有第三棵树就只能比 size+mtime，任何"两边都改了"的判断都做不出来。

实测本机三棵全空（该账户未开启过同步），只有 `config.DEVICE_ID = 18653421`【D】。所以本文所有行为结论来自产物字符串，不含真实行样本。

---

### 2.1 【2026-10-16 复核】三棵树的建表语句（【D】，活库副本含 WAL）

上一轮的表结构结论**本轮用活库副本复核通过**。取法：把 `%APPDATA%\DingTalk\<uid>_v2\DingpanSync\sync.db` 连同 **`-wal`（655 112 B，主库仅 4 096 B，业务数据实际都在 WAL 里）** 一起拷到中性目录，**删掉 `-shm`** 让 SQLite 自行重建索引后只读打开。

> ⚠️ 这条经验要照抄：**只拷 `sync.db` 会拿到一个几乎空的库**（主库 4 KB）。WAL 才是数据所在。

18 个对象（6 表 + 5 索引 + `sqlite_sequence`），逐表建表语句（照抄，未改写关键列）：

```sql
-- 本地磁盘快照（身份 = local_id）
CREATE TABLE 'cached_local' ('id' INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
  'local_id' TEXT NOT NULL, 'parent_local_id' TEXT NOT NULL, 'path' TEXT NOT NULL,
  'size' INTEGER NOT NULL, 'modify_time' INTEGER NOT NULL, 'is_dir' INTEGER NOT NULL, 'md5' TEXT NOT NULL);

-- 远端 dentry 快照（带 version 与 md5_reliable）
CREATE TABLE 'cached_remote' ('id' INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
  'remote_id' TEXT NOT NULL, 'parent_remote_id' TEXT NOT NULL, 'version' TEXT NOT NULL,
  'name' TEXT NOT NULL, 'size' INTEGER NOT NULL, 'modify_time' INTEGER NOT NULL,
  'md5' TEXT NOT NULL, 'md5_reliable' INTEGER NOT NULL, 'path' TEXT NOT NULL, 'thumbnail' TEXT NOT NULL);

-- 锚点树（唯一一张同时持有 local_id 与 remote_id 的表，且 local_id 是 INTEGER）
CREATE TABLE 'sync' ('id' INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, 'path' TEXT NOT NULL,
  'local_id' INTEGER NOT NULL, 'remote_id' TEXT NOT NULL, 'parent_remote_id' TEXT NOT NULL,
  'version' TEXT NOT NULL, 'size' INTEGER NOT NULL, 'modify_time' INTEGER NOT NULL, 'md5' TEXT NOT NULL);

-- 远端事件账本：event_type 是 INTEGER  ← §4.1 的 9 值枚举正好落在 INT 上
CREATE TABLE 'remote_history' ('id' INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
  'remote_id' TEXT NOT NULL, 'parent_remote_id' TEXT NOT NULL, 'event_type' INTEGER NOT NULL);

-- 订阅树的展开状态：带 depth
CREATE TABLE 'remote_folder_sync_status' ('id' INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
  'remote_id' TEXT NOT NULL, 'space_id' INTEGER NOT NULL, 'status' INTEGER NOT NULL, 'depth' INTEGER NOT NULL);

CREATE TABLE 'config' ('id' INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, 'key' TEXT NOT NULL, 'value' TEXT NOT NULL);
```

**三条从建表语句读出来的结论**（不必靠推断）：

1. **`local_id` 的类型在三棵树里不一致**：`cached_local` / `sync` 用 `INTEGER`，而 `remote_id` / `parent_remote_id` / `version` 全是 `TEXT`。即**本地身份是 NTFS 的 64 位数字 file ID，远端身份是服务端下发的字符串**——两套 ID 体系在设计上就没打算统一，靠锚点树对应。
2. **`version` 是 `TEXT` 而非整型**——说明它可能不是单纯自增序号（与 §4 的 `spaceVersion` 单调整数不同，dentry 版本是另一个编码）。
3. **`cached_remote.thumbnail` 是 `NOT NULL TEXT`**：缩略图引用随 dentry 一起缓存，不是旁路可选字段。

本机账号**未启用过钉盘同步**，故 6 张业务表**全为 0 行**（仅 `config` 有 1 行 `DEVICE_ID=18653421`）——`remote_history.event_type` 与 §4.1 枚举是否同一编码**仍无法用行样本坐实**（§11-4）。

## 3. 远端模型：dentry + 版本 + 前值（CAS）【S】

字段面（编译进产物的字面量）：`dentryId / parentDentryId / **preParentId** / newDentryName / dentryName / dentryPath / dentrySize / dentryType / **dentryVersion** / **preDentryVersion** / dentryMd5 / dentryClientMd5 / localDentryMd5 / spaceId / remote_space_id / syncAddDentryParamList / existingDentry / conflictStrategy / conflictHandle / partSizeOfClient`。

`preXxx` 系列是**乐观并发控制**：写请求带"我看到的旧版本/旧父节点"，服务端不匹配就拒绝【推】。对照我们：`DriveController.java:96-111` 预签名 PUT 无任何条件检查，`DriveFileService.java:51-63` 同路径直接 upsert 覆盖 size——两个人同时改同名文件，后写的静默赢，前一份直接没了。

服务端权威纠正也有日志：`add file dentry reponse parent diff, expect {}, actual {}, will change to expect`（客户端算的父节点与服务端返回不一致时，以服务端为准）【S】。

**28 个 CSpaceSync 方法全量**（`/r/Adaptor/CSpaceSync/` 前缀，本轮完整枚举）：
`addDentry` `addDentryByHistory` `batchAddDentry` `batchInfoEditLock` `batchInfoUpload` `checkSpaceFileCountLevel` `checkUserEnableSync` `deleteDentry` `genDeviceId` `genMd5` `getDentryInfo` `getNewestSpaceVersion` `getToken` `infoDownload` `infoUpload` `listDentrysByFolder` `listRedologs` `listSubscribes` `monitor` `moveDentry` `releaseEditLock` `renameDentry` `reportMd5NotMatching` `subscribe` `tryGetEditLock`
（另有 `stringify request body for ...` 三条日志串凑成 28 条命中，实际 RPC 数 25。）

深链：`dingtalk://dingtalkclient/page/yunpan?route=previewDentry&spaceId=`【S】。

### 3.1 【2026-10-16 增补】单个 dentry 的字段清单（【G】，反编译）

`sync_frame.dll` 已建持久化 Ghidra 工程，`FUN_1801251d0` 是 dentry（或上传参数）的 JSON 序列化器：逐字段 `if (字段存在) { ... } else { 记 "toJson <字段> fail" }`。这解释了上一轮字符串扫描为什么能得到一整片连续的字段名——它们来自同一个函数的**字段表**。

同族的字段块（`.rdata` 连续排布，均为 NUL 分隔 ASCII）给出了完整请求/响应形状：

| 字段块起点 | 语义 | 关键字段 |
| --- | --- | --- |
| `0x180761d98` | 空间/事件 | `orgId` `spaceId` `eventTimestamp` `eventType` `eventData` |
| `0x1807624d0` | upload 参数 | `… conflictStrategy` `isExisting` `batchItemId` `storeUrl` `storeType` `parentPath` `conflictHandle` `syncAddDentryParamList` `successInfo` `failedInfo` `preferBatchSize` `dentryIds` `lockInfos` **`partSizeOfClient`** `preferUploadProtocol` `callerRegion` `infoUploadParamList` |
| `0x180761fe8` | 结束标记 | `sceneType` |

证据形态：字符串针 `partSizeOfClient` @ `0x1807627b8`，xref 唯一 READ @ `0x18012542c` ∈ `FUN_1801251d0`【G】。

**这一条对实现的意义**：`partSizeOfClient` 与 `preferUploadProtocol` / `preferBatchSize` 是**并列的「客户端偏好」字段**——即分片大小是客户端**提议**、服务端在 transaction 响应里最终裁定（与 §7 的 `chunk_numbers` 由服务端给出相印证）。我们目前的 `multipartThreshold = 32 MB`（客户端写死）+ 8 MB 分片（服务端 `DriveProperties.java` 下发）方向是对的，但**缺少"客户端提议 / 服务端裁定"这一次协商回执**。

#### 3.1.1 `partSizeOfClient` 取值 = **4 MB**（【G】，反编译到脚，已闭合）

上一轮列为「取不到」。本轮定位到分片上传主函数 **`FUN_1801a4440`**（`"center chunked upload: upload_id={}, chunk_count={}"` 的唯一 xref），其伪码中 `0x400000`（= 4 194 304 = **4 MiB**）出现 3 次，均在分片语境：

```c
// ① 循环外：把 4 MB 装进读缓冲
thunk_FUN_18019ddc0(&local_df8, 0x400000, local_e48);

// ② 循环内两次：本片长度 = min(剩余字节, 4 MB)
ppppppplVar26 = remaining;
if (0x400000 < (longlong)ppppppplVar26) {
    ppppppplVar26 = 0x400000;      // 封顶 4 MB
}
local_e58 = ppppppplVar26;
local_e78 = read(..., ppppppplVar26);   // 本片实际读入
if (local_e78 != ppppppplVar26) {
    // "center chunked upload: read error at seq {}, expected {}, got {}"
}
```

**分片数公式也在同一函数里**——经典的向上取整惯用法：

```c
local_d98 = (char *)(((file_size + 0x3fffff + sign_correction) >> 0x16));
//            ^ 0x3fffff = 0x400000-1 = 4M-1     ^ 0x16 = 22 → >>22 == ÷2^22 == ÷4194304
local_c68 = "{}/attachment/upload/transaction?file_size={}&chunk_numbers={}";
```

即 `chunk_numbers = ceil(file_size / 4 MiB)`（含负数修正，标准编译器对整数除法的生成形式）。**三处证据互相印证：缓冲初始化、循环封顶、分片数公式，全部是 4 MiB。**

**坑（本轮踩过并更正）**：先用 `ConstRefs` 按 4/5/8/16 MB 扫全 `.text` 的 1 308 832 条指令，得 38 个命中函数，但**按"是否含 upload 关键字"筛选时只有 `FUN_1801a4440` 命中**，其余 37 个是内存/缓冲区常量（例如同函数的 `0x1e00000`=30 MB 是**缓冲区容量**而非分片尺寸）。**纯常量扫描会淹没在噪声里，必须落到具体函数的语义语境才能定案。**

**对照我们的实现**：

| 项 | 钉盘 | EasyShare |
| --- | --- | --- |
| 分片大小 | **4 MiB**（客户端在 `partSizeOfClient` 提议） | 8 MB（服务端 `DriveProperties.java:79` 下发） |
| 分片数 | 客户端算 `ceil(size/4MiB)` 随初始化请求上报 | 服务端按 parts 计算 |
| 大文件阈值 | 无独立阈值（一律走分片） | `multipartThreshold = 32 << 20`（`internal/drive/upload_session.go:26`） |
| 续传指纹 | — | `空间+路径+size+mtime`（`upload_session.go:39-48`），**不含内容哈希** |

> 结论：**4 MB vs 8 MB 本身不必对齐**（这是可协商参数，服务端说了算）；真正要对齐的是「客户端提议 / 服务端裁定」这一次往返，以及续传指纹里缺失的内容哈希。


---

## 4. 增量同步协议：space version + redolog【S】

这是钉盘"目录再大也不卡"的正解，也是我们最缺的一块。

- 每个 space 有一个单调版本 `spaceVersion`，客户端持久化 `last_space_version`：`init_space_version_from_db` / `update_space_version_to_db` / `reset_space_version_from_remote`。
- 拉变更不是列目录，是**拉日志**：`listRedologs(space_id, last_space_version)` → 返回 `redologs` + `nextToken` + `newestSpaceVersion`。分页靠 `nextToken`，不是一次给完。
- **日志断了就退化成全量重建**：`listRedologs from space {} failed for changed too much, last_space_version {}` → `full_rebuild, local file count: {}` / `update_all_space_from_redolog`。这是必须一起抄的兜底，否则增量协议在长期离线后无解。
- 版本自身也会冲突：`mark_space_version_conflict` / `clear_space_version_conflict` / `space_version_conflict timeout, allow one retry` / `erase_space_version {}, current version: {}`；`listRedologs space {} newestSpaceVersion differs from max_version` 是"我拉到的不是最新"的检测。
- 变更实时性靠推送+RPC 通知：`notify_space_version_updated` → `rpc: notify_space_version_updated`（引擎→UI），`on_space_version_update ignore, cur/new version` 做版本单调性过滤。

### 4.1 【2026-10-16 增补】`event_type` 枚举：**完整 9 值 + 数值**（【G】，反编译到脚）

上一轮记为「字符串里没有枚举名」。本轮拿到**闭集**：值到名的映射函数直接反编译出来了。

**证据链**（`sync_frame.dll`，SHA-256 `4697e02d…efc4`，自建持久化 Ghidra 工程）：

1. 字符串针 `LocalModify` / `RemoteModify` 的 defined data 落在 VA `0x1807749f0` / `0x180774a30`【G】。
2. 两者的 xref 都包含 `FUN_1803b1600@0x1803b1600` 与 `FUN_180441710@0x180441710`【G】。
3. 这两个函数都是**对同一入参做 9 分支 switch、把枚举值翻译成字符串**（`to-string`），且**两个函数给出的映射逐值完全一致**（互为独立交叉验证）【G】。

`FUN_1803b1600` 的 switch 原文（照抄，未改写）：

```c
switch(*param_3) {
case 0:   memmove(&local_68, "Ignore",       6);  break;
case 1:   memmove(&local_68, "LocalCreate",  0xb); break;
case 2:   memmove(&local_68, "LocalMove",    9);  break;
case 3:   memmove(&local_68, "LocalModify",  0xb); break;
case 4:   memmove(&local_68, "LocalRemove",  0xb); break;
case 5:   _Src = "RemoteCreate"; goto LAB_1803b1793;
case 6:   memmove(&local_68, "RemoteMove",  10); break;
case 7:   _Src = "RemoteModify"; goto LAB_1803b1793;
case 8:   _Src = "RemoteRemove"; LAB_1803b1793: memmove(&local_68, _Src, 0xc);
}
// 初值取自 s_Unknown_18076f240；无 default 分支，未命中时保留初值 "Unknown"
```

`FUN_180441710` 对同一入参（`*(param_1 + 0x18)`）做同样的 9 分支，并且**显式写出 default**【G】：

```c
switch(*(undefined4 *)(param_1 + 0x18)) {
case 0: memmove(param_2, "Ignore",       6);  break;
case 1: memmove(param_2, "LocalCreate",  0xb); break;
case 2: memmove(param_2, "LocalMove",    9);  break;
case 3: memmove(param_2, "LocalModify",  0xb); break;
case 4: memmove(param_2, "LocalRemove",  0xb); break;
case 5: memmove(param_2, "RemoteCreate", 0xc); break;
case 6: memmove(param_2, "RemoteMove",  10);  break;
case 7: memmove(param_2, "RemoteModify", 0xc); break;
case 8: memmove(param_2, "RemoteRemove", 0xc); break;
default: memmove(param_2, "Unknown",     7);
}
```

两个函数**逐 case 对齐、无一处分歧**（含 `Ignore`=0 与 `Unknown`=兜底），故映射本身可作硬结论。

**枚举全表**（数值 → 名）：

| 值 | 名 | 语义 |
| --- | --- | --- |
| 0 | `Ignore` | 忽略（显式存在的取值，**不是** default） |
| 1 | `LocalCreate` | 本地新建 |
| 2 | `LocalMove` | 本地移动/改名 |
| 3 | `LocalModify` | 本地内容修改 |
| 4 | `LocalRemove` | 本地删除 |
| 5 | `RemoteCreate` | 远端新建 |
| 6 | `RemoteMove` | 远端移动/改名 |
| 7 | `RemoteModify` | 远端内容修改 |
| 8 | `RemoteRemove` | 远端删除 |
| 其他 | `Unknown` | 无效值兜底 |

**⚠️ 坑（本轮自己踩过并更正）**：这 8 个名字在 `.rdata` 里虽然地址连续（`0x1807749c0`–`0x180774a30`，间隔 16 B），但**地址顺序是 LocalCreate/LocalMove/LocalRemove/LocalModify**，而**枚举序数是 LocalCreate/LocalMove/LocalModify/LocalRemove**——`Remove` 与 `Modify` 在后两个位置上互换。**靠地址相邻推断枚举序数是错的**，必须以 switch 的 `case` 常量为准。上一版本节据地址顺序写成 `LocalCreate/LocalMove/LocalRemove/LocalModify`，此处已按反编译更正。

#### 4.1.1 附带取到的两个枚举（【G】）

除 `event_type` 外，本次顺带拿到了两个**有 switch 背书**（序号确定）的枚举：

**任务执行结果**（`FUN_18034bea0@0x18034bea0`，switch 原文照抄，default 保留初值 `"Unknown"`）：

| 值 | 名 | 语义【推】 |
| --- | --- | --- |
| 0 | `Success` | 成功 |
| 1 | `Retry` | 重试 |
| 2 | `Skip` | 跳过 |
| 3 | `SkipWithError` | 带错跳过 |
| 4 | `NeedStopSync` | 需停止同步 |
| 5 | `NeedRebuild` | 需重建 |
| 其他 | `Unknown` | 兜底 |

> 注意 `case 2` 在伪码里被优化成 `CONCAT35(...,0x70696b53)`（= 小端 `"Skip"` 的 4 字节），**不是缺失分支**——这是编译器对 4 字节短常量的内联，易误读为空洞。该枚举对应 §8 的自愈语义（`NeedRebuild` ↔ `full_rebuild`、`NeedStopSync` ↔ `full_rebuild` 触发前的停机）。

**同区域还有一簇任务状态**（【S】，未反编译到 switch）：`PAUSED` / `SYNCED` / `SYNCING`（另有 `depressed-show-all`、`ERROR_RESOURCE` 相邻但语义存疑）。

**同区域的另两个枚举**（同为字面量簇，**未反编译到 switch，故只给集合不给序数**）：

| 语义 | 取值集合 | 级别 |
| --- | --- | --- |
| 修改判定 | `UNKNOWN` / `UNMODIFIED` / `MODIFY_TIME_CHANGED` / `DATA_CHANGED` | 【S】 |
| 事件归并动作 | `modify_change_to_create` / `move_change_to_create` / `create_change_to_modify` / `move_change_to_modify` / `remove_change_to_move` / `change_to_ignore` | 【S】 |
| 任务状态 | `QUEUED` / `PRE_PROCESSING` / `PROCESSING`（终态串未落在同一簇） | 【S】 |

**注意口径**：上一轮定名的 `remote_history.event_type`（本地 SQLite 列）与这里的 9 值枚举**是否为同一套编码尚未证实**——本地库无行样本，两者只落在同一命名族【推】。要坐实需运行时样本（用户授权后订阅一个空间即可产生）。

## 5. 按需占位符：Windows Cloud Filters 直接落地【S】

钉盘同步的本地形态是 **CfApi 占位符**（云文件按需下载，和 OneDrive 同机制，无需内核驱动）：

- 建/转换/更新：`CfCreatePlaceholders`、`CfConvertToPlaceholder`、`CfUpdatePlaceholder`、`CfGetPlaceholderInfo`、`cfp_convert_to_place_holder_if_not`、`file_identity_to_placeholder_identity`。
- 拉数据回调：`on_fetch_placeholders` / `on_cancel_fetch_placeholders` / `read placeholder file stream`；释放空间回调：`on_notify_dehydrate` / `on_notify_dehydrate_completion`、`free_space_and_update_place_holder`、`async_lfs_remove_all_placeholder`。
- 用户意图两个开关：**固定（始终留在本地）** `cfp_state_set_pinned` / `set pin state pinned`；**取消固定** `fuse_unpin_materialized_item`。资源管理器图标见 `state_failed.ico`、`state_locked.ico`【D，同目录实测存在】。
- 占位符下的远端改只更元数据不下载：`cloud modify with local is placeholder, update local size and modify time`。
- 冲突分支也按占位符特判：`remote create on local is placeholder`、`local create add sync info for placeholder`。
- 企业策略可以直接否决本地删除：`Delete event ignored by enterprise settings. File will re-establish local placeholder`。
- 扩展侧只有薄壳：COM 类 `SyncContextMenu`（`CLSID_SyncContextMenu`）+ `IShellExtInit` + `PlaceholderInfo` + `SyncFileClient`，状态查询走 `sync_status_and_paths` / `sync_directory` / `sync_issue`【S】——UI 与引擎解耦，扩展只问状态、只发指令。体量印证：1.74 MB、**4 249 个函数**（引擎本体 10.4 MB），资源段里 **4 组图标 × 8 个尺寸**【R】，据此推测是 4 档状态覆盖图标【推】（同目录另带 `state_failed.ico`、`state_locked.ico` 两个独立状态图元【D】）。是否真被资源管理器加载未观察，REA 也只给到"候选"级别【R 限制】。

对照我们：`internal/spacedav/spacedav.go` 是 WebDAV 内存映射，写文件"整文件攒内存一次上传"（`files.go:56-103`，`:62` 自己承认大文件吃等量内存）；盘符靠注册表 Shell NameSpace 指 UNC（`internal/namespace/namespace_windows.go:61-66`），没有扩展 DLL、没有占位符、没有"本地有多少云端内容"这一档。

---

## 6. 冲突矩阵：写死在引擎里的规则表【S】

钉盘不是"报错让用户选"，是每种事件组合一条规则。逐条日志（事件命名 = `local|remote` × `create|modify|move|remove`）：

| 情形 | 规则 |
| --- | --- |
| 远端新建 vs 本地已有**且内容相同** | 认作同一文件：`remote create conflict file content same, replaced old remote with new` / `delete old synced file`（哈希做去重） |
| 远端新建 vs 本地已有、内容不同 | `remote create on md5 empty, rename local failed` → 保双方，本地改名 |
| 本地改 + 远端改 | `local modify conflict with remote modify` → `lfs_rename_resolve_conflict: keyword: {}, from: {} to: {}`（生成冲突副本名）；`remote modify conflict with local modify, will skip local` |
| 远端建目录 vs 本地同名目录 id 不同 | `RemoteCreateDirConflictWithDiffId` / `RemoteDirCreateConflict`，两条分支各自解析 |
| 移动目标已存在 | `remote move target exist, rename resolved conflict failed` / `local move on remote dest exist, lfs_move_resolve_conflict failed` |
| 移动成环 | `remote move parent is child of current, lfs_move_new_parent_resolve_conflict failed` |
| 本地移 vs 远端同内容 | `local move conflict file content same, delete conflict remote file` |
| 远端删目录、本地有改 | `remote folder remove with local conflict` |
| 参数层 | 请求体带 `conflictStrategy` / `conflictHandle`，即策略是**协议字段**，不是客户端私货【G，已升级为反编译证据】——`conflictStrategy` 是结构体 `+0xb4` 的 32 位整型，由通用 JSON 取值器读入（§11-3），客户端内**无按值分派**，故**取值域由服务端定义** |

对照我们：唯一的"冲突"逻辑是并发撞唯一键后回读（`DriveFileService.java:236-245`），业务口径上等于静默覆盖。

---

## 7. 上传：三段事务 + 分片序号【S】

- 初始化：`{}/attachment/upload/transaction?file_size={}&chunk_numbers={}`
- 逐片：`{}/attachment/upload/chunk?upload_id={}&chunk_sequence={}`
- 提交：`{}/attachment/upload/transaction?file_size={}&chunk_numbers={}&upload_id={}`
- 失败面：`center chunked upload: read error at seq {}, expected {}, got {}`（每片声明期望字节数并回读校验）、`center chunk upload seq {} biz error`。
- 下载也分片：`center chunked GET`。
- 三种上行模式常量本轮重新核实存在：`HttpToCenter` / `HttpToOssHeaderSignature` / `HttpToOssWithAk`（客户端持什么凭据由协商决定，不是静态密钥）。
- 分片大小 `partSizeOfClient` = **4 MiB**（【G】，已闭合，推导见 §3.1.1：分片缓冲、循环封顶、分片数公式三处互证）。客户端提议，服务端在 transaction 响应里裁定。
- 校验范式：`check_zip_file_valid: checking file {}, expect_size: {}, expect_md5: {}`——size + md5 双校验是钉盘的通用完整性写法。

对照我们：阈值 `multipartThreshold = 32 << 20` 在 `internal/drive/upload_session.go:26`（写死），分片 8 MB 由服务端 `DriveProperties.java:79` 下发，会话指纹是 `空间+路径+size+mtime`（`upload_session.go:39-48`）——**不含内容哈希**，改过一次内容后 mtime 变，续传会话必然对不上。

---

## 8. 完整性自愈与脏数据处理【S】

`fix_space_rename_dirty_data checking/fixing/successfully fixed {} entries for space_id={}`、`fix_cloud_invalid_name_dirty_data completed, fixed {}/{} entries`、`update root localid failed error`、`restore sync state, path: {}`、`recycle remove_expired_log_files from version {}`、`remove_all_old_dpsync_version: ... trying move_to_trash`。
含义【推】：引擎把"索引会坏"当常态设计，带三类自愈（改名脏数据、非法名脏数据、锚点根丢失）+ 本地库版本目录的定期裁剪。我们没有任何一致性巡检/修复入口。

回收站与版本：删除在本地是进 **Windows 回收站**（`move_to_trash` 走 `IFileOperation` / `SHCreateItemFromParsingName`），远端恢复走 `addDentryByHistory` + `remote_history` 表（`history_current_ver` / `history_has_ver` / `get_first_history_event`）。图标名 `RecycleBin_*`、`FileHistory_*`、`DriveHistory_*` 也在产物里。
→ 我们的 `deploy/ruoyi-db/easyshare-file.sql:15` 明确写着"回收站/版本态字段留待后续切片"，删除即物理删对象（`DriveController.java:142-143`、`DriveFileService.java:209-214`）。

权限与配额落到文件系统：`remove_directory_limit` 一整套 NTFS ACL 操作（`get_user_sid` / `alloc_acl` / `add ace` / `set_file_sec`）+ `priviliage_limit_toast`，把"这个空间只读/禁止下载"表达成本地 ACL；文案 `kKey_PermissionLimit`、`kKey_FileCanNotDownloadForPermissionLimit`、`kKey_CapacityLimit`、`kKey_FileSizeLimit`，硬上限 `File "{}" exceeds 100GB, cannot be synced`，空间不足 `space capacity not enough`，限速 `dpsync_upload_speed_limit_bytes_per_second` / `dpsync_download_speed_limit_bytes_per_second`【S】。

---

## 8.5 【2026-10-16 增补】同步引擎的源码模块地图（【G】，编译进产物的路径）

`sync_frame.dll` 里带 **68 条源文件绝对路径**（日志宏的 `__FILE__`），构成一份**未经裁剪的模块清单**——比字符串日志更能说明引擎怎么分层。根为 `D:\sync_main\src\`：

| 模块 | 文件数 | 代表文件 | 职责【推】 |
| --- | --- | --- | --- |
| `sync_frame\tasks` | 15 | `task_queue_impl_win.cpp`、`list_dir_task.cpp`、`edit_lock_manager.cpp`、`task_event.cpp` | 任务队列与事件驱动（引擎心脏） |
| `sync_frame\repository` | 13 | `remote\oss_client.cpp`、`remote\remote_filesystem.cpp`、`local\local_filesystem.cpp`、`local\self_exciting_suppressor.cpp` | 本地/远端两个文件系统仓库 |
| `sync_frame\storage` | 11 | `sync_db.cpp`、`cached_local_db.cpp`、`cached_remote_db.cpp`、`remote_history_db.cpp`、`config_db.cpp` | §2 的三棵树各一个 `*_db`，一表一文件 |
| `sync_frame\monitor` | 6 | `local\platform\win32\read_directory_changes_watcher.cpp`、`remote\remote_monitor.cpp` | 双向变更监听（本地走 Win32 `ReadDirectoryChangesW`） |
| `sync_base\util` | 5 | `filesystem_wrapper.cpp`、`move_merger.cpp`、`path_tree.h` | 跨层工具（`move_merger` 正对应 §6 的移动冲突） |
| `sync_base\dt_task_queue` | 4 | `dt_task_queue.cc`、`dt_task_queue_mgr.cc` | 通用任务队列底座（被 tasks 层复用） |
| `sync_frame\sync` | 3 | `sync_instance.cpp`、`sync_node.cpp`、`sync_status.cpp` | 同步编排与状态机 |
| `sync_base\service` | 3 | `gray_service.cpp`、`device_desc_service.cpp`、`ut_service.cpp` | 灰度 / 设备 / 埋点 |
| `sync_frame\cloud_filter_fuse` | 2 | `cf_fuse_node_impl.cpp`、`thumbnail_provider.cpp` | **CfApi 占位符实现**（§5 的落点） |
| 其余单体 | 16 | `sync_app.cpp`、`sync_frame.cpp`、`ipc_client_v2.cpp`、`upgrade.cc`、`plugin_manager.cpp`、`qui_s\custom_notification\custom_notification_manager.cpp` | 应用壳 / IPC / 升级 / 插件 / 通知 |

**三条可直接借用的结构判断**：

1. **`storage` 一表一文件**（`cached_local_db` / `cached_remote_db` / `sync_db` / `remote_history_db` / `edit_lock` 各一个）——印证 §2 的"三棵树各自独立落库，不共用一张大表"。我们把目录层、回收站、上传会话都压在一张 `es_file` 上是相反的取向。
2. **`repository` 明确分 `local` / `remote` 两套文件系统抽象**（`platform_filesystem.cpp` 之上再分平台实现）——对应我们 `internal/spacedav`（远端 WebDAV 视图）与 `internal/drive`（控制面客户端）的分离，但**我们没有"本地仓库"这一层**（改动由 OS 直接做，我们被动接受 WebDAV 请求）。
3. **`tasks` + `monitor` 是两个独立模块**（15 + 6 文件）：变更监听只产出事件，任务层负责收敛——这是"事件驱动 + 队列"而非"请求-响应"。我们目前是纯请求-响应（WebDAV 调一次算一次），**没有事件队列，也没有"谁改的"这条线索**。

> 口径：路径是编译期 `__FILE__` 字面量，**证明这些源文件参与构建**，不证明其当前生效分支。文件数按"去重后的完整路径"计。

---

## 9. 差距清单（钉盘证据 → 我们现状 file:line）

> ⚠️ **本表是 2026-10-15 的快照，到 10-18 已有 8 行过期**（增量列举、按需占位符、冲突策略、
> 内容哈希秒传、回收站、服务端改名移动、目录对象、进度回报都已落地）。
> 现状一律以 [`docs/benchmark-absorption.md`](../benchmark-absorption.md) 为准——
> 那张表的每条「文件 · 符号」引用都由 `scripts/diag/check-doc-refs.mjs` 校验，不会烂。
> 留这张表只作取证记录，不作现状依据：**本轮就是照它说话才错了两次**。

| 能力 | 钉盘 | EasyShare 现状 | 结论 |
| --- | --- | --- | --- |
| 条目身份 | `local_id`/`remote_id`，路径只是属性 | 唯一键含 `file_path`；`internal/drive/client.go:33-38` 有 `FileId` 但不参与写路径 | **半缺**：身份有了，写语义仍按路径 |
| 远端版本 | `dentryVersion` + `preDentryVersion` CAS | 无 version 列，无 If-Match（`internal/spacedav/spacedav.go:39-44` FS 接口仅 4 方法） | **缺** |
| 增量同步 | `spaceVersion` + `listRedologs` + `nextToken` + 全量兜底 | 全量 list，无游标无 etag（`client.go:75-82`、`DriveStorage.java:272-279`） | **缺**（最关键） |
| 按需占位符 | CfApi placeholder + pin/unpin + dehydrate | WebDAV 全量物化，写文件整份进内存（`files.go:56-103`） | **缺** |
| 冲突策略 | 矩阵化 9+ 条规则 + `conflictStrategy` 协议字段 | 静默覆盖（`DriveFileService.java:51-63`） | **缺** |
| 内容哈希/秒传 | `md5` + `md5_reliable` + `genMd5` 服务端代算 | 全仓无文件级内容哈希 | **缺** |
| 回收站 | 本地进系统回收站 + 远端 `addDentryByHistory` | 物理删除（`DriveController.java:142-143`），SQL 注释自认留待后续 | **缺** |
| 编辑锁 | `tryGet/release/batchInfoEditLock` + `lock_expire_time` | 无 | **缺** |
| 服务端改名/移动 | `renameDentry` / `moveDentry` | 控制面无 rename/move/copy 端点，WebDAV 改名=下+上+删 | **缺** |
| 目录对象 | `dentryType` + 订阅树 + `depth` | 空目录只活在内存（`spacedav.go:169-181`），`listTTL=3s` | **缺** |
| 配额/策略落本地 | 100 GB 单文件、容量文案、NTFS ACL | 无 | **缺** |
| 自愈巡检 | `fix_*_dirty_data` 三类 | 无 | **缺** |
| 传输记录 | `sync_record` + `rpc: get_recent_sync_record` | 有 `internal/task`，但重启即全量 JSON 覆盖，无历史查询面 | **部分** |

---

## 10. 改造切片建议（**未实现，仅路线**；顺序按依赖）

1. **目录身份与版本列**：`es_file` 加 `content_md5 / md5_reliable / dentry_version / del_flag / trash_time`，写路径全部改走 `file_id`；预签名 PUT 引入条件提交（对应 `preDentryVersion`）。可验证点：同名并发上传不再互相吃掉，两份都在。
2. **增量列举**：给空间加 `space_version` 与变更日志表，接口出 `nextToken` + `hasMore`，客户端存 `last_space_version`，日志不足时退全量。可验证点：改一个文件只传一条变更，不重列整空间。
3. **回收站 + 服务端改名/移动**：删除转 `del_flag`，新增 rename/move/restore 端点，WebDAV 层不再"下载+上传+删除"模拟改名。可验证点：资源管理器改名是原子操作且保留 FileId。
4. **占位符（最后做，收益最大也最重）**：`internal/namespace` 从"注册表 NameSpace + UNC"换成真扩展 + CfApi，先做只读占位（列表零下载、点开才拉、支持"始终保留在本地/释放空间"），再谈双向同步。可验证点：10 GB 目录在本地占 0 字节，双击首开延迟可接受。
5. （可选）**内容哈希与秒传**：依赖切片 1 的 md5 列落地后才能做。

---

## 11. 仍未取到的证据

> 2026-10-16 更新：原始三条中 **`event_type` 枚举已闭合**（§4.1，9 值 + 数值 + 兜底），**`partSizeOfClient` 已闭合 = 4 MiB**（§3.1.1），并新增了源码模块地图（§8.5）。剩余如下。

按价值排序，全部标注卡在哪一步：

1. ~~`partSizeOfClient` 的具体数值~~ → **已闭合 = 4 MiB**（§3.1.1，三处互证）。
   剩余：**服务端覆盖规则**（transaction 响应里 `chunk_numbers` 是否会改客户端提议值）。要取需服务端响应样本（运行时）或继续反编译 transaction 的**响应处理**分支——本轮只反编译了**请求构造**侧。

2. **redolog 每条日志的字段结构**（`event_type` 之外）。
   现状：`eventType` 字段名与 9 值枚举已定（§4.1）；`redologs` / `nextToken` / `newestSpaceVersion` 三个响应字段名已定（§4）。**缺单条 redolog 的字段清单**。
   取法：定位 `listRedologs` 响应反序列化器（`fromJson` 族）并反编译——本轮已探明该族存在（`toJson <字段> fail` 共 117 条），但未逐个归属到响应类型。

3. **`conflictStrategy` 的取值集合 —— 本轮已确认「静态取不到」。**
   进展（值得记下来省得下次重复投入）：`FieldUsers.java` 以位移 `0xb4` 为针扫出 **39 个函数**，按 `conflict` 关键字过滤后剩 5 个；逐个反编译后确认**全部是序列化/反序列化器**（`FUN_18015c210` / `FUN_18015d0f0` / `FUN_18017a820` / `FUN_18017bd10` / `FUN_18049d100`）。
   关键观察：反序列化分支写成 `memcmp(key,"conflictStrategy",0x10)==0 → 读 param_1+0xb4`，即**该字段由一个通用的 JSON 取值器按 32 位整型读入**（`thunk_FUN_1805a2700(uVar12 + 0x10, param_1 + 0xb4)`），**没有任何按值分派的 switch**。
   结论：**取值域不在客户端**——要么服务端下发什么就存什么，要么常量定义在别处（服务端）。这一条**必须靠运行时样本或服务端侧证据**，继续静态挖是负收益。

4. **`sync.db` 的真实行样本。**
   本机账户未开启过钉盘同步，三棵树为空。要拿第一手数据，得在自己的钉钉里真的订阅一个空间（**会产生云端写入，需明确授权**）。
   这一项同时能坐实 §4.1 末尾那条口径：`remote_history.event_type` 与 9 值枚举是否同一套编码。

5. **无任何运行时观察。**
   以上全部为静态产物证据（REA Evidence + 字符串 + 自建 Ghidra 工程反编译 + 只读本机库）。**没有任何一次进程级或协议级运行时观测**。

---

## 12. 复现本轮（2026-10-16）增补的完整命令

```powershell
# 0) 前置（见 toolchain-rea.md §1）
$env:JAVA_HOME          = 'D:\Develop\java21'
$env:GHIDRA_INSTALL_DIR = 'D:\Develop\ghidra_12.1.4_PUBLIC'

# 1) 出正式 REA Evidence（关键：放宽默认 330 s 启动超时）
$env:REA_GHIDRA_STARTUP_TIMEOUT_MS = '1800000'
rea analyze output\sync_frame.dll --provider ghidra --format json
#   -> ev_dc508b2cc52ed00486b4f95ed854ea019224bc9b1e1e513db6794aebc9da97b8

# 2) 建持久化 Ghidra 工程（一次性 270 s，之后所有查询复用）
& "$env:GHIDRA_INSTALL_DIR\support\analyzeHeadless.bat" `
    output\ghidra-proj dingpan -import output\sync_frame.dll -overwrite

# 3) 之后每次定向取证（8–24 s）
$env:PROBE_OUT = "$PWD\output\probe.txt"
& "$env:GHIDRA_INSTALL_DIR\support\analyzeHeadless.bat" `
    output\ghidra-proj dingpan -process sync_frame.dll -noanalysis `
    -scriptPath scripts\reverse `
    -postScript ProbeStrings.java 'LocalModify' 'RemoteModify'
```

脚本：`scripts/reverse/` 下的 `ProbeStrings.java`（字符串针 → xref → 反编译）、`ConstRefs.java`（立即数扫描 → 命中函数反编译）、`FieldUsers.java`（结构体偏移 → 字段读写点）。

---

相关：`docs/intelligence/2026-10-13-dingtalk-minutes-reverse.md`（听记侧，H5 bundle 路线）、`docs/toolchain-rea.md` §8（大目标取证通路）。
