# WPS 云盘对标研究与 EasyShare 网盘演进建议

> 研究日期：2026-10-13
> 研究对象：WPS 云盘 Windows客户端（个人/企业空间形态）
> 研究方式：**本机残留元数据取证**（`C:\Users\lishi\WPS Cloud Files\.10709431\*.db` / `*.xml`）+ 产品截图 + 官方文档
> 补充文档：[`cloudreve-benchmark.md`](cloudreve-benchmark.md)（服务端架构对标，本篇为桌面端产品形态对标）
> 本轮结论：**只做调研，不动代码。** 公司部署观察期（2026-10-13 起两周）内不改网盘数据面。

## 1. 研究目的与路线说明

### 1.1 路线说明：本轮只走元数据取证（**§1.1 的 REA 结论已于 2026-10-15 更正，见 §1.1b**）

用户给出 [REA（逆向一切）](https://github.com/morluto/rea/blob/main/README_zh.md) 与 WPS 云盘主界面截图，要求「学习 WPS 云盘完善网盘」。2026-10-13 当时判断 REA 路线走不通，理由有两条：

1. **WPS 云盘客户端本机未安装。** 仅有 `C:\Program Files (x86)\Kingsoft\office6\wpscloudsvr.exe`（云服务进程，无 UI）和 `C:\Users\lishi\WPS Cloud Files\` 同步目录（空的，仅元数据）。
2. 推测 **REA 不适用**：REA 的主力能力是 Electron `app.asar` 拆包与网站运行时观察，而 WPS 云盘客户端是 **Qt 原生桌面程序**，不含 `resources/app.asar`，不匹配 `analyze-javascript-application` 路径。

第 2 条于 2026-10-15 被证伪，见下节。**本节的元数据取证路线仍然有效，且是本文全部结论的来源**——两条路线是互补而非替代关系。

### 1.1b 【2026-10-15 更正】REA 可用，且原生 PE 是本机主路径

2026-10-15 重新核实后的**实测事实**：

1. **WPS 云盘客户端本机是安装的，且是完整形态。** 旧结论只看了 `C:\Program Files (x86)\Kingsoft\`。实际安装在 `D:\Program\office\WPS Office\12.1.0.29161\`（另有 `.28505`、`.21915` 两个旧版本共存），云盘引擎与 Shell 扩展齐全：

   | 产物 | 体量 | 角色 |
   | --- | --- | --- |
   | `office6\addons\ksyncengine\ksyncengine.dll` | 1.2 MB | **同步引擎，CfApi 主调用方** |
   | `office6\qingnse64.dll` | 16.7 MB | 命名空间/Shell 宿主 |
   | `office6\qingshellext64.dll` | 868 KB | 资源管理器 Shell 扩展（薄壳） |
   | `office6\wpscloudsvr.exe` | 977 KB | 云服务进程 |

2. **REA 6.4.0 在本机完全可用**，可用性故障是两个可修的环境变量问题，与目标形态无关：
   - `GHIDRA_INSTALL_DIR` 未设置 → 设为本机已解压的 `D:\Develop\ghidra_12.1.4_PUBLIC`。
   - **`JAVA_HOME` 指向 `D:\Develop\java17`（17.0.9）→ Ghidra 12.1.4 要求 64-bit full JDK 21+，这是真正的拦截点**；本机已有 `D:\Develop\java21`，切过去即可。

   ```powershell
   $env:GHIDRA_INSTALL_DIR = 'D:\Develop\ghidra_12.1.4_PUBLIC'
   $env:JAVA_HOME          = 'D:\Develop\java21'
   rea decompile <pe> --procedure entry --provider ghidra --format json
   ```

   实测产出真 Evidence（`provider.id=ghidra`、`version=12.1.4`、`authority=shipped-artifact`）。

3. **纠正「REA 只擅长 JS/Electron」的误解。** REA 的原生能力是一等公民：`inspect-pe-resources`、`inspect-binary-layout`、`analyze`、`decompile`、`function`、`xrefs`、`trace_call_path` 等均走 Ghidra/IDA/Hopper provider。REA 的正确用法矩阵是「JS/Electron → Electron provider；原生 PE → Ghidra provider」，而非「只做 JS」。

4. **本机耗时基线**（Windows x64 + Ghidra 12.1.4 + JDK 21，实测）：

   | 命令 | 目标 | 耗时 | 结果 |
   | --- | --- | --- | --- |
   | `decompile --procedure entry` | 282 KB exe | **53 s** | ✅ 出伪码 + Evidence ID |
   | `decompile --procedure <导入名>` | 1.2 MB DLL | **89 s** | ❌ `invalid_value: Unknown Ghidra procedure name`——**外部导入符号不是 Ghidra 函数名，必须先经 xrefs 定位真实地址** |
   | `analyze` | 1.2 MB DLL | **91 s** | ✅ 出 Evidence ID |

   > 对 10 MB 级目标（钉钉 `sync_frame.dll`）此基线线性外推约 12–15 分钟，与 2026-10-15 钉钉取证记录的一致。

**对本文其余部分的影响**：§2–§5 的元数据结论不受影响；但 §6「明确不照搬」与 §10 结论中凡以「Qt 原生、无法深挖」为前提的推论，都应在补齐 PE 取证后复核。**本轮（2026-10-15）未执行该复核**，仅更正路线判断本身。

### 1.2 改用的取证方法与优势

改走**本机 SQLite 元数据取证**。WPS 云盘同步目录在用户profile 下留有一整套运行时数据库，其表结构就是产品数据模型的直接投影。相比扒 JS 源码，这条路对「信息架构」这类问题**证据强度更高**——表名与字段是产品自己承认的建模，而非逆向推测出的调用链。

风险边界：本地库中的业务表均为空（本账号未使用过云文档同步），因此本文结论**基于表结构与官方产品资料交叉核验，不基于实际数据样本**。凡涉及"它怎么用这些字段"的判断，均标注为推断。

## 2. 证据范围

| 证据 | 路径/来源 | 提取到的内容 |
| --- | --- | --- |
| WPS Cloud Files 运行时目录 | `C:\Users\lishi\WPS Cloud Files\.10709431\` | 8 个 SQLite 库 + 3 个 XML，共 21 张表 |
| 同步目录本体 | `C:\Users\lishi\WPS Cloud\WPS Cloud Files\`、`C:\Users\lishi\WPS Cloud Files\` | 空目录，确认客户端未启用 |
| WPS Office 云服务 | `C:\Program Files (x86)\Kingsoft\office6\wpscloudsvr.exe` | 仅云服务进程，无客户端 UI |
| 产品截图 | 用户提供（企业空间形态） | 左侧导航树、顶部三卡片入口、文件列表列定义 |
| REA 项目文档 | `morluto/rea` README + `docs/installation.md` | 能力边界与客户端支持矩阵 |

### 2.1 本机表结构全清单

```text
cloudfile.db
  cloud_table(dbId, fileId, colorMark)
recentfile.db
  cloudfile_not_uploaded(dbId, filePath, localId, cacheItemId, parentId,
                         groupId, cloudPath, status, errorCode, errorMsg, time)
docTag.db
  tagInfo(tagId, title, type, rank, cTime, mTime, attr, extendInfo)
  tagDocMapping(tagId, docId, docType, rank, cTime, mTime, source, attr, extendInfo)
toDeleteFiles.db
  to_delete_files(id, fileId, parentId, fileName, filePath, lastModify,
                  fileSize, type, owner, extendInfo)
syncassistant.db
  sync_assistant_table(id, syncTaskId, syncParentTaskId, status, type,
                       transferTaskId, transferRootTaskId, cacheItemId, fileId,
                       groupId, parentId, fileName, fileSize, completeSize,
                       createTime, completeTime, fileVersion, modifyTime,
                       localPath, cloudPath, roamingLocalPath,
                       errorCode, errorMsg, winErrorCode, ...)
transferhelper.db
  device_table(hash_id, device_id, user_id, app_id, device_name, device_type, os_type)
  msg_record_table(msg_id, msg_time, msg_type, msg_text, status,
                   sender_device_id, recipient_device_id, file_id, ...)
batchupload.db
  batch_upload_table(id, state, parentTaskId, uploadTaskId, type, fileName,
                     fileSize, fsFileId, fileId, groupId, parentFileId,
                     createTime, completeTime, errorCode, errorInfo)
localfileuploadrelated.db
  local_file_upload_related_table(id, localPath, uploadCachePath, fileId, ...)
recentcloudfolder.xml
  root > folderList / fileList（最近访问目录，本机为空）
specificcloudteam.xml
  root > teamBaseInfo(totoalTeamCount, entranceReadMark) / teamList（团队空间，本机为空）
startpagerecent.xml
  root > fileList > file（36 KB，本机有真实数据，见 §2.2）
```

### 2.2 `startpagerecent.xml` 的真实样本（字段级证据）

本机该文件有实际数据，暴露了 WPS 云盘"最近访问"记录的完整建模：

```xml
<file fileSrcType="group" fileId="258849535890" fileState="0"
      name="IITA有偿提问表.xlsx" fileType="sharefile" fileVersion="4"
      size="13392" groupId="171468045" cloudPath="收到的文件"
      appType="et" operation="open"
      lastTerminalType="Browser"
      lastTerminalName="Mozilla/5.0 ... MicroMessenger/7.0.20 ..."
      oriTerminalType="云文档" oriTerminalName="我收到的轻地址"
      lastUserId="208827526" accessTime="1698124426"
      lastUserModifyTime="1698123290" isSyncFolder="0" fake="0" />
```

值得注意的建模选择：

- **`fileId` 与路径并存**——`fileId` 是稳定身份，`cloudPath` 是位置，两者解耦。**与 EasyShare `es_file` 的设计判断一致。**
- **`fileVersion` 是一等字段**——最近列表直接带版本号，说明版本是文件的核心属性而非附属功能。
- **记录来源端点**——`lastTerminalType` / `oriTerminalType` / `lastTerminalName` 区分"从哪台设备、哪个应用打开的"（本例中经微信内置浏览器收到）。
- **`fileState` 独立于路径存在**——文件状态与位置分离，是回收站/版本的前置建模。
- **`groupId` 与 `owner`/`userId` 分离**——群组维度独立于用户维度。

## 3. 截图信息架构与表结构的映射

截图左侧导航树不是随意排布，每一项都能对应到本地表：

| 截图左侧栏 | 对应表/字段 | 模型含义 |
| --- | --- | --- |
| 我的云文档 | `es_file.space_type=personal`（对标） | 个人空间 |
| 团队文档 | `specificcloudteam.xml.teamBaseInfo` | 团队空间，独立计数与入口红点 |
| 我的设备 | `transferhelper.device_table` | 设备维度：hash_id + device_type + os_type |
| **标签** | `docTag.tagInfo` + `tagDocMapping` | **标签是多对多独立实体**，有 type/rank/extendInfo |
| **星标** | `cloudfile.colorMark` | **标记位挂在文件上**（单值，不是标签） |
| 共享 | `startpagerecent.fileType=sharefile` | 分享关系独立于文件本体 |
| 常用 | 推断为 `fileState`/rank 派生 | 推断：非独立表，是派生视图 |
| **回收站** | `toDeleteFiles.to_delete_files` | **软删 + 完整原路径快照** |

另有截图顶部三张入口卡片，对应三套不同的数据通路：

| 卡片 | 通路 | 落表证据 |
| --- | --- | --- |
| 上传（选择本地文件） | 一次性上传 | `batch_upload_table` |
| 文档云同步（开启自动同步） | 目录监听同步 | `sync_assistant_table` + `local_file_upload_related_table` |
| 网盘文件夹（指定文件夹同步） | 固定根同步 | `sync_assistant_table.syncFolderPath` 语义 |

**这张表是本篇最有价值的产出**：它证明 WPS 云盘把「上传」「监听同步」「固定根同步」当成三种独立任务建模，各自有状态表，而不是一个通用上传通道的参数。

## 4. 最重要的架构发现

### 4.1 `fileId` 与路径解耦——EasyShare 已做对

WPS 的 `startpagerecent.xml` 同时存 `fileId` 与 `cloudPath`，`to_delete_files` 同时存 `fileId` / `parentId` / `filePath`。EasyShare 的 `es_file` 表（2026-09-06 落地）同样是 `file_id` 稳定身份 + `file_path` 位置，`internal/drive/client.go` 的 `Object` 结构同时返回 `Path` 与 `FileId`。

**判断：这一项无需改动。** 2026-07的 Cloudreve 对标结论（"稳定 File ID 优先于复杂 URI"）在 WPS 这里得到了第三方验证。后续所有新能力都应挂 `fileId`，不要重新引入路径绑定。

### 4.2 回收站是"路径快照"而非"前缀隐藏"

`to_delete_files` 的字段是决定性的：`filePath` + `parentId` + `lastModify` + `fileSize` + `owner` 全部保留。这说明 WPS 的删除是**软删 + 原位置快照**，还原时直接按快照恢复，不需要推测路径。

EasyShare 当前 `CloudDelete`（`app.go:1331`）调用 `client.Delete` 直删对象，`deploy/ruoyi-db/easyshare-file.sql` 注释也写明"回收站/版本态字段留待后续切片（届时加列，不本切片）"。

**判断：这是当前最大的产品缺口，也是 P1 第一优先。** 与 Cloudreve 对标的 P1 结论（回收站/版本）完全一致，两份独立对标指向同一处——说明这不是某家产品的偏好，是文件产品的通识门槛。

### 4.3 标签必须是一等实体，不能是文件上的枚举

WPS 的 `docTag` 是标准多对多：`tagInfo`（标签定义：title/type/rank）+ `tagDocMapping`（映射关系，带 docType 说明标签可跨文件类型）。`rank` 字段说明标签有用户可调的排序权重。

`cloudfile.colorMark` 是另一回事——它是挂在 file 上的**单值标记位**，对应截图里的"星标"。**星标 ≠ 标签**：星标是单值快速标记，标签是多值组织工具。

EasyShare 当前完全没有这两者，且 `CloudFile`（`types/core.ts:59`）只有 7 个字段，无任何标注维度。

**判断：标签依赖 `fileId` 落地（已具备），可作为 P1 末位或 P2 开工。** 但顺序上它必须排在回收站和重命名之后——否则用户给一个会被重命名/删除的文件打标签，标签会跟着漂移。

### 4.4 三种上传通路各自建模，不要合并

`batch_upload_table`（一次性上传，有 `state` 状态机和 `errorInfo`）与 `sync_assistant_table`（同步，有 `syncParentTaskId` 父子层级 + `completeSize/fileSize` 进度 + `roamingLocalPath` 漫游路径）是**两张表两套模型**。

EasyShare 当前 `CloudUpload(targetDir)` / `CloudUploadFolder(targetDir)` / `CloudUploadPaths(paths)`（`app.go:1150-1317`）三条入口最终都收敛到同一个 `Upload` 方法，Upload Session（`internal/drive/upload_session.go`）也只有一种会话形态。

**判断：当前收敛是对的，不应现在拆开。** 消费级桌面阶段没有自动同步需求（阶段 5CfAPI 才是），过早引入父子任务树会让 UI 和持久化复杂化。**记录此差异，作为阶段 5 的输入。**

### 4.5 版本是文件的核心属性，不是附属功能

`startpagerecent.xml` 的每条记录都带 `fileVersion`，`to_delete_files` 带 `lastModify`，`sync_assistant_table` 带 `fileVersion`。版本在 WPS 的模型里是文件身份的一部分。

EasyShare 的 `es_file` 当前只有 `file_size`，无版本列；`Object` 结构有 `LastModified` 但那是 S3 的 mtime，不是版本号。

**判断：与回收站同批做。** 回收站要还原到"删除前的那个版本"，没有版本列就只能还原到"当前对象"——语义不完整。

### 4.6 失败诊断双写错误码

`sync_assistant_table` 同时有 `errorCode`（业务码）+ `errorMsg`（人读文本）+ `winErrorCode`（系统码）；`recentfile.cloudfile_not_uploaded` 有 `errorCode` + `errorMsg`；`batch_upload_table` 有 `errorCode` + `errorInfo`。

EasyShare 的任务错误目前通过 `a.reportError(ctx, err)`（`app.go` 各Cloud 方法）写日志，前端只看到失败/成功二元状态。

**判断：低成本高回报。** Cloudreve 对标 §3.6 已提出统一任务模型，本篇补充了 WPS 的具体做法——错误三件套（业务码 + 人读文本 + 系统码）是最低标准，不需要等统一任务模型落地就能加。

### 4.7 最近访问要记来源端点

`startpagerecent.xml` 记录 `lastTerminalType`（Browser/PC）、`oriTerminalType`（云文档）、`oriTerminalName`、`accessTime`。这不是花哨功能——它是"这个文件我在哪台设备上打开过"的溯源依据，对多端场景（EasyShare 阶段 6「云端与内网融合」）有直接价值。

EasyShare 当前无最近访问概念，但已有全局快捷搜索面板（`Alt+Space`，2026-09-06）走的是"网盘按名过滤聚合"。

**判断：P2。** 成本低（一张本地表 + 读时写一行），但优先级低于文件生命周期本身。

## 5. 能力矩阵

| 能力 | WPS 证据 | EasyShare 现状 | 建议 |
| --- | --- | --- | --- |
| 稳定文件身份 | `fileId` + `cloudPath` 并存 | ✅ `es_file.file_id` + `file_path`（2026-09-06） | **已对标，无需改动** |
| 目录导航/面包屑 | 截图面包屑 | ✅ `driveFolder.ts` 视图推导 | 已落地 |
| 跨目录搜索 | 截图顶部搜索框 | ✅ 含子文件夹 | 已落地 |
| 上传断点续传 | `batch_upload_table.state` | ✅ Upload Session + Multipart（2026-09-06） | 已落地 |
| 在线预览 | — | ✅ `previewKind` 后端声明 | 已落地 |
| **回收站/还原** | `to_delete_files` 路径快照 | ❌ `CloudDelete` 直删 | **P1-1**：es_file 加 `state`/`deleted_at`/`origin_path`，软删 |
| **文件版本** | `fileVersion` / `lastModify` | ❌ 仅 S3 mtime | **P1-1 同批**：es_file 加 `version_id` |
| **重命名/移动** | `file_path` 可变 + `fileId` 恒定 | ❌ 只能新建+删除 | **P1-2**：es_file `file_path` 改写 + 冲突检查 |
| 错误诊断三件套 | `errorCode`+`errorMsg`+`winErrorCode` | ❌ 仅日志 | **P1-3**：低垂果实，可独立于P1-1/2 |
| **多选/批量操作** | 截图批量入口（推断） | ❌ 单文件操作 | **P1-4**：勾选 + 批量删/下载/移 |
| 星标标记 | `cloudfile.colorMark` | ❌ | P2：单值标记位，成本极低 |
| **标签体系** | `docTag` 多对多 + rank | ❌ | P2：必须排在回收站/重命名之后 |
| 最近访问 | `startpagerecent.xml` 端点溯源 | ❌ | P2：阶段 6 多端场景前置 |
| 缩略图缓存 | `file_thumbnail` + `user_thumbnail` | ❌ 通用文件图标 | P2：Cloudreve §3 已列，本篇不新增依据 |
| 团队空间 | `specificcloudteam.xml` | ⚠️ `es_space` 有 shared 空间 | 暂不做：企业空间复杂度高，与当前部署规模不匹配 |
| 三种上传通路 | `batch_upload` vs `sync_assistant` | ⚠️ 已收敛为一种 | **不做**：记录差异，归阶段 5 输入 |
| 设备管理 | `device_table` |⚠️ 无 | 暂不做：与阶段 6 配对一并考虑 |

## 6. 推荐演进顺序（观察期后）

### P1：文件生命周期（回收站 → 版本 → 重命名）

依赖关系是线性的，不能跳：

1. **回收站 + 版本**（同批）。`es_file` 加列：`state`（active/recycled/purged）、`deleted_at`、`origin_path`、`origin_parent_id`、`version_id`。删除改为置 `state=recycled` + 保留对象，列表接口按 `state` 过滤。回收站视图直接查 `state=recycled`，还原按 `origin_path` 复位。这是 Cloudreve §5-P1 与 WPS §4.2 的交集，两份对标共同指向。
2. **重命名 / 移动**。`es_file.file_path` 改写 + `uk_es_file_path` 冲突检查（唯一键已存在，直接可用）。改名后RustFS 对象需同步搬移或改用新 key + 旧 key 待回收。这是回收站的前置——先有还原再有移动，否则用户移动完发现移错了无处可退。
3. **错误诊断三件套**。独立小切片，不依赖前两项。
4. **多选 / 批量操作**。勾选态 + 逐项结果（允许部分失败），与 Cloudreve §5-P2 一致。

### P2：组织与检索（星标 → 标签 → 最近）

1. 星标（`cloudfile.colorMark` 对标，单值位）。
2. 标签（多对多 + 用户可排rank）。**必须在 1-2 完成之后**。
3. 最近访问（本地表 + 端点溯源字段）。

## 7. 明确不照搬的内容

- **不引入团队空间**。`specificcloudteam.xml` 的团队计数、入口红点、组织树，对应的是 WPS 的企业客户形态；EasyShare 当前是单组织私有部署，`es_space` 的 shared 空间已够用。
- **不拆分三种上传通路**。见§4.4，当前收敛是对的。
- **不抄「最近访问」的端点溯源全字段**（`lastTerminalName` 存完整 UA 串这类）。阶段 6 真需要时再按我们的设备模型设计，不照搬它的字段。
- **不做设备管理面板**。与阶段 6 配对设计合并考虑。
- **不把 WPS 的目录同步根当现有功能补齐**。阶段 5 CfAPI 已经规划了更完整的同步根方案，不走它的固定文件夹同步。

## 8. 与既有对标文档的关系

| 文档 | 视角 | 结论重叠度 |
| --- | --- | --- |
| [`cloudreve-benchmark.md`](cloudreve-benchmark.md)（2026-07-25） | 服务端架构（多租户 Web 网盘） | 高：回收站/版本/标签/批量四项结论一致；本文补充桌面端产品形态与证据 |
| 本文（2026-10-13） | 桌面客户端产品形态 | — |

两份文档在 **回收站/版本（P1）、标签（P2）、批量操作（P2）** 三处结论一致，且证据来源完全独立（Cloudreve 源码 vs WPS 本地表结构）。这种独立互证比单一来源的结论可信度更高。

Cloudreve §5 已排定的 P0（`fileId` + 目录层 + Upload Session + 统一任务）**已于 2026-09-06 三片合龙落地**，本文的P1 建议正是接在那之后。

## 9. 后续验收门槛

任何本篇建议的新能力都应满足：

1. 挂 `fileId`，不新增路径绑定（§4.1已验证的方向）。
2. 删除可还原、还原到原位置（§4.2）。
3. 破坏性操作前有确认，且失败原因对人可读（§4.6）。
4. 批量操作返回逐项结果，允许部分失败（§5-P1-4）。
5. 不新增普通用户需要理解的技术设置。
6. 与阶段 5（CfAPI 同步根）共用同一文件模型，不产生第二套真相源。

## 10. 最终判断

WPS 云盘最值得EasyShare 借鉴的**不是界面长什么样**（那是 Qt 渲染的原生窗口，与我们的 Wails Web 技术栈无可比性），而是**它把哪些东西当成一等实体**：

- 文件身份与路径分离（§4.1，我们已做对）
- 删除是可还原的状态，不是消失（§4.2，最大缺口）
- 标签与标记是独立建模的组织维度（§4.3）
- 版本是文件身份的一部分（§4.5）
- 失败必须留下可诊断的三件套（§4.6）

**下一步唯一关键路径：`es_file` 加回收站与版本列。** 这一步同时解锁回收站、版本、重命名三件事，且是 2026-07 Cloudreve 对标就写下的 P1 第一项——跨越三个月、基于两份独立证据的同一个结论。