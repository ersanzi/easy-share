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

`sync_frame.dll` 本轮**没有 REA Evidence**，原因要说清：

- `rea doctor`（6.4.0，Ghidra 12.1.4 + JDK 21）全项 healthy，`ghidra-native_decompiler` 确认识别 `pe x86_64`。
- `rea analyze sync_frame.dll --provider ghidra` 两次失败：第一次路径组件准入被钉钉进程占用 `%APPDATA%\DingTalk` 撞共享冲突（把 DLL 复制到中性目录后 SHA-256 不变）；第二次跑到 `list_documents` **`provider_timeout` 330 000 ms** 超时。
- 同一条通道在 1.7 MB 的 `DingSyncShellExtension.dll` 上约 4 分钟正常出证（见下），所以这是**目标体积超预算**，不是配置或引擎问题。

**REA Evidence（本轮实际产出）**：

| Evidence ID | 操作 | 对象 | 结论 |
| --- | --- | --- | --- |
| `ev_e8edcc100cc1eb505874dcd25b9347df5d2a9fbbc386000934fac4f11cae5621` | `analyze`（provider ghidra 12.1.4） | 扩展 DLL | 12 段、**4 249 个函数**、1 054 条串；`.rsrc` 独占 703 488 B |
| `ev_6cf534251cc7e9bd7416f8ed3b64b664696722207ae5145d999e52ad5b67dc98` | `inspect-pe-resources`（无需引擎） | 扩展 DLL | 覆盖完整（examined_entries 77）：**37 条资源、4 个 RT_GROUP_ICON**，每组 8 个尺寸（16/24/32/48/64/96/128/256，32 bit） |

REA 自报的限制照抄：非 `RT_GROUP_ICON` 的载荷不解析；语言回退、图像解码、以及"资源管理器是否真的加载了这些图标"均未观察。

因此其余结论标签为：**【S】**= 二进制字符串扫描（`D:\Develop\_dl\dingpan\strings.py`、`strings2.py`，ASCII + UTF-16LE 双通道）；**【D】**= 本机 DingpanSync SQLite 实测副本；**【R】**= 上表 REA Evidence；**【推】**= 由前述做的推断；**【?】**= 未知。字符串是编译进产物的字面量，属硬证据；但**日志串只能证明存在该分支，不能证明运行时真的走到**（全文无运行时观察）。另外中文文案不在同步引擎也不在扩展里（UTF-8 / UTF-16LE 中文串扫描命中 **0**，产物内文案为英文），文案层【?】。

复现：

```bash
# 字符串面（本轮实际用的两个过滤器集，输出 12 / 11 个语义分块）
python strings.py  sync_frame.dll  sync_frame_strings.txt
python strings2.py sync_frame.dll  sync_frame_strings2.txt
# 本地库（必须连 -wal 一起拷，删掉 -shm 让 SQLite 从 WAL 重建索引）
cp "$APPDATA/DingTalk/<uid>_v2/DingpanSync/sync.db"{,-wal,-shm} <中性目录>/
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

## 3. 远端模型：dentry + 版本 + 前值（CAS）【S】

字段面（编译进产物的字面量）：`dentryId / parentDentryId / **preParentId** / newDentryName / dentryName / dentryPath / dentrySize / dentryType / **dentryVersion** / **preDentryVersion** / dentryMd5 / dentryClientMd5 / localDentryMd5 / spaceId / remote_space_id / syncAddDentryParamList / existingDentry / conflictStrategy / conflictHandle / partSizeOfClient`。

`preXxx` 系列是**乐观并发控制**：写请求带"我看到的旧版本/旧父节点"，服务端不匹配就拒绝【推】。对照我们：`DriveController.java:96-111` 预签名 PUT 无任何条件检查，`DriveFileService.java:51-63` 同路径直接 upsert 覆盖 size——两个人同时改同名文件，后写的静默赢，前一份直接没了。

服务端权威纠正也有日志：`add file dentry reponse parent diff, expect {}, actual {}, will change to expect`（客户端算的父节点与服务端返回不一致时，以服务端为准）【S】。

**28 个 CSpaceSync 方法全量**（`/r/Adaptor/CSpaceSync/` 前缀，本轮完整枚举）：
`addDentry` `addDentryByHistory` `batchAddDentry` `batchInfoEditLock` `batchInfoUpload` `checkSpaceFileCountLevel` `checkUserEnableSync` `deleteDentry` `genDeviceId` `genMd5` `getDentryInfo` `getNewestSpaceVersion` `getToken` `infoDownload` `infoUpload` `listDentrysByFolder` `listRedologs` `listSubscribes` `monitor` `moveDentry` `releaseEditLock` `renameDentry` `reportMd5NotMatching` `subscribe` `tryGetEditLock`
（另有 `stringify request body for ...` 三条日志串凑成 28 条命中，实际 RPC 数 25。）

深链：`dingtalk://dingtalkclient/page/yunpan?route=previewDentry&spaceId=`【S】。

---

## 4. 增量同步协议：space version + redolog【S】

这是钉盘"目录再大也不卡"的正解，也是我们最缺的一块。

- 每个 space 有一个单调版本 `spaceVersion`，客户端持久化 `last_space_version`：`init_space_version_from_db` / `update_space_version_to_db` / `reset_space_version_from_remote`。
- 拉变更不是列目录，是**拉日志**：`listRedologs(space_id, last_space_version)` → 返回 `redologs` + `nextToken` + `newestSpaceVersion`。分页靠 `nextToken`，不是一次给完。
- **日志断了就退化成全量重建**：`listRedologs from space {} failed for changed too much, last_space_version {}` → `full_rebuild, local file count: {}` / `update_all_space_from_redolog`。这是必须一起抄的兜底，否则增量协议在长期离线后无解。
- 版本自身也会冲突：`mark_space_version_conflict` / `clear_space_version_conflict` / `space_version_conflict timeout, allow one retry` / `erase_space_version {}, current version: {}`；`listRedologs space {} newestSpaceVersion differs from max_version` 是"我拉到的不是最新"的检测。
- 变更实时性靠推送+RPC 通知：`notify_space_version_updated` → `rpc: notify_space_version_updated`（引擎→UI），`on_space_version_update ignore, cur/new version` 做版本单调性过滤。
- **订阅制而非全盘同步**：`subscribe` / `subscribe_remove` / `listSubscribes` / `listSubscribes` 返回 `subscribes`，粒度是 space 与 org（`subscribe_remove id not a space or org {}`）；目录展开深度写在 `add_refresh_dir: {}, list type: {}, depth: {}` 与 `remote_folder_sync_status.depth`。`checkSpaceFileCountLevel` + `is_large_space` 专门处理巨型空间。
- 引擎生命周期是自管的：`idle too much has no subscribe and task, will uninstall`（长期没订阅就自卸载）。

对照我们：`internal/drive/client.go:75-82` 一次拉全量，`DriveController.java:62-81` → `DriveStorage.java:272-279` 在 S3 内部翻 1000 条聚合成单 List，**接口没有分页参数、没有游标、没有 etag、没有增量**；`search_surface.go:123-124` 每次搜索全量拉两个空间。

---

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
| 参数层 | 请求体带 `conflictStrategy` / `conflictHandle`，即策略是**协议字段**，不是客户端私货【推】 |

对照我们：唯一的"冲突"逻辑是并发撞唯一键后回读（`DriveFileService.java:236-245`），业务口径上等于静默覆盖。

---

## 7. 上传：三段事务 + 分片序号【S】

- 初始化：`{}/attachment/upload/transaction?file_size={}&chunk_numbers={}`
- 逐片：`{}/attachment/upload/chunk?upload_id={}&chunk_sequence={}`
- 提交：`{}/attachment/upload/transaction?file_size={}&chunk_numbers={}&upload_id={}`
- 失败面：`center chunked upload: read error at seq {}, expected {}, got {}`（每片声明期望字节数并回读校验）、`center chunk upload seq {} biz error`。
- 下载也分片：`center chunked GET`。
- 三种上行模式常量本轮重新核实存在：`HttpToCenter` / `HttpToOssHeaderSignature` / `HttpToOssWithAk`（客户端持什么凭据由协商决定，不是静态密钥）。
- 分片大小 `partSizeOfClient` **仍是【?】**：字面量在，赋值点没定位到（上一轮 31 351 个函数里没顺着数据流找到，本轮 REA 超时，仍未知）。客户端提议值，服务端可覆盖【推，因 transaction 请求由服务端定 chunk_numbers】。
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

## 9. 差距清单（钉盘证据 → 我们现状 file:line）

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

- `partSizeOfClient` 实际数值与服务端覆盖规则（需 `rea decompile` 或自写 Ghidra 脚本定位赋值点；REA 全量分析在本目标上 330 s 超时，是通道预算问题不是引擎缺失）。
- redolog 的 `event_type` 枚举取值与每条日志的字段结构（字符串里没有枚举名，需反编译或抓包）。
- `sync.db` 的真实行样本：本机账户未开启过钉盘同步，三棵树为空。要拿第一手数据，得在自己的钉钉里真的订阅一个空间（会产生云端写入，需明确授权后再做）。
- `conflictStrategy` 的取值集合（客户端本地策略 vs 服务端下发）。
- 无任何运行时观察：以上全部为静态产物证据。

相关：`docs/intelligence/2026-10-13-dingtalk-minutes-reverse.md`（听记侧，H5 bundle 路线）。
