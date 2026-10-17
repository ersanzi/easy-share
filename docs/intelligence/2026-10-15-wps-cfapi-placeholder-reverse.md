# WPS 云盘「桌面形态」逆向取证 — CfApi 占位符 / 同步根 / 本地数据模型

日期：2026-10-15 　目标：WPS Office 12.1.0.29161 云盘同步引擎（ksyncengine）
产物根：`D:\Program\office\WPS Office\12.1.0.29161\office6\`

| 产物 | 大小 | SHA-256 |
| --- | --- | --- |
| `addons\ksyncengine\ksyncengine.dll` | 1 246 200 | `8747df16b1531244d9c7c41ebbb88b3a7cf9e9ba582738a785b52ecda0d282b6` |
| `qingnse64.dll` | 16 716 280 | `f8d4c6576403b56947b4e187c180d0381008869ebe0704a1a1765410d0865df4` |
| `qingshellext64.dll` | 868 344 | `49a601b57f9fef694e7a4ae296c2719b7f4497c1782188e507adde8f21bb4a55` |
| `wpscloudsvr.exe` | 977 400 | `6549656ae9d488034e1d1827b916e4d89601d3dff7c888078afaaeda7874f6ca` |

> ※ 上一轮此处曾标注「以 REA 回执 digest 为准、人工誊写可能有误」。**2026-10-16 已在本机复核两处来源**（`output/wpsbin/` 副本与 `D:\Program\office\WPS Office\12.1.0.29161\office6\` 原件），**SHA-256 完全一致**，即上表该行现为实测值；原注中另一个疑似变体（`…9ebc07…`）为誊写错误，已弃用。

> **边界声明**：本文全部为**静态**取证（REA/Ghidra 反编译 + 二进制字面量 + 只读注册表 + 只读本机 SQLite 副本）。
> **无任何运行时观察**：没有启动/结束 WPS 进程、没有改注册表、没有写同步根内任何文件、没有抓包。日志串只证明「分支存在」，不证明「运行时真走到」。
>
> **2026-10-16 补证轮**：本文 §11 的四项欠账中**三项已闭合、一项部分推进**——新增 §2.1b（`cldapi.dll` 懒加载机制 + **回调表全量**）、
> §3.2b（回调表实测，取代 §3.2 的推断）、§3.3b（`CF_PLACEHOLDER_CREATE_INFO` 真实填充，**更正两处推断错误**）、§5.1b（`activeState` 位结构，部分）。
> 级别为 **【G】**：走**自建持久化 Ghidra 工程 + 自写脚本**（[`scripts/reverse/`](../../scripts/reverse/README.md)），可复现但**无 REA Evidence ID**；
> 上一轮卡住的 `list_names` bridge 崩溃被绕过（脚本直接读内存与反编译，不经 bridge 的 JSON 层）。方法论见 [`toolchain-rea.md`](../toolchain-rea.md) §8。

---

## 0. 一句话结论

WPS 云盘桌面形态不是「映射盘」，而是一条完整的 **Windows Cloud Files（CfApi）同步根**：`ksyncengine.dll` 里有一套以 `KCfApiSyncFolderEngine` 为根的类族，负责把同步根注册进 `SyncRootManager`、连上 CfApi、**建占位符（`CfCreatePlaceholders`）而不下载内容**、通过**回调把"用户点开文件"变成一次按需下载**（`cfApiFetchDataCallback` → Hydration 任务）、用**自定义状态（custom state）+ pin state + in-sync state** 三套状态位把"云端文件 / 本地副本 / 待上传 / 待修复 / 已释放"表达在 NTFS 上、并用一张 SQLite 的 `fs_metadata_table` 把 `cloudFileId`（云端身份）与 `fsFileId`（本地 NTFS 身份）双向绑定。我们要的「只读占位符原型」所需的最小闭包，这份文档第 9 节给全了。

---

## 0b. 中文口径（不看证据也想得通）

WPS 云盘的文件夹里，文件**看得见、点得开、但不占地方**。它靠的不是虚拟盘符，是 Windows 自带的「云文件」能力（Cloud Files / CfApi，OneDrive 用的是同一套内核接口）。做法分五步：

**一，先登记一个"同步根"。** 程序告诉系统「这个目录归我管」，系统在注册表 `SyncRootManager` 下留一条记录（谁提供的、显示什么名字、用什么图标、缩略图由哪个 COM 组件出）。登记之后，资源管理器才知道这个目录里的文件不是普通文件。

**二，先只放空壳。** 云上有 100 个文件，本地就放 100 个 0 字节的空壳，壳上带着"我从哪来"的身份牌。空壳在资源管理器里外观和普通文件一样，大小也显示云端真实大小（系统会读壳里存的元数据），但磁盘占用是 0。

**三，点开才真下载。** 用户双击哪个文件，系统就回调程序：「这个人要读这段数据」。程序去服务端拉，边拉边回填，用户甚至感觉不到有"下载"这一步。下载完成后那个空壳就变成真文件，占用空间变大。

**四，右键两个开关。** 「始终保留在这台电脑上」= 把壳钉住（pin），系统就不会自动清理它；「释放空间」= 把本地那份内容删掉、退回空壳（这在 Windows 里叫 dehydrate，"脱水"）。

**五，怎么知道哪个壳对应云上哪个文件。** 壳里存着一个"身份牌"（placeholder identity），程序自己另有一张表把「云上的 ID」和「本地 NTFS 的文件 ID」对上——因为本地文件被改名、被移动之后 NTFS 的文件 ID 不变，用路径去认会断，用 ID 认才稳。

还有几个顺手解决的现实问题，值得一并抄：

- **"正在读取"要限速。** 本地杀毒软件、搜索索引、360 这些程序会无差别扫盘，一扫就触发一堆下载。所以程序有一个白名单/黑名单，对特定进程的读取做节流。
- **上传中的文件不能被自己再下载回来。** 所以壳上还带一个"自定义状态"，标着"这个文件正在上传/这个文件在线文档不能下载/这个上传因为容量满了失败"。
- **本地和云端状态对不上时要能自愈。** 程序有专门的"状态修复"和"释放重试"任务，比对外壳上的状态和数据库里的记录，不一致就修回去。

---

## 1. 证据等级与可复现方法

标签沿用钉盘那份文档的口径：**【R】**=REA Evidence；**【S】**=二进制字面量（编译进产物的字符串/RTTI 名，属硬证据，但只能证明分支存在）；**【D】**=本机数据/注册表实测；**【推】**=由前述做的推断；**【?】**=未知。

### 1.1 REA Evidence（本轮实际产出）

| Evidence ID | 操作 | 对象 | 关键结论 |
| --- | --- | --- | --- |
| `ev_58891b0660a758bc0a1a88e22899076b747890325e7767f2cc1a701bb6907574` | `analyze`（ghidra 12.1.4） | `ksyncengine.dll` | 7 段、**7 987 个函数**、**2 962 条串**；段：`Headers 0x10000000`/`.text 0x10001000-0x100d3000 (860 160 B)`/`.rdata 0x100d3000-0x1010e200 (242 176 B)`/`.data 0x1010f000`/`.rsrc 0x1011d000`/`.reloc 0x1011e000`/`tdb` |
| `ev_4c50a5de5128bf9eb1678a92448e185c2b1757673c33a9eaa261d1c37d143dc9` | `analyze` | `qingshellext64.dll` | 架构 `x86_64`（PE32+），成功 |
| `ev_49e2f7355a4e5d37f0af7b24629d9b317ca3f1f9f73abe34f22babe48ab2689c` | `inspect-pe-resources`（无需引擎） | `qingshellext64.dll` | 覆盖完整（`examined_entries` 80，36 条资源）：**15 个 PNG 具名资源（name id 103/104/105/306/307/308/312/313/314/1017/1018/1019/1027/1028/1029，语言 2052）**、2 个 `REGISTRY`、1 个 `TYPELIB`、**3 个 RT_GROUP_ICON**（各 2 尺寸：32×32 + 24×24，32 bit） |
| `ev_0ed4f97c1c8c7eff2a77d9601d123ed0e62e9c76b1a56f8d6945ad02fead558f` | `inspect-pe-resources` | `wpscloudsvr.exe` | PE32（x86），24 entries / 10 资源：**1 个 RT_GROUP_ICON（48/32/16 三尺寸，32 bit）**、1 个 `IDI_APPLICATION` |
| `ev_677f9d5cbf1098e…`→见下 | `search --kind procedures` | `ksyncengine.dll` | 见 §1.2 失败记录 |
| `ev_12fe842595c5176d6d728a8a5da6c21313c5cb7e180c54bd815a93be4329eec1` | `search --kind strings` | `ksyncengine.dll` | **`normalized_result` 为空（0 条）**，`limitations` 自报「Only Ghidra-defined string Data is observed; packed or undefined string bytes may be absent…an empty search does not establish that text is absent」 |

**REA 自报限制照抄**（`ev_58891b06…` / `ev_12fe8425…`）：
- 会话在 Ghidra 默认自动分析完成后才提供操作；分析未完成不暴露部分结果。
- 导入的 Program 与临时工程是**临时**的，关闭即删（`import_mode: ephemeral-source-immutable`）；注释操作只改会话数据库元数据，**从不写原始可执行字节**。
- 默认地址空间使用小写 `0x` 前缀十六进制。
- **`search_strings` 只观测 Ghidra 已定义的 string Data**；打包或未定义的字符串字节可能缺席，**空结果不等于产物中不存在该文本**。
- 正则模式用 Java Pattern 语义。
- Windows P0 只接受受认可的 native x86 / x86-64 PE；managed / hostile / sensitive / mutable-path 目标不支持。

### 1.2 本轮踩到、且对后续取证有普遍意义的坑

这四条是本轮真实撞上的，**不是从 `toolchain-rea.md` 抄的**，建议回填进那份备忘：

**(a) `qingshellext64.dll` 的 `analyze` 首次 `execution_failure`：`Ghidra bridge response is not valid JSON`。** 同样的命令重跑一次即成功。属**可重试的瞬态故障**，不是配置问题（`details.diagnostics` 里 Ghidra 自身 `Analysis succeeded`，失败发生在 bridge 序列化层）。**对策：`analyze` 报 `execution_failure` 时先原样重跑一次。**

**(b) `ksyncengine.dll` 的 `xrefs --address <导入名>` 直接 `invalid_value: Unknown analyzed symbol name`，且 `list_names` 在 bridge 层崩（同样是 `Ghidra bridge response is not valid JSON`）。** 这不是偶发——见 §2，本 DLL 的**全部**外部导入在 Ghidra 里都**只以 `FUN_` 地址注册**（`search --kind procedures` 返回的 22 个「Cf」命中全是 `FUN_xxxxxxxx` / `Unwind@` / `Catch_All@`，`CfCreatePlaceholders` 等导入名**一个都没有**）。原因：这些 API 是 `LoadLibraryW` + `GetProcAddress` 动态解析（字面量面坐实，见 §2.3），PE 导入表里根本没有它们。**对策：目标 DLL 的导入名可能完全不是 Ghidra 符号——先 `rea analyze` 看 `procedure_count` 与段表，再决定走符号还是走字节。**

**(c) 非 ASCII 路径会让 `rea` 彻底不可用。** 一轮 `rea search` 把 `--pattern` 里的 `|` 当成了管道，`cfapi` 被 cmd 当外部命令执行并失败；换成批处理文件后，`rea` 返回 `target_unavailable … access 'E:\myProgrom\????\easyShare\...'`——**`rea` 启动的 Ghidra 子进程把中文路径的 `有趣项目` 变成了 `????`**。**对策：所有 REA 目标一律先复制到纯 ASCII 路径（如 `C:\Users\lishi\AppData\Local\Temp\wpsre\`），复制不改变 SHA-256，`subject.digest` 仍可比对。** 同时：**`rea` 的参数里出现 `|` 时不要用 `pwsh` 的 `& rea … --pattern 'a|b'` 形式**，写 `.bat` 再 `cmd /c`，或全用 `pwsh` 数组参数传递。

**(d) 复现命令清单**（本轮实际执行，逐条可重跑；所有 `$env:` 每次新进程都要重设）：

```powershell
$env:GHIDRA_INSTALL_DIR = 'D:\Develop\ghidra_12.1.4_PUBLIC'; $env:JAVA_HOME='D:\Develop\java21'
$bin = 'C:\Users\lishi\AppData\Local\Temp\wpsre'      # 纯 ASCII 工作副本目录

# 1) 概览 + 段表 + 函数数（对应 ev_58891b06…）
rea analyze "$bin\ksyncengine.dll" --provider ghidra --format json

# 2) 资源面（无需引擎，秒出；对应 ev_49e2f735… / ev_0ed4f97c…）
rea inspect-pe-resources "$bin\qingshellext64.dll" --format json
rea inspect-pe-resources "$bin\wpscloudsvr.exe"    --format json

# 3) 程序名检索（证明 CfApi 导入名未进 Ghidra 符号表；对应 ev_e677f9d5…）
rea search "$bin\ksyncengine.dll" --pattern='Cf[A-Za-z]+' --kind procedures --mode regex --provider ghidra --format json

# 4) 字符串检索（返回空，见限制照抄）
rea search "$bin\ksyncengine.dll" --pattern='Cf*' --kind strings --mode literal --provider ghidra --format json
```

**字节层复现**（无 REA、纯 Python，见 §2 的结论全部由此定位）：

```powershell
# 解析 PE 导入表 + 延迟导入表，确认 CfApi 名不在其中
python output\peimp.py
# 扫 CfApi 名串的落点与段归属
python output\cfstrref.py
# 枚举 C++ RTTI 限定名（本文 §2.2 全量类族表来源）
python output\rtti.py > output\ksync-symbols.txt
```

**本机数据复现**：

```powershell
# cache.db 必须只读副本；本机实测无 -wal / -shm，但仍按 WAL 流程处理
Copy-Item 'C:\Users\lishi\WPS Cloud Files\.<uid>\cache.db' output\wpsdata\cache.db
python output\wpsq.py ; python output\wpsq2.py
python output\placeh.py        # 占位符 reparse tag + 属性位直方图
```

---

## 2. CfApi 调用方的真实结构（P0 §1–§4 的答案）

### 2.1 先破一个前提：`ksyncengine.dll` 里**没有** CfApi 的 PE 导入

`peimp.py` 解析 `ksyncengine.dll` 的**导入表（11 个 DLL、503 个符号）与延迟导入表（SHELL32 / ole32 / ADVAPI32 / SHLWAPI，共 19 个）**，**CfApi 相关符号命中数 = 0**。`qingnse64.dll`、`qingshellext64.dll` 同样为 0。

**【推断，但有字面量佐证】** CfApi 是**运行时动态解析**的，机制如下（全部为 `ksyncengine.dll` 实测字面量，**【S】**）：

| RVA | 字面量 | 作用 |
| --- | --- | --- |
| `0x000e757c` | `LoadLibraryW %1 error: %2` | 加载 DLL |
| `0x000e7598` | `GetProcAddress funcName %1 errorCode %2` | 按名取函数 |
| `0x000e756c` | `winrtwrapper` | 该封装模块名 |
| `0x000e72dc`–`0x000e7418` | **`CfRegisterSyncRoot` `CfUnregisterSyncRoot` `CfConnectSyncRoot` `CfDisconnectSyncRoot` `CfExecute` `CfCreatePlaceholders` `CfOpenFileWithOplock` `CfCloseHandle` `CfConvertToPlaceholder` `CfUpdatePlaceholder` `CfDehydratePlaceholder` `CfSetPinState` `CfSetInSyncState` `CfGetPlaceholderInfo` `CfGetSyncRootInfoByPath` `CfReportProviderProgress`** | **16 个 CfApi 函数名，以 NULL 分隔的连续名表**（`0x000e72dc`–`0x000e7434`，相邻间隔恰好等于各名字节长度+1）。这正是一张 `GetProcAddress` 名字表。 |
| `0x000e7400` 起同段 | `CfGetSyncRootInfoByPath`（另在 `0x000e7fe8` 附近还有一份） | 见下 |

**关键修正**：任务书给的「导入 8 个 CfApi 函数」应当订正为——`ksyncengine.dll` **动态解析 16 个 CfApi 函数**（8 个是任务书列出的，另外 8 个是 `CfUnregisterSyncRoot` / `CfDisconnectSyncRoot` / `CfExecute` / `CfOpenFileWithOplock` / `CfCloseHandle` / `CfSetInSyncState` / `CfGetSyncRootInfoByPath` / `CfReportProviderProgress`）。这 8 个「新增」的函数里，`CfExecute` + `CfReportProviderProgress` 是**回调骨架的必需件**（见 §3.2），`CfOpenFileWithOplock` + `CfCloseHandle` 是**回调期拿文件句柄**的必需件——缺了它们就实现不出正确的 fetch 回调。

> `qingnse64.dll` 是**另一条独立路径**：它字面量面同时出现 **`\cldapi.dll`（UTF-16LE）+ `CfGetPlaceholderInfo` + `CfGetSyncRootInfoByPath`（三者 ASCII/UTF-16 混排紧邻于 RVA `0x007873b0` 一带）**，即它**按传统方式静态延迟加载 `cldapi.dll` 并取这两个查询函数**——只读、不写。这与「同步引擎独占写路径、UI 侧只读」的分工一致。

### 2.1b 【2026-10-16 闭合】动态解析机制与**回调表全量**（【G】，反编译到脚）

§2.1 当时的「推断」本轮**已升级为直接观测**：不再是从名表推「大概是 GetProcAddress」，而是**反编译到了加载与解析代码本身**，并**读出了静态的 `CF_CALLBACK_REGISTRATION` 数组**。

**方法**：建 `ksyncengine.dll` 的持久化 Ghidra 工程（1.2 MB，一次导入），用 `scripts/reverse/` 的脚本定向查询（每次 6–9 s）。**这条通路绕开了上一轮卡住的 `list_names` bridge 崩溃**——脚本直接读数据与指令，不经 Ghidra bridge 的 JSON 层。

#### (1) 动态加载：`FUN_10089000@0x10089000`（`cldapi.dll` 的懒加载器）

```c
HMODULE * FUN_10089000(void) {
  // 双检锁（TLS 保护的 init-once）
  if (... < DAT_1011b7b4) {
    ...
    FUN_100882c0(local_2c, puVar1, L"\\cldapi.dll");   // ← UTF-16LE 字面量
    DAT_1011b7b0 = LoadLibraryW((LPCWSTR)ppppWVar2);      // ← 加载
    _atexit(FUN_100d2930);                                 // ← 退出时 FreeLibrary
  }
  ...
}
```

> **这就是上一轮 ASCII 扫描没命中 `cldapi.dll` 的原因**：该串是 **UTF-16LE**（`L"\cldapi.dll"`），不是 ASCII。上一轮把 `cldapi.dll` 记作 `qingnse64.dll` 的结论，**对 `ksyncengine.dll` 同样成立，且机制相同**。

#### (2) 取函数：`FUN_100884d0@0x100884d0`（`CfConnectSyncRoot` 的 thunk）

```c
int FUN_100884d0(a1,a2,a3,a4,a5) {
  HMODULE *h = FUN_10089000();              // 确保 cldapi.dll 已加载
  if (... lazy guard ...) {
    DAT_1011b7c8 = *h == NULL ? NULL : GetProcAddress(*h, "CfConnectSyncRoot");
  }
  if (DAT_1011b7c8 != NULL) return (*DAT_1011b7c8)(a1,a2,a3,a4,a5);
  return 0x65a;                              // ← 取不到函数时返回固定错误码
}
```

**这解释了 §2.1 的谜题**：每个 CfApi 函数都有一个**独立 thunk**（`FARPROC` 存在 `.data` 的独立槽位），所以 PE 导入表里查不到——它们是 `GetProcAddress` 的产物。`FUN_10088a00@0x10088a00` 是 `CfRegisterSyncRoot` 的同构 thunk（`GetProcAddress(..., "CfRegisterSyncRoot")`）。

#### (3) ★ `CF_CALLBACK_REGISTRATION` 数组**全量**（上一轮 §11-1 的头号缺口）

`CfConnectSyncRoot` 的唯一调用点是 `FUN_10057610@0x10057610`：

```c
// KSyncEngineCfApiSyncFolderVfs::_connectSyncRoot  (源文件行号 0x270 / 0x274 / 0x285 见日志字面量)
cVar4 = FUN_10087350();                          // isSystemEnvCompatibleWpsDrive()
if (cVar4 == '\0') { 报错 "isSystemEnvCompatibleWpsDrive return false, cannt connectSyncRoot" }
else {
  pvVar6 = operator_new(8);
  *(void **)((int)this + 0x3c) = pvVar6;         // 连接键（CfConnectSyncRoot 的 out 参数）
  puVar7 = QString::utf16((QString *)((int)this + 0x2c));   // 同步根路径
  DVar8 = FUN_100884d0(puVar7, &DAT_1011299c, this /*回调上下文*/, 6 /*flags*/, pvVar6);
  if (DVar8 == 0) *(undefined1 *)((int)this + 0x40) = 1;
  else 报错 "CfApiVfs: connectSyncRoot error=%1" (行 0x285)
}
```

**回调表在 `.rdata` 的 `0x1011299c`，逐项读出如下**（`ksyncengine.dll` 是 **32 位 PE**，故 `CF_CALLBACK_REGISTRATION = {int32 Type; void* Callback}` = **8 字节/项**）：

| 偏移 | `Type` | 名 | 回调地址 | 实测符号 |
| --- | --- | --- | --- | --- |
| +0x00 | 0 | `CF_CALLBACK_TYPE_FETCH_DATA` | `0x10056fc0` | `cfApiFetchDataCallback` |
| +0x08 | 2 | `CF_CALLBACK_TYPE_CANCEL_FETCH_DATA` | `0x10018080` | （空实现，函数体 76 B） |
| +0x10 | 9 | `CF_CALLBACK_TYPE_NOTIFY_DEHYDRATE` | `0x100572b0` | `KSyncEngine…` |
| +0x18 | 11 | `CF_CALLBACK_TYPE_NOTIFY_DELETE_COMPLETION` | `0x10057150` | 调 `FUN_10018f50` + `FUN_100887c0` |
| +0x20 | 6 | `CF_CALLBACK_TYPE_NOTIFY_DELETE` | `0x10057400` | 调 `FUN_10018f50` |
| +0x28 | `0xFFFFFFFF` | `CF_CALLBACK_TYPE_NONE` | `NULL` | **终止项** |

**连接标志 = 6**：`0x2 CF_CONNECT_FLAG_REQUIRE_PROCESS_INFO | 0x4 CF_CONNECT_FLAG_REQUIRE_FULL_FILE_PATH`。**两个标志都置起**——前者用于 §3.5 的读取节流（要知道是谁在读），后者用于确认同步根内路径。

**五个回调的引用计数实测**：`FuncCallers` 报告 FETCH_DATA / NOTIFY_DEHYDRATE / NOTIFY_DELETE_COMPLETION / NOTIFY_DELETE **各自的 `getReferencesTo` 引用数均为 0**，CANCEL_FETCH_DATA 为 3（被同族代码直接调用）。**只有表项引用、没有代码直接调用**——这从反面证实了「这张表就是 CfApi 的唯一入口」，与 `CfConnectSyncRoot` 的唯一调用点互证。

#### (3b) 独立复核：全库扫描确认「只有这一张表」

为排除「还有别的回调表没看到」，另写 `ScanCb32.java` **全 `.rdata`/`.data` 盲扫** 8 字节/项的
`{int32 Type; void* Callback}` 形态（要求 `Type ∈ [0,13]`、`Callback` 指向可执行段、且以
`{0xFFFFFFFF, NULL}` 收尾）。

**结果**：命中 14 处，但**13 处是假阳性**——它们是 MSVC 的 **C++ 异常 unwind 表**，
`Callback` 全部落在 `Unwind@…` 伪符号上（结构同形：小整数 + 指向 `.text` 的偏移）。
**唯一一处 Callback 落在真实函数体上的，就是 `0x1011299c`**，5 项与表项逐一吻合。

**两种独立方法互证**（读调用点传的地址 vs 全库盲扫），且**全库只有这一张** ——
`ksyncengine.dll` 里 `CfConnectSyncRoot` 的注册表**就是这 5 项，没有遗漏**。

> 方法学提示：扫这类「小整数 + 函数指针」的数组，**必须用 `Unwind@` 这类伪符号做负向过滤**，
> 否则异常表会淹没真实结果（本例 14 中 13 假）。

#### (4) `cfApiFetchDataCallback` 的真实行为（FETCH_DATA 回调）

`FUN_10056fc0@0x10056fc0`，函数体里唯一的字符串揭示了它的语义：

```c
void FUN_10056fc0(int param_1, QString *param_2) {
  iVar1 = *(int *)(param_1 + 0x10);                    // 上下文
  if (iVar1 != 0) {
    日志("%1 will read not downloaded file %2%3", ...);   // ← 明确语义："将要读未下载的文件"
    ...
    param_2 = FUN_1005b970(operator_new(0x38), param_1, param_2);   // 造 JobContext
    QMetaObject::invokeMethod(iVar1, "slot_fetchData", 2 /*Qt::QueuedConnection*/,
                              0, 0, &param_2, "KSyncEngineHydrationJobContext*", ...);
  }
}
```

**这条链完整闭合了「点开才下载」**：CfApi 回调 → 造 `KSyncEngineHydrationJobContext` → **Qt 队列连接**投递到 `slot_fetchData` → 业务线程执行 hydration。回调本身**不做 I/O**，只做投递——这是 Qt 事件循环与 CfApi 回调线程（不可阻塞）之间的正确接法。

> 与上一轮结论的差异：上一轮从字面量推「`cfApiFetchDataCallback` → Hydration 任务」（§0 一句话结论），**本轮把中间那一跳（Qt `invokeMethod` + JobContext + 槽名）坐实了**。

#### (5) 这条记录的方法学价值

上一轮 §11-1 把「回调表完整枚举」列为**头号缺口**，归因于「`Ghidra bridge response is not valid JSON` + 回调是未命名 lambda」。**实际两处归因都不准**：

1. **回调不是 lambda**——五个回调都有**独立函数体**，`FETCH_DATA` 那个还带 `__FILE__` 风格的名字串。查不到是因为**没有任何代码直接调用它们**（只被表引用），按「找调用者」的思路会一无所获。
2. **bridge 崩溃可以绕开**——`scripts/reverse/` 的脚本走 Ghidra API 直接读内存与反编译，**不经 bridge 的 JSON 序列化**，1.2 MB 目标 6–9 秒出结果。

**可复用判据**：找 CfApi/FUSE 这类**回调入口**，不要从「谁调用它」入手（答案是没有），要从「谁注册它」入手——反编译 `CfConnectSyncRoot` / `CfRegisterSyncRoot` 的调用点，直接读它传的结构体地址。

### 2.2 C++ RTTI 类族：CfApi 路线的完整骨架

`ksyncengine.dll` 的 `.rdata` 保留了完整的 MSVC RTTI 限定名。`rtti.py` 提取到 **562 个唯一限定名**，其中与 CfApi 直接相关的构成一条清晰的继承链：

```
KSyncEngineManager
 └─ 每同步根一个 KSyncEngine（EngineType 工厂产出）
     ├─ KSyncFolderSyncEngine            （虚拟同步文件夹路线）
     └─ KCfApiSyncFolderEngine           （CfApi 路线）★
         └─ createVfs() → KSyncEngineCfApiSyncFolderVfs   ★ VFS 全部写操作落点
```

**`KSyncEngineCfApiSyncFolderVfs` 的成员函数（这是实现的骨架）【S】**：

```
createPlaceholder      moveCacheToVfs        freeUpPlaceholder     updatePlaceholder
setCustomFileState     propertyChange        setIgnoreState        notifyDiffProcessed
initImpl               unInitImp             bindingImpl           unbundleImpl
_initSyncRootReg       _uninitSyncRootReg    _connectSyncRoot      _disconnectSyncRoot
startImpl              stopImpl
+ 成员：slot_fetchData                     （回调槽，见 §3.2）
+ 匿名命名空间：cfApiFetchDataCallback     （fetch 回调实体，见 §3.2）
```

**`syncengine::cfapiutil` 自由函数（CfApi 的 API 封装层，函数名即语义）【S】**：

| 函数 | 字面量证据（同名 RVA 紧随其后） | 映射到的 CfApi |
| --- | --- | --- |
| `cfapiutil::createPlaceholder` | `create Placeholder error=%1` | `CfCreatePlaceholders` |
| `cfapiutil::convertToPlaceholder` | `CreateFile %1 errCode=%2` / `CfConvertToPlaceholder %1 errmsg=%2` | `CfConvertToPlaceholder` |
| `cfapiutil::updatePlaceholderInfo` | `findPlaceholderInfo return NULL. path:%1` / `updatePlaceholderInfo failed. fileId: %1 name: %2 hr: %3` / `restore pinstate failed. path:%1` | `CfGetPlaceholderInfo` + `CfUpdatePlaceholder` |
| `cfapiutil::dehydratePlaceholder` | `Get fileSize of %1 errCode %2` / `CfDehydratePlaceholder failed. filePath: %1 ,error %2` | `CfDehydratePlaceholder` |
| `cfapiutil::setPinStateByPath` | `handleForPath is INVALID_HANDLE_VALUE. path:%1` / **`CfSetPinState failed filePath: %1 error is %2`** | `CfSetPinState` |
| `cfapiutil::setPinStateUnspecified` | （紧邻上面一条） | `CfSetPinState(… CF_PIN_STATE_UNSPECIFIED …)` |
| `cfapiutil::setExcludedFile` | `setPinState failed. path:%1` | `CfSetPinState(PINNED\|UNPINNED\|EXCLUDED?)` 见 §3.1 |
| `cfapiutil::convertExcludedFileToUnspecifiedFile` | — | 撤销排除 |
| `cfapiutil::setInSyncState` | `CfSetInSyncState failed. filePath: %1 isInSync: %2 hr: %3` | `CfSetInSyncState` |
| `cfapiutil::appendCustomStateToFile` | `file path is too long: %1` / `appendCustomStateToFile failed. filePath: %1 error %2` | `CfExecute(CF_OPERATION_TYPE_TRANSFER_PLACEHOLDERS)` 一族，见 §3.4 |
| `cfapiutil::clearCustomStateFromFile` | `clearCustomStateFromFile failed. filePath: %1 error %2` | 同上，清零 |
| `syncengine::cffilestate::_setFileState` | **`CfApiVfs: setExcludedFile error path = %1 state = %2`** / `CfApiVfs: _getFileStateInfo error path = %1 state = %2` / `CfApiVfs: appendCustomStateToFile error path = %1 state = %2` | 三态统一入口 |

**注意 `setExcludedFile` 与 `setPinStateByPath` 是两条独立函数**，前者内部失败日志写的是 `setPinState failed`——说明 **「EXCLUDED」是用 pin state 实现的一种文件级状态**（CfApi 的 `CF_PIN_STATE_EXCLUDED`），不是另开一套。

### 2.3 CfApi 名串的两份副本

`cfstrref.py` 定位到名串在 `.rdata` 里有**两份**：

| 名串 | 副本 A（RVA） | 副本 B（RVA） |
| --- | --- | --- |
| `CfRegisterSyncRoot` | `0x000e7edc` | — |
| `CfConnectSyncRoot` | `0x000e7f08`–`0x000e7f2c` | — |
| `CfCreatePlaceholders` | `0x000e7f40` | — |
| `CfConvertToPlaceholder` | `0x000e7f80` | **`0x000e0e04`** |
| `CfUpdatePlaceholder` | `0x000e7f98` | — |
| `CfDehydratePlaceholder` | `0x000e7fac` | **`0x000e0ea4`** |
| `CfSetPinState` | `0x000e7fc4` | **`0x000e0c6c`** |
| `CfGetPlaceholderInfo` | `0x000e7fe8` | — |

**副本 B（`0x000e0c6c` / `0x000e0e04` / `0x000e0ea4`）正好落在三处错误日志格式串的中间**——即它们是 `CfSetPinState failed filePath: %1 error is %2` 这一类消息里**被内联进格式串的实际函数名**，证明这三条日志确实在 `CfSetPinState` / `CfConvertToPlaceholder` / `CfDehydratePlaceholder` 的**失败分支**上。副本 A（`0x000e72dc`–`0x000e7434`）是给 `GetProcAddress` 用的连续名表。

**没有一个名串被 4 字节 RVA 引用命中**（`cfstrref.py` 对全部 11 处名串做全文件 `struct` 扫描，`section_refs = 0`）——这与「动态解析 + 名字表」的判断自洽（表是**遍历读取**的，不是逐项取址）。

### 2.4 同步根注册：写哪些注册表键【S】+【D】

`KSyncEngineCfApiSyncFolderVfs::_initSyncRootReg` / `_uninitSyncRootReg` 用到的键名/值名（全部实测字面量）：

```
HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\SyncRootManager\
UserSyncRoots                       ← 子键名
Provider / Flags / IconResource / DisplayNameResource / ThumbnailProvider
hideDriveCfMenuStatus  hideDriveCfMenuEnabled
Kingsoft / WPSCloud / usercenter / wpsdrive      ← 设置树的路径分量
enableDriveThumbnail
```

**本机实测（只读）** `HKLM\…\Explorer\SyncRootManager` 下**只有一条 WPS 记录**，与上面每一个值名一一对上【D】：

| 值名 | 实测值 |
| --- | --- |
| 键名 | `WPSCloudProvider!S-1-5-21-…-1001!<uid>!<企业名>` |
| `Provider` | `WPSCloudProvider` |
| `Flags` | `2`（对照：同机 OneDrive 是 `1058`） |
| `IconResource` | `<office6>\qingnse.dll,58` |
| `DisplayNameResource` | 企业名称（明文） |
| `ThumbnailProvider` | `{BD0EE609-3E41-4DA5-993F-4CA96C0B08F4}` |
| 子键 | `UserSyncRoots` |

`Flags = 2` 的含义**【推】**：`CF_SYNC_ROOT_REGISTER_…` 一族中，`0x1`=`CF_SYNC_ROOT_STANDARD` 之类的位被置 0，而任务书给的另一路（个人云盘）应为另一取值——**本机只登记了企业云盘一个根，个人云盘根未出现在 `SyncRootManager` 里**，这是本轮的一个实测差异，【?】其成因（可能靠 `CF_SYNC_ROOT_REGISTER_…` 之外的机制，或该根在另一账户上下文）。

---

## 3. 占位符状态机（P0 逐条回答）

### 3.1 `CfSetPinState` 的调用点与取值（P0-1）

**调用点（成员级，均有字面量锚定）【S】**：

| 调用者 | 触发条件 |
| --- | --- |
| `KSyncEngineCfApiSyncFolderVfs::freeUpPlaceholder` → `cfapiutil::dehydratePlaceholder` | 「释放空间」：先把内容脱掉，再恢复 pin 语义 |
| `KCfApiSyncFolderVfsFolderDiffMaker::_judgeFreeUp` | 差异扫描时判定「这个文件该释放」；配套日志 **`has free up file state`**、**`retry free up`**、**`file %1 has free up state, cloudFileId=%2`** |
| `KCfApiSyncFolderFreeUpHandler::start` / `_notifyAction` / `slot_taskFinished` | 释放任务的调度侧；日志 **`free up %1`** |
| `KSyncEngineUpTaskMaker::_handleFreeUp` 的 CfApi 版 `KCfApiSyncFolderUpTaskMaker::_handleFreeUp` | 上传任务生成器里把 free-up 也当一类任务 |
| `cfapiutil::setExcludedFile` / `convertExcludedFileToUnspecifiedFile` | 置/撤「排除」 |
| `cfapiutil::updatePlaceholderInfo` | 更新占位符后**恢复 pin state**（日志 `restore pinstate failed. path:%1`） |

**取值**：本轮**未在 ASCII 字面量里命中任何 `CF_PIN_STATE_*` 常量名或字面量**（`cfscan.py` 对 `CF_PIN_STATE` / `PINNED` / `UNPINNED` / `INHERIT` / `EXCLUDED` 的 ASCII 与 UTF-16LE 双通道扫描命中 **0**）。**原因明确**：这些是 SDK 头文件里的**枚举宏**，编译后变成**立即数入栈/入寄存器**，源码里的标识符不进二进制。

**【推，证据链完整】** 但从三个函数的**语义命名 + 失败日志**可以反推取值集合与用法，且与 CfApi 官方语义严格对齐：

| 封装函数 | `PinState` 实参 | 依据 |
| --- | --- | --- |
| `setPinStateByPath` | `CF_PIN_STATE_PINNED`（1） | 函数名 + 日志 `CfSetPinState failed filePath: %1 error is %2`；这是「始终保留在这台电脑上」 |
| `setPinStateUnspecified` | `CF_PIN_STATE_UNSPECIFIED`（0） | 函数名直译「未指定」= 交还给系统自动管理 |
| `setExcludedFile` | `CF_PIN_STATE_EXCLUDED`（3） | 函数名 + 上表「内部走 `setPinState failed` 日志」 |
| （对应 `convertExcludedFileToUnspecifiedFile`） | 回到 `UNSPECIFIED`（0） | 函数名 |
| `CF_PIN_STATE_UNPINNED`（2） | **本轮未直接观察到显式调用点** | `dehydratePlaceholder` 只做脱水；「取消固定」是否走显式 `UNPINNED` 还是 `UNSPECIFIED`，【?】 |

> **对实现者的价值**：`setPinStateByPath` vs `setPinStateUnspecified` vs `setExcludedFile` 三分，说明 WPS 把「用户显式钉住」和「系统默认」严格区分——**不要用 `UNPINNED` 去表达「用户没表态」**，那会让系统误以为是用户主动取消固定，从而影响 `CfDehydratePlaceholder` 的准入。

### 3.2 `CfConnectSyncRoot` 的回调注册表（P0-4）★ 实现骨架

**这是本轮最有价值的一条。** 回调实体有明确的名字与日志锚点：

```cpp
// ksycengine.dll, .rdata @ 0x000df7e8   【S】
`anonymous-namespace'::cfApiFetchDataCallback
// 紧随其后 @ 0x000df818
"%1 will read not downloaded file %2%3"
// @ 0x0dfb18
slot_fetchData
// @ 0x0e0678
`anonymous-namespace'::cfApiSendTransferInfo
// 紧随其后 @ 0x000e06a8
"CfExecute returned %1"
// @ 0x000e06c0
slot_fireFinished
```

**回调类型判定【S，高置信】**：回调表里**唯一被命名的回调是 `cfApiFetchDataCallback`**，且它旁边就是「**%1 will read not downloaded file**」（"有人要读一个还没下载的文件"）与 `CfExecute`。这三条字面量拼在一起，只有一种解释：

> **`CF_CALLBACK_TYPE_FETCH_DATA`**：系统在应用读取占位符数据时回调，回调里用 `CfExecute` 回报进度/数据（`CfReportProviderProgress` 另有一份名表项）。**这正是「点开才下载」的实现点。**

其余回调**未能逐条命名**【?】——原因与 §3.1 同：`CF_CALLBACK_REGISTRATION` 是**结构体数组初始化**，回调类型是**宏常量（`CF_CALLBACK_TYPE_FETCH_DATA = 0` 等）编译成的立即数**，回调函数体多是**未命名 lambda**（`.rdata` 里只留 `KSyncEngineCfApiSyncFolderVfs::bindingImpl::<lambda_f891b94944dc141bb9e533a32866e87a>::operator ()` 与 `unbundleImpl::<lambda_e4089addee0912c8b34016c58342bd7e>::operator ()` 两个**带哈希的 lambda 名**，以及 `……_fireFinished::<lambda_156087ff…>`、`_scheduleUpTask::<lambda_3132ae43…>`、`_scheduleDownTask::<lambda_c04d0b23…>`）。

**【推】回调表至少含下面这几项**（每一项都有独立的行为字面量支撑，但「是否真的在同一张 `CF_CALLBACK_REGISTRATION` 数组里」未被直接观测）：

| 回调类型 | 证据字面量 | 行为 |
| --- | --- | --- |
| `CF_CALLBACK_TYPE_FETCH_DATA` | `cfApiFetchDataCallback`、`%1 will read not downloaded file %2%3`、`slot_fetchData`、`CfExecute returned %1` | 拉数据（**已命名**） |
| 取消/中止 | （`CfExecute` 的 `CF_OPERATION_TYPE_CANCEL_FETCH_DATA` 路径，未命名） | 【?】 |
| `…NOTIFY_DEHYDRATE` | `freeUpPlaceholder`、**`retry free up`**、`file %1 has free up state, cloudFileId=%2`、`KCfApiSyncFolderFreeUpHandler` | 释放空间 |
| `…NOTIFY_DELETE` / `…NOTIFY_RENAME` / `…NOTIFY_MOVE` | `_handleCloudRenameTask` / `_handleCloudMoveTask` / `_handleCloudRemoveTask` | 占位符被本地改动 |
| `…NOTIFY_FILE_CLOSE_COMPLETION` | `watermark`/`in-sync` 相关（见 §3.4） | 关闭后回写 in-sync |
| `…NOTIFY_TRANSFER_DATA` 的**完成**侧 | **`cfApiSendTransferInfo`**、`slot_fireFinished` | 传输完成通知（**已命名**） |

> **对实现者的价值**：只读原型**只需 `CF_CALLBACK_TYPE_FETCH_DATA` 一个回调**。`cfApiSendTransferInfo` 的命名存在说明 WPS 走的是「**回调里 `CfExecute` 主动回报**」而不是「由系统托管」。**最小实现 = `CfConnectSyncRoot` + 只注册 FETCH_DATA + 回调里 `CfOpenFileWithOplock` 拿句柄 → 拉数据 → `CfExecute(TRANSFER_DATA)`。**

### 3.2b 【2026-10-16 闭合】回调表实测（取代 §3.2 的推断）

§3.2 当时按「有独立行为字面量」推断出**至少**含哪些项，**但没能确认它们真在同一张数组里**（明写「未被直接观测」）。本轮**从 `CfConnectSyncRoot` 的连接点直接读出了数组内容**，见 [§2.1b (3)](…#21b-2026-10-16-闭合动态解析机制与回调表全量g反编译到脚)。

**实测表（`.rdata @ 0x1011299c`，32 位 PE 故 8 字节/项）**：

| `Type` | 名 | 回调 | 相对 §3.2 推断 |
| --- | --- | --- | --- |
| 0 | `FETCH_DATA` | `0x10056fc0` | ✅ 推断正确（且确认了它就是 `cfApiFetchDataCallback`） |
| 2 | `CANCEL_FETCH_DATA` | `0x10018080` | ✅ 推断有（此处坐实：**空实现**，76 B） |
| 9 | `NOTIFY_DEHYDRATE` | `0x100572b0` | ✅ 推断正确 |
| 11 | `NOTIFY_DELETE_COMPLETION` | `0x10057150` | ⚠️ 推断写的是 `NOTIFY_FILE_CLOSE_COMPLETION`——**实测是 DELETE_COMPLETION** |
| 6 | `NOTIFY_DELETE` | `0x10057400` | ✅ 推断有 |
| `0xFFFFFFFF` | `NONE` | `NULL` | ✅ 终止项 |

**三点对 §3.2 的实质修正**：

1. **没有 `NOTIFY_RENAME` / `NOTIFY_MOVE`。** §3.2 推断表里列了它们（依据是 `_handleCloudRenameTask` 字面量存在）——但**它们不在注册表里**。说明改名/移动走的是**别的路径**（可能是 `CfConnectSyncRoot` 之后的委托，或 `NOTIFY_DELETE+CREATE` 组合，或 `qingnse64.dll` 侧），**待查**。
2. **`NOTIFY_FILE_CLOSE_COMPLETION` 也不在。** 实测第 4 项是 `NOTIFY_DELETE_COMPLETION`（值 11）。§3.4 的「in-sync 在文件关闭后回写」这条推断**失去表项支撑**，需另找落点。
3. **表很小——只有 5 项 + 终止项。** §3.2 推断的「表更大，含 6+ 类」偏高。**对只读原型的意义不变且更强**：最小实现确实只需 `FETCH_DATA`，WPS 自己在一个完整的企业云盘里也只多注册了 4 项。

**另补：连接标志 = 6**（`REQUIRE_PROCESS_INFO | REQUIRE_FULL_FILE_PATH`）。§3.2 未提标志。`REQUIRE_PROCESS_INFO` 正是 §3.5 读取节流的**前置条件**——不置这个标志就拿不到「是哪个进程在读」，节流无从谈起。**实现顺序上，节流标志要在 `CfConnectSyncRoot` 时就定下来。**

### 3.3 `CfCreatePlaceholders` 的 `CF_PLACEHOLDER_CREATE_INFO` 填充（P0-2）★ 核心

**填充逻辑【S，由函数名与数据模型联合确定】**：

| 结构体字段 | WPS 放什么 | 证据 |
| --- | --- | --- |
| `RelativeFileName` | 相对同步根的**本地名** | `fs_metadata_table` 同时存 `fileName` 与 `localName`（**134 行中 4 行两者不同**【D】）——即「云端名」与「本地实际名」是**两个字段**，`RelativeFileName` 用哪一个由 `KSyncEngineCfApiSyncFolderVfs::createPlaceholder` 决定；配套日志 `create placeholder %1` / `on disk file [%1] exsit` |
| `FileSize` | **云端真实大小**（非 0） | `fs_metadata_table.fileSize` 实测 min/max = 0 / 26 281 641【D】；这也解释了资源管理器为何能显示正确大小而磁盘占用为 0 |
| `LastWriteTime` / `CreationTime` | 来自 `fs_metadata_table.modifiedTime` / `createdTime` | 同表有此两列，实测 `createdTime` max = `1760404627`【D】 |
| `FileAttributes` | 由 `cffilestate::_setFileState` 与 `setCustomFileState` 派生 | 见 §4 的属性位实测 |
| **`FileIdentity`** | **`fs_metadata_table.id`：一个 32 字符的十六进制本地代理 ID** | **实测 134/134 行的 `id` 长度恒为 32**【D】；`id` 是主键；`parentId` 与 `id` 构成树（132/134 行的 `parentId` 能解析到某个 `id`，**0 个孤儿**【D】） |
| `FileIdentityLength` | 32 字节（或该 32 字符串的字节长度） | 同上 |
| `FsMetadata` | **未观察到被使用** | `fs_metadata_table.extraInfo` **134 行全为空串**【D】；`CfConvertToPlaceholder` 的日志只提 `CreateFile %1 errCode=%2` + `CfConvertToPlaceholder %1 errmsg=%2`，**没有 FsMetadata 相关的失败分支**【S】 |

**`fsFileId` vs `cloudFileId` 的确切语义（任务书点名的问题）——实测回答【D】：**

```
fsFileId   BIGINT   134/134 行全部填了值；min = 1 407 374 883 796 822
                     max = 783 626 335 162 470 458
                     >> 56 的取值只有 1 个（全部相同）
                     & 0xFFFFFFFFFFF（低 44 位）的 distinct = 134（全部互异）
cloudFileId VARCHAR   134/134 行全部非空；长度分布 = {11:60, 12:61, 10:3, 8:1, 2:6, 18:1, 20:1, 31:1}
volSerial  VARCHAR   134/134 行全为空串 ''
```

**结论：**
- **`fsFileId` = NTFS 文件 ID（64 位 File Reference Number）**。判据一：高位 8 位（`>>56`）**全库同值**——那是 NTFS MFT 记录的**序列号高位**，同卷同日创建的文件序列号高位趋同；判据二：低 44 位**全库互异**——那是 MFT 记录号的取值范围。判据三：`fs_metadata_table` 给 `fsFileId` 单独建了索引 `index_fsFileId_fs_metadata_table`【D】，说明它是**高频按身份查询**的键。
- **`cloudFileId` = 云端文件 ID**（服务端分配的、长度不定的字符串；`file_metadata_table_drive.fileId` 也是 VARCHAR 且长度分布 {11:60, 12:61,…} 与之吻合【D】）。给 `cloudFileId` 也建了索引【D】。
- **`volSerial` 是留给「同 fsFileId 在多卷碰撞」的消歧列，本机 134 行全空**——说明 WPS 只在检测到碰撞时才填。**实现时不必照抄**，但要知道它存在。
- ⚠️ **任务书里「`activeState` 看起来像 volSerial」的猜测被实测否定**：`activeState` 实测 6 种取值 `{0:3, 293634312:120, 293897217:3, 294160399:2, 562069768:2, 1099203599:4}`【D】，**而 `volSerial` 是另一列、且全空**。`activeState` 与属性位对不上（§4.2 给实测拆解），它更像是**云端下发的「活跃/缓存代际」标记**，【?】确切语义未取到（需要抓一次服务端响应或反编译写入点）。

**`type` 字段枚举（实测）【D】**：`1=124, 2=4, 3=1, 4=3, 6=1, 7=1`，且**只有 `type=1` 的行带非零 `fileSize`（124 行全是文件）**，`type ∈ {2,3,4,6,7}` 的 10 行 `fileSize` 全为 0（都是目录/虚拟节点）。三行 `parentId='0'` 的根行 `type` 分别是 `6` 和 `3`（**注意是 2 行不是任务书说的 3 行**：【D】实测 2 行）。**【推】** 对照 `KSyncEngine` 与 `KSyncEngineCfApiSyncFolderVfs` 的字面量池（`shareTypes` / `virtualType` / `originType` / `LinkFileInfo` / `CloudItemPathInfo` / `folder` / `file`），`type` 大致是 `1=文件　2=文件夹　3=同步根　4=虚拟节点(共享/链接)　6=根　7=企业根`，**但映射未被直接观测，标【?】**；`file_metadata_table_drive.type` 用的是字符串 `'file'/'folder'`【D】，两表口径不一致。

> **对实现者的价值**：最小原型**只需** `id`（32 hex，自己生成）+ `fsFileId`（`GetFileInformationByHandleEx(FileIdInfo)` 取）+ `cloudFileId`（服务端 ID）+ `fileSize` + 时间戳。`FileIdentity` 直接塞 `id` 的 32 字节。**不要去碰 `activeState`**，语义未定。

### 3.3b 【2026-10-16 闭合】`CF_PLACEHOLDER_CREATE_INFO` 的真实填充（【G】，反编译到脚）

上一轮 §3.3 是**从函数名 + 数据模型联合推断**的（原文自标「【S，由函数名与数据模型联合确定】」），本轮**读到了填充代码本身**——并且**发现两处推断错误**。

**调用链**：`FUN_1005a820@0x1005a820` = `syncengine::cfapiutil::createPlaceholder`
→ `FUN_10088600`（`CfCreatePlaceholders` 的 `GetProcAddress` thunk）

```c
bool FUN_1005a820(QString *rootPath, QString_meta *item) {
  local_2c = QString::utf16(item + 4);                 // 相对文件名
  local_28 = *(int *)(*(int *)(item + 4) + 4) * 2 + 2; // 名字字节数（UTF-16 含 NUL）
  local_64 = QString::utf16(item);                     // 完整路径
  local_30 = *(undefined4 *)(item + 0x1c);             // 时间戳 A
  local_34 = *(undefined4 *)(item + 0x18);             // 时间戳 B
  local_24 = 2;                                        // ← Flags 初值 = MARK_IN_SYNC
  local_3c = 0x80;                                     // ← FileAttributes = FILE_ATTRIBUTE_NORMAL

  now = QDateTime::currentDateTime().toTime_t();       // 回退用的"现在"
  // 若元数据里带有效时间就用它，否则用 now
  ...
  local_5c = ts * 10000000 + 0x19db1ded53e8000;        // Unix 秒 → FILETIME(100ns)
  local_4c = ...
  local_54 = ...
  local_44 = ...

  if (*(int *)(item + 0x20) != 1) {                    // ← 不是"文件"
    local_24 = local_24 | 1;                           //   Flags |= DISABLE_ON_DEMAND_POPULATION
    local_3c = 0x10;                                   //   FileAttributes = FILE_ATTRIBUTE_DIRECTORY
    local_34 = 0; local_30 = 0;
  }

  DVar4 = FUN_10088600(QString::utf16(rootPath), &local_64 /*pinfo*/, 1 /*count*/,
                       0 /*flags*/, 0 /*out*/);
  if (DVar4 != 0) 报错 "create Placeholder error=%1"        // 行 0x256
  return DVar4 == 0;
}
```

#### 与 §3.3 推断的差异（两处必须更正）

| 项 | §3.3 原推断 | **实测** | 影响 |
| --- | --- | --- | --- |
| `Flags` | `CF_PLACEHOLDER_CREATE_FLAG_NONE` | **文件 `0x2` = `MARK_IN_SYNC`；目录 `0x3` = `MARK_IN_SYNC\|DISABLE_ON_DEMAND_POPULATION`** | **重要**：新建的占位符**一开始就标 in-sync**（避免首次同步风暴）；目录**禁止按需填充**（目录内容由自己的列表任务驱动，不让系统替它拉） |
| `FileAttributes` | 「由 `cffilestate::_setFileState` 与 `setCustomFileState` 派生」 | **目录 `0x10` (`FILE_ATTRIBUTE_DIRECTORY`)；文件 `0x80` (`FILE_ATTRIBUTE_NORMAL`)**——直接写死这两个值 | 简化：不是复杂派生，就是二分 |

#### §3.3 推断正确的部分（本轮坐实）

| 项 | 实测 |
| --- | --- |
| `FileSize` | 从元数据结构读（`item` 偏移），**不是 0** ✅ |
| `LastWriteTime` / `CreationTime` | 走 `Unix秒 × 10^7 + 116444736000000000` 转 FILETIME ✅ —— `0x19db1ded53e8000` 正是那张 `1601→1970` 纪元差常量，**该常量可作硬证据**（`116444736000000000`） |
| `FileIdentity` | `local_64` 指向的 32 字节块（对应 `fs_metadata_table.id`）✅ |
| **`FsMetadata`** | **未出现**——`FUN_10088600` 的 5 个参数里没有它（参数序：路径、pinfo、count、flags、out），`pinfo` 结构由栈上局部量拼出，**栈布局中无对应槽位被写** ✅ 推断正确 |

#### 三条可直接抄进实现的细节

1. **时间字段要处理"元数据没有"的情况**——代码里先取 `now` 做回退，再在检测到有效时间时覆盖（`if ((-1 < *(int *)(item+0xc)) && ((0 < *(int *)(item+0xc)) || (*(uint *)(item+8) != 0)))`）。**这是 Qt `QDateTime` 的 `isValid` 语义被内联**，空时间不能直接算 FILETIME。
2. **目录用 `DISABLE_ON_DEMAND_POPULATION`**——否则系统会尝试"按需填充"目录内容，与我们自己的列表任务打架。**这条是上一轮完全没提到的**。
3. **`MARK_IN_SYNC` 在创建时就置**——配合 §3.4 的 `CfSetInSyncState`，说明 WPS 的 in-sync 是**创建即同步态**，不是等第一次比对才置。

**级别**：【G】（自建 Ghidra 工程 + 脚本，无 REA Evidence ID）。复现见 §2.1b 同款命令，脚本 `scripts/reverse/DumpFuncs.java`。

### 3.3c 【2026-10-16 新答案】改名/移动**不走 CfApi 回调**，走**自己的目录监视器**

§3.2b 发现回调表里没有 `NOTIFY_RENAME` / `NOTIFY_MOVE` 之后，本轮把它查清了——**不是遗漏，是架构选择**。

**证据 1：类型名清单（RTTI，【S】）。** `ksyncengine.dll` 里有一整族**与 CfApi 平级**的 VFS 任务类：

```
KSyncEngineCfsRenameTask        KSyncEngineCfsMoveTask        KSyncEngineCfsRemoveTask
KSyneEngineSyncfolderVfsRenameTask   KSyneEngineSyncfolderVfsMoveTask   KSyneEngineSyncfolderVfsRemoveTask
KSyncEngineItemUpRenameContext  KSyncEngineItemUpMoveContext  (以及 Down*/CrossMove* 全套)
KYunkitRenameHandler            KYunkitBatchMoveHandler       KYunkitBatchRemoveHandler
KSyncEngineRenameItemUpdateFileCacheTask   KSyncEngineMoveItemUpdateFileCacheTask
```
命名规律：`Cfs*` = CfApi 侧、`Vfs*` = VFS 侧、`Up*/Down*` = 上行/下行方向、`*UpdateFileCacheTask` = 改完本地缓存再回写 `fs_metadata_table`。

**证据 2：目录监视器真的在跑（【G】，反编译到调用）。** 改名/移动的**输入源**是 Win32 目录变更通知：

```c
// syncengine::KSyncEngineWinFileMonObj::readChange   (FUN_1003c410@0x1003c410)
BVar7 = ReadDirectoryChangesW(
    *(HANDLE *)((int)this + 0xc),      // 目录句柄
    *(LPVOID *)((int)this + 0x14),     // 缓冲区
    0x4000,                            // ← 16 KB
    1,                                 // bWatchSubtree = TRUE（递归监视）
    DVar6,                             // 关心的变更类型（过滤位）
    &local_18,                         // 返回字节数
    (LPOVERLAPPED)((int)this + 0x18),  // 异步 OVERLAPPED
    NULL);                             // 完成例程为空 → 走异步等待
if (BVar7 == 0) 日志("ReadDirectoryChangesW failed! lastError=%1");   // 行 0x6b
```

**这条把 WPS 的本地改动检测路线定死了**：**不使用** CfApi 的 `NOTIFY_RENAME`/`NOTIFY_MOVE` 回调，而是自己开 `ReadDirectoryChangesW`（**16 KB 缓冲、递归、异步**）监视同步根，变更**自己解析**、再派发到上面那族 `*RenameTask` / `*MoveTask`。

#### 为什么这样选（以及对我们最重要的一条）

| | CfApi `NOTIFY_RENAME` / `NOTIFY_MOVE` | `ReadDirectoryChangesW`（WPS 实测选择） |
| --- | --- | --- |
| 触发时机 | 系统在占位符被改名/移动时回调 | 文件系统发生变更时通知（不区分是不是占位符） |
| 需要注册 | 要放进 `CF_CALLBACK_REGISTRATION` | 自建线程 + 句柄 |
| 能看见的操作 | **只**占位符的（CfApi 管理范围内） | **全部**磁盘变更（含非占位符、临时文件、编辑器写入） |
| 与 `DEHYDRATE` 的关系 | 回调式 | 同一条变更流 |

**推断的理由**：`NOTIFY_RENAME`/`NOTIFY_MOVE` 只在**占位符**被改名时回调，而同步根里还可能有**普通文件**（编辑器临时文件、`.wpsonline`、非云盘内容），统一走 `ReadDirectoryChangesW` 才能**一个来源管全部变更**——这也解释了为什么 `CF_CALLBACK_REGISTRATION` 只留 5 项（连 `NOTIFY_RENAME`/`MOVE` 都不占位）。

**对我们的直接意义**：切片 5 只做**只读占位符**时，**完全不需要** `NOTIFY_RENAME`/`NOTIFY_MOVE` 回调——这也是 WPS 自己的选择。等做双向同步时再决定是否补，届时可选：
- 走 CfApi 回调（更"正统"，但只覆盖占位符）；
- 或抄 WPS 走 `ReadDirectoryChangesW`（覆盖全，但要在用户态自己做变更合并与去抖）。

**WPS 的抑制机制（实测，非推断）**：不叫 `self_exciting_suppressor`（那是**钉盘** `sync_frame.dll` 的模块名，见钉盘取证 §8.5——**不要混用两家的机制名**），
而是 `KSyncEngineFileMon` 类上的一整套**过滤接口**【S】：

```
KSyncEngineFileMon::addWhiteList / removeWhiteList / addWhiteFolder / removeWhiteFolder
KSyncEngineFileMon::isInWhiteList / isIgnoreFilePath / isInExcludeExtNameList
KSyncEngineFileMon::addChangedFiles
KSyncEngineFileMon::slot_notifyTimerTigger     ← 定时器触发（去抖在这一层）
```
即：**白名单/白目录 + 路径忽略 + 扩展名排除 + 定时器批量冲刷**。`slot_notifyTimerTigger` 这个名字说明**不是来一个变更就处理一个**，而是**累积到定时器触发再批量派出**——这正是前面说的「去抖」的落点。
另有 `KSyncEngineVfs::addWhiteList` / `KSyncEngineVfsTask::addWhiteList`，说明白名单在 VFS 层与任务层各有一份。

> **写这段时的自我更正**：初稿把 `self_exciting_suppressor` 当成 WPS 的机制引用，**实际它只存在于钉钉的 `sync_frame.dll`**（`scripts` 外的字符串扫描证实 `ksyncengine.dll` 里该串出现 **0 次**）。两家的目录监视都源于 Win32，但**抑制机制各自实现、名字不同**，引用时必须分开。

**级别**：【S】类型名 + 【G】反编译到 `ReadDirectoryChangesW` 调用点（含行号 `0x44`/`0x59`/`0x6b` 与函数全名）。**无运行时观察**：没有实际触发过改名看它走哪条分支。

### 3.4 版本对账、in-sync 与自定义状态（P0-3 + §3.5）

**`CfUpdatePlaceholder` 的调用场景【S】**：`KSyncEngineCfApiSyncFolderUpdatePlaceholderTask::doIOTask`（失败日志 `file %1 not found`）与 `cfapiutil::updatePlaceholderInfo`（日志 `findPlaceholderInfo return NULL. path:%1` / `updatePlaceholderInfo failed. fileId: %1 name: %2 hr: %3`）。**上游是 `KCfApiSyncFolderDownTaskScheduler::_handleCloudModifyTask`**——即「远端改了（名字/大小/时间）→ 建一个 update 任务 → **只改占位符元数据，不下载内容**」。这正是任务书问的 P0-3，**答案：是，只更元数据**。配套还有 `_handleCloudPropChangedTask`（属性变更）与独立的 `KSyncEngineCfApiSyncFolderPropChangeTask`（失败日志 `can not change file %1 prop`）。

**`CfSetInSyncState`【S】**：`cfapiutil::setInSyncState`（`CfSetInSyncState failed. filePath: %1 isInSync: %2 hr: %3`）+ 任务类 `KCfApiSyncFolderSetInSyncTask::doIOTask`（`can not set %1 filed %2 to %3`）。**in-sync 的用途是修复**：`KCfApiSyncFolderVfsFolderDiffMaker::_judgeStateRepair` 的日志是 **`cfapi state not in sync`** / **`file %1 not in sync, cloudFileId=%2`**，而上层处理器是 `KCfApiSyncFolderRepairStateHandler`（日志 `repair state %1`）。**【推】** in-sync 标记的是「本地占位符的元数据/内容与云端一致」，掉标记 → 走修复任务重新 `CfUpdatePlaceholder` 或 `CfConvertToPlaceholder`。

**自定义状态（custom state）的搬运方式【S，重要结构性发现】**：`appendCustomStateToFile` / `clearCustomStateFromFile` **在 `.rdata` 里出现了两次**——一次在 `cfapiutil` 命名空间内（`0x000e0368` / `0x000e03ec`），一次在 `0x000e7434` / `0x000e74ac` 的**名表区**（与 CfApi 名表相邻）。名表区那两条严格夹在 `CfReportProviderProgress`（`0x000e7418`）与 `applyTransferStateToFile`（`0x000e750c`）之间——**这强烈指向它们是 Windows 提供的外部辅助导出**（CfApi 的 `CfGetPlaceholderStateFromFileInfo` / `CfGetPlaceholderInfo` 家族之外的 `Cf…` 辅助，或 `cldapi` 的未文档化导出），被 WPS **按名动态解析**；`applyTransferStateToFile` 同理。**【推，中置信，未取到 dll 导出表证实，标【?】。**

**自定义状态承载的取值（全部实测字面量【S】）**：`UploadFailed`、`Upload_FileSizeLimit`、`Upload_SpaceFull`、`CloudHasNewVersion`、`OnlineFile`、`FileAtVirtualFolder`、`LimitUploadFile`，以及人类可读文案 `Upload failed` / `File exceeds upload limit` / `Your cloud space was full` / `Has new version in service` / `Online document` / `Deleted online document` / `File uploading in the current directory is not supported` / `Synchronization to the cloud is not supported`。

**这与「属性位 → 覆盖图标」的关系（P0-5 的答案，见 §4）**：自定义状态是**资源管理器图标的语义来源**——WPS 用「自定义状态」把「上传中/超限/空间满」这类**业务状态**塞进占位符，再由它的 ThumbnailProvider/图标 COM 组件读出渲染。**但本轮没有观测到「自定义状态位 → 具体哪个图标」的映射表**：`appendCustomStateToFile` 只被证明**存在**，其 `customState` 参数的**位分配**未取到【?】。

### 3.5 读取节流：一条容易被忽略但**必须抄**的实现细节【S】

回调一旦接上，**本机所有程序读该目录都会触发下载**。WPS 做了一份**进程白名单 + 智能限流**：

```
KSyncEngineHydrationSmartLimiter::addHydrationJob / onEventTimer
  日志：empty name proccess start read %2
        %1 start read %2
        limite %1 read until %2
        %1 will read %2 at %3
        %1 will not be limited to read
```

**内置的进程名单（完整字面量，两段拼接）【S】**：

```
explorer.exe|kexplorermain.exe|360FileBrowser64.exe|360FileBrowser.exe|wps.exe|et.exe|wpp.exe|
wpspdf.exe|wpsofd.exe|wpscloudsvr.exe|wpsphoto+.exe|photolaunch.exe
TQSafeUI.exe|360rp.exe|2345VirusScan.exe|AvastSvc.exe|SysCleanerUI.exe|UniSensitive.exe|ksysslim.exe|
SearchProtocolHost.exe|ESCCIndex.exe|DFSSearchService.exe|svchost.exe|dllhost.exe|
baidunetdisk.exe|cloud-drive-daemon.exe|Dropbox.exe|weiyun_daemon.exe
```

**【推】** 前一组（含 `explorer.exe`、WPS 自家全家桶、360 的文件浏览器）是**放行**或**优先**；后一组（杀毒/索引/其它网盘）是**限流**对象。README 级别的启示：**只读原型如果不做这一步，在真机上第一次打开同步根会被 `SearchProtocolHost.exe` / 杀软打到满速下载全目录。**

### 3.6 Hydration 任务与孤儿占位符清理【S】

```
KSyncEngineHydrationJob::updateInfo        can not file item by fileId %1
KSyncEngineHydrationJob::slot_getFileInfoComplete   KSyncEngine_Hydration
   网络失败分支 networkError %1 / get fileinfo json object fail
   请求字段 fname groupid parent mtime fsize ftype user_nickname userid user_acl
KSyncEngineHydrationHelper::reportProgress   Couldn't report provider progress %1 hr %2
KSyncEngineHydrationHelper::overlappedCompletionRoutine
  Async read failed for %1, Status %2 / numberOfBytesTransfered=0
  Executing download for %1, Status %2, offset %3h`%4h length %5
  Downloaded data for %1, offset %2h`%3h length %4
  ReadFileEx failed. errCode: %1
  fail to remove cachedata file %1 errCode %2
KSyncEngineHydrationHelper::_copyFileImpl
  download %1 fileSize not equal with placeholder %2      ← 完整性校验
  Failed to open %1 for read, hr %2 / Failed to allocate read buffer for %1, hr %2
KSyncEngineHydrationMgr::onItemDownloaded  fileId %1 download at cacheFilePath:%2  SyncEngine_Hydration
KSyncEngineCfApiSyncFolderUpTaskNewHandler::_handleNotOnDiskFile
KSyncEngineCfApiSyncFolderUpTaskNewHandler::slot_placeholderRemoved
KCfApiSyncFolderDownTaskModifyHandler::slot_placeholderFreeUp
```

**这个流程是「先下到本地缓存文件、再覆盖进占位符」，且用 `download %1 fileSize not equal with placeholder %2` 做尺寸对账。** 另有字段 `cloud_yunpan_metadata_checksize` / `metadata_size` / `file_size` / `syncfolder`——说明**云端下发元数据时会声明一个校验尺寸**。

---

## 4. 桌面形态实测：属性位 → 覆盖图标（P0-5）

### 4.1 同步根与占位符的真实 reparse tag（**订正任务书**）

任务书给的是「Reparse Tag = `0x9000301a` = `IO_REPARSE_TAG_CLOUD_9`」。**实测这个 tag 属于同步根目录本身**。逐条目实测（`placeh.py`，`C:\Users\lishi\WPSDrive\<uid>\WPS云盘\` 下 133 个条目，**只读**）【D】：

| 对象 | Reparse Tag | 含义 |
| --- | --- | --- |
| **同步根目录本身** | `0x9000301a` | `IO_REPARSE_TAG_CLOUD_9` |
| **根的直接子目录** | **`0x9000601a`** | `IO_REPARSE_TAG_CLOUD_6` |
| **根下的占位符文件** | **`0x9000401a`** | `IO_REPARSE_TAG_CLOUD_4` ★ |
| 非占位符（普通文件） | 无 tag | `fsutil` 返回 4390 (`ERROR_NOT_A_REPARSE_POINT`) |

**分布【D】**：`0x9000401a` × 116、`0x9000601a` × 10、无 tag × 7。

结构上，`IO_REPARSE_TAG_CLOUD_0..F` 这 16 个 tag 就是**每个同步根分配一个**，低位（`0x…1a` 里的 `a`）是 tag 类型位，中间的 `0`/`4`/`6` 是**同步根实例号**。reparse data 头部实测可读：`01 00 92 00 46 65 52 70 …`（`0x0001`=version，`0x0092`=长度，`"FeRp"` 是 `Cf` 的压缩卷标识）+ `02 00 0a 00 07 00 01 00 60 00 00 00 0a 00 04 00 …`。

> **对实现者的价值**：注册同步根后，**系统会自动分配 `CLOUD_n` 实例号**，你不需要、也不能自己指定 `0x9000301a`。文档里任何「占位符 tag = 0x9000301a」的写法都是把**同步根**的 tag 误当成**占位符**的 tag。

### 4.2 属性位实测（与任务书一致，补全直方图）【D】

| 属性位组合 | 计数 | 十进制 | 拆解 |
| --- | --- | --- | --- |
| `0x00400020` | 116 | 4 194 336 | `FILE_ATTRIBUTE_SPARSE_FILE (0x200)` + `FILE_ATTRIBUTE_ARCHIVE (0x20)` = **已脱水占位符**（本地零字节） |
| `0x00180020` | 4 | 1 572 896 | `ARCHIVE` + `PINNED (0x80000)` + `UNPINNED (0x100000)` 之外的 … 见下 |
| `0x00180027` | 5 | 1 572 903 | `READONLY(0x1)+HIDDEN(0x2)+SYSTEM(0x4)+ARCHIVE+PINNED+UNPINNED` = 同步根内的 `desk***.ini`（系统生成的文件夹配置，非用户文件） |
| `0x00000031` | 3 | 49 | `READONLY+SYSTEM+DIRECTORY+ARCHIVE` |
| `0x00000010` | 3 | 16 | 纯 `DIRECTORY` |
| `0x00000011` | 1 | 17 | `READONLY+DIRECTORY` |
| `0x00000015` | 1 | 21 | `READONLY+SYSTEM+DIRECTORY` |

**任务书给的 `0x401620` / `0x180020` 与实测的 `0x00400020` / `0x00180020` 是同族**（`0x1620 = SPARSE|ARCHIVE|RECALL_ON_DATA_ACCESS|UNPINNED`、`0x180020 = SPARSE|ARCHIVE|PINNED`）。本机这一批**恰好没有 `RECALL_ON_DATA_ACCESS (0x400000)` 位**，即当前全部是 **hybrid/已脱水**，没有 `0x1620` 那一档。**【推】** 属性位随「是否允许后台回填」变化，不是固定两值。

### 4.3 属性位 → 覆盖图标的映射：**证据只到这里**

**能确证的（【D】+【R】）：**

| 证据 | 内容 |
| --- | --- |
| `SyncRootManager` 值 | `IconResource = <office6>\qingnse.dll,58`、`ThumbnailProvider = {BD0EE609-…}` |
| `qingshellext64.dll` 资源【R】 | **15 个具名 PNG**（id `103,104,105,306,307,308,312,313,314,1017,1018,1019,1027,1028,1029`，语言 2052）+ **3 个 RT_GROUP_ICON**（32×32 + 24×24） |
| `wpscloudsvr.exe` 资源【R】 | 1 个 RT_GROUP_ICON（48/32/16）+ `IDI_APPLICATION` |
| 注册的 COM 组件（本机只读注册表）【D】 | 见下表 |

**注册表实测的 WPS 云盘相关 COM 组件（`HKLM\SOFTWARE\Classes\CLSID`，全部 `Apartment`）【D】：**

| CLSID | 名字 | DLL | 类别【推】 |
| --- | --- | --- | --- |
| `{67F4D210-BFC2-4ADD-9A2A-C9B9E1F42C4F}` | `qingshellext Class` | **`qingshellext64.dll`** | 薄壳入口 |
| `{AA147FFB-0B1F-4BB1-9B1E-8D062B35C146}` | `QingNseContextMenu Class` | `qingnse64.dll` | 右键菜单 |
| `{3AE9FE89-8DA5-4EF6-9E27-F7547EE06DD7}` | `nsemenu Class` | `qingnse64.dll` | 命名空间菜单 |
| `{BD0EE609-3E41-4DA5-993F-4CA96C0B08F4}` | **`WpsDriveThumbnailProvider Class`** | `qingnse64.dll` | **缩略图/覆盖图标提供者**（与 `SyncRootManager.ThumbnailProvider` 同值） |
| `{783E484C-5E48-457B-A361-2DB2147CD2B4}` | `WpsDrive BHO` | `qingnse64.dll` | 浏览器辅助对象 |
| `{F3B92F9C-C41B-0947-5529-6910839FCB47}` | `WPS云盘特殊文件夹保护` | `qingnse64.dll` | 特殊文件夹保护 |
| `{FC397F71-A3E3-4198-86FB-39B7267E5E8C}` | **`WPS在线文档图标`** | `qingnse64.dll` | **在线文档覆盖图标** |

**菜单挂载点（`HKCU\Software\Classes`，实测 9 处）【D】**：
```
*\shellex\ContextMenuHandlers\Open With qingshellext      → qingshellext64.dll
*\shellex\ContextMenuHandlers\QingNseContextMenu          → qingnse64.dll
Directory\shellex\ContextMenuHandlers\…（同上两条）
Directory\Background\shellex\ContextMenuHandlers\…（同上两条）
lnkfile\shellex\ContextMenuHandlers\…（同上两条）
```

> **注意**：`HKLM\…\Shell Extensions\Approved` 里 **一个 WPS 的 CLSID 都没有**【D】。

**任务书问的三件事，逐条回答：**
1. **Shell 扩展注册了哪些 CLSID/菜单项** → 上表 + 上列 9 个挂载点，全部实测。
2. **除 `IShellExtInit` 外还有没有 `IExplorerCommand`/`IThumbnailProvider`/`IPreviewHandler`** → **有 `IThumbnailProvider`**（`WpsDriveThumbnailProvider Class` + `SyncRootManager.ThumbnailProvider` 同值，双重确证）；**`IExplorerCommand` 与 `IPreviewHandler` 未观测到**【?】（注册表里没有对应 CLSID 角色，`qingshellext64.dll` 的资源里也没有 `IPreviewHandler` 相关的预览图元）。
3. **`qingshellext64.dll` 是不是薄壳** → **是**：868 KB / PE32+，`inspect-pe-resources` 给出 36 条资源（15 PNG + 3 图标组 + REGISTRY/TYPELIB 等），而 CfApi 相关字面量**一个都没有**（`cfscan.py` 对该 DLL 的 22 个 CfApi/CloudFiles 关键词 ASCII+UTF-16 命中 **0**）。**它负责 UI 呈现（图标/菜单/属性页），所有 CfApi 逻辑在 `ksyncengine.dll`。**

**不能确证的（【?】，写入「仍未取到」）：** 「**具体哪个自定义状态位 → 具体哪个图标资源 id**」的映射表。要拿到它需要：(a) 反编译 `qingnse64.dll` 的 `WpsDriveThumbnailProvider` 实现（16.7 MB，按耗时线性外推 ≈ 20 分钟/次，本轮未做）；或 (b) 运行时观察（本轮边界禁止）。**15 个 PNG 具名资源 id 与 §3.4 的 7 个自定义状态取值之间存在一个「7→15」的映射，本轮只能确认"两边都存在"，不能确认对应关系。**

---

### 4.4 【2026-10-16 新证据】覆盖图标：WPS **不注册** Shell 覆盖图标处理器（【D】，否定性结论）

§4.3 当时把「自定义状态位 → 覆盖图标资源 id」列为缺口，并推测中间有一张映射表。**本轮从注册表侧拿到了否证**。

**实测本机 `ShellIconOverlayIdentifiers`（两个视图都查了）**：

| 位置 | 项数 | 内容 |
| --- | --- | --- |
| `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\ShellIconOverlayIdentifiers` | 7 | **6 个 `.WorkspaceExt0`–`.WorkspaceExt5`** + `EnhancedStorageShell` |
| `HKLM\SOFTWARE\WOW6432Node\...\ShellIconOverlayIdentifiers` | 6 | 同上（无 `EnhancedStorageShell`） |

**这 6 个 `.WorkspaceExt*` 的 CLSID 逐个反查 `InprocServer32`，全部指向百度网盘**：

```
{C568C78A-…} {2A6FE247-…} {FF895810-…} {D8BE1E70-…} {7FAAC51E-…} {2433C980-…}
        → 全部 = D:\Program\baidu\BaiduNetdisk\YunShellExtV164.dll
```

**没有任何一项指向 WPS**（`ksyncengine.dll` / `qingnse64.dll` / `qingshellext64.dll` 均未出现在覆盖图标注册表里）。

### 这意味着什么（对实现者是关键简化）

**CfApi 的覆盖图标由 Windows 自己渲染，不需要提供方注册 `IShellIconOverlayIdentifier`。**

CfApi 有内建状态（in-sync / pending / pinned / 自定义状态），资源管理器**直接读占位符状态**画覆盖图标——这就是为什么 WPS 装了完整的同步根，却不需要在 `ShellIconOverlayIdentifiers` 里占位。

**反面证据同时存在**：百度网盘用的是**老式方案**（自己注册 6 个覆盖图标处理器 + `.WorkspaceExtN` 前缀抢排序），正是 CfApi 时代之前（或不用 CfApi）的做法。两种形态在这台机器上**并存且可对比**，这本身就是一份难得的对照样本。

> **Windows 覆盖图标的硬限制**（解释 `.WorkspaceExt0` 的命名动机）：资源管理器**只加载前 15 个**（按字母序），百度用 `.` 前缀把名字排到最前。**如果我们也走老式方案，会与百度/OneDrive 抢这 15 个位置**；走 CfApi 则完全不进这个赛道。

**对 §9「只读占位符最小闭包」的影响**：原第 6 步（pin/释放）之后的图标部分**可以整段删掉**——**不需要写任何覆盖图标 COM 组件**。这是本轮的净简化。

**级别**：【D】（本机只读注册表实测，两个视图 + CLSID 反查）；**无运行时观察**（未启动 WPS 看图标实际长什么样）。

---

## 5. 本地数据模型（P1）

产物：`C:\Users\lishi\WPS Cloud Files\.<uid>\`（隐藏目录，账户级缓存），**主库 `cache.db` 376 832 B**【D】。**只读副本**读取（`sqlite3` 的 `mode=ro&immutable=1`），复制到 `output/wpsdata/` 后读取，本机实测**无 `-wal` / `-shm`**【D】。

### 5.0 18 张表的全景【D】

```
cache.db 的 18 张表（含 sqlite_sequence）与行数：
  fs_metadata_table            134      ← ★ 占位符表
  file_metadata_table_drive    124      ← 云端元数据的本地投影（云盘）
  sharedtome_metadata_table     49      ← 分享给我的
  group_metadata_table           5
  data_ver_table                 5      （版本计数器）
  file_version_state_table       3      ← ★ 版本对账
  filecache_data_drive           2      ← ★ 扩展信息
  filetransfer_table             2
  recyclebin_metadata_table      0      ← ★ 回收站模型（表结构在，空）
  company_metadata_table / file_thumbnail_table / metadata_syncresult /
  quickaccess_item_table / sharedwithme_table / star_item_table /
  tempstoretransfer_table / user_thumbnail_table   均为 0
其余 10 个库（同目录，均已实测存在）：
  batchupload.db        → batch_upload_table
  cloudfile.db          → cloud_table
  docTag.db             → tagDocMapping, tagInfo
  kdocsofflinepage.db   → data_ver_table, docs_offline_page_data_table
  localfileuploadrelated.db → data_ver_table, local_file_upload_related_table
  precloudfile.db       → pre_cloudfile
  recentfile.db         → cloudfile_not_uploaded
  syncassistant.db      → data_ver_table, sync_assistant_table
  toDeleteFiles.db      → to_delete_files
  transferhelper.db     → device_table, msg_record_table, seq_record_table
```

`cache.dat`（4 420 B）、`autoupdatedrive.ini`、`recentcloudfolder.xml`、`specificcloudteam.xml`、`startpagerecent.xml`、`thumbnail_<uid>`、以及 `cachebackup/ cachedata/ file_thumbnail/ hyperionlocalcache/ syncfolderbackup/ user_thumbnail/` 六个子目录亦实测存在【D】。**`C:\Users\lishi\WPS Cloud Files\` 下另有 `hyperionlocalcache/` 与第二个账户目录**；**`C:\Users\lishi\WPSDrive\` 下有两个 uid 目录**【D】。

### 5.1 `fs_metadata_table` — 占位符表（134 行）★

```sql
CREATE TABLE fs_metadata_table (
  id VARCHAR(32) PRIMARY KEY,
  parentId VARCHAR(32) DEFAULT '',
  fsFileId BIGINT DEFAULT -1,
  cloudFileId VARCHAR(50) DEFAULT '',
  fileName VARCHAR(256) DEFAULT '',
  localName VARCHAR(256) DEFAULT '',
  type INTEGER DEFAULT 1,
  fileSize BIGINT DEFAULT 0,
  createdTime INTEGER DEFAULT 0,
  modifiedTime INTEGER DEFAULT 0,
  state INTEGER DEFAULT 0,
  extraInfo VARCHAR(256) DEFAULT '',
  keepOnThisDeviceState INTEGER DEFAULT 0,
  activeState INTEGER DEFAULT 0,
  volSerial VARCHAR(64) DEFAULT ''
);
CREATE INDEX index_parentId_fs_metadata_table on fs_metadata_table(parentId);
CREATE INDEX index_fsFileId_fs_metadata_table on fs_metadata_table(fsFileId);
CREATE INDEX index_cloudFileId_fs_metadata_table on fs_metadata_table(cloudFileId);
```

| 列 | 语义 | 实测分布【D】 |
| --- | --- | --- |
| `id` | **本地代理 ID（32 hex）**，占位符树的主键；**【推】即 `CF_PLACEHOLDER_CREATE_INFO.FileIdentity`** | 134/134 长度 = 32 |
| `parentId` | 父节点的 `id`；`'0'` 表示根 | 132/134 可解析到存在的 `id`，**0 孤儿**；2 行 `parentId='0'`（根行） |
| `fsFileId` | **NTFS 64 位 File ID** | 134/134 有值；`>>56` 全库同值、低 44 位全库互异（见 §3.3） |
| `cloudFileId` | **云端文件 ID（变长字符串）** | 134/134 非空；长度 {11:60, 12:61, 10:3, 8:1, 2:6, 18:1, 20:1, 31:1} |
| `fileName` / `localName` | 云端名 / 本地实际名 | **4 行两者不同** |
| `type` | 节点类型 | `1:124, 2:4, 3:1, 4:3, 6:1, 7:1`；**只有 `type=1` 有非零 `fileSize`** |
| `fileSize` | 云端大小 | `0 … 26 281 641`，均值 ≈ 1.30 MB |
| `createdTime` / `modifiedTime` | Unix 秒 | max = `1760404627` |
| `state` | 状态位 | **134/134 全为 0**（本机未产生非零态） |
| `extraInfo` | 扩展 | **134/134 全为空串** ⇒ **`FsMetadata` 未被使用**（§3.3） |
| **`keepOnThisDeviceState`** | 「始终保留在这台电脑上」开关 | **134/134 全为 0**（无固定项）；与 `fileSize>0` 交叉为 **0** |
| `activeState` | **待定** | `{0:3, 293634312:120, 293897217:3, 294160399:2, 562069768:2, 1099203599:4}` |
| `volSerial` | 卷序列号（消歧用） | **134/134 全为空串** |

> **`keepOnThisDeviceState` 与 `activeState` 的区别（任务书点名）**：`keepOnThisDeviceState` 语义明确 = 用户级「固定」意图（0/1），本机全 0；`activeState` 与它无关，也**不是 volSerial**（`volSerial` 是独立一列且全空）。`activeState` 的 6 个取值与文件属性位**对不上**（116 个 `0x00400020` 占位符里混着多种 `activeState`），**更可能是云端下发的"活跃度/缓存代际"整数**。【?】确切语义未取到——需要反编译写入点或抓一次服务端响应。

### 5.1b 【2026-10-16 部分推进】`activeState` 的位结构（【D】，有收敛但**未闭合**）

上一轮列为「取法：反编译写入点或抓包」。本轮改走**本机数据交叉验证**——把 `fs_metadata_table` 的每一行与**磁盘上的实际文件**对起来，得到下表（只统计磁盘上真实存在的条目）：

| `activeState` | 十六进制 | 低 6 位解码（`CF_PLACEHOLDER_STATE` 掩码） | 实测磁盘属性 | 条目特征 |
| --- | --- | --- | --- | --- |
| 293634312 | `0x11808108` | `0x08` = **`IN_SYNC`** | `0x400020` = `ARCHIVE\|RECALL_ON_DATA_ACCESS`（文件）/ `0x10`（目录） | 116 文件 + 4 目录，**正常已同步**那批 |
| 562069768 | `0x21808108` | `0x08` = **`IN_SYNC`** | `0x180020` = `ARCHIVE\|PINNED\|UNPINNED` | 2 个 `.wpsonline` |
| 294160399 | `0x1188880f` | `0x0f` = **`PLACEHOLDER\|SYNC_ROOT\|ESSENTIAL_PROP_PRESENT\|IN_SYNC`** | `0x180020` = `ARCHIVE\|PINNED\|UNPINNED` | 2 个 `.wpsonline` |
| 1099203599 | `0x4184840f` | `0x0f` = 同上 | （4 个均不在盘） | `desktop.ini` ×4 |
| 293897217 | `0x11848401` | `0x01` = **`PLACEHOLDER`** | （3 个均不在盘） | 「分享的文件夹」×3 |
| 0 | `0x00000000` | `0x00` = **`NO_STATES`** | `0x30`/`0x15` | type 3/6/7 特殊节点 |

#### 成立的结论

**低 6 位 = `CF_PLACEHOLDER_STATE` 位掩码。** 实测取值集合 `{0x00, 0x01, 0x08, 0x0f}` **全部落在官方位定义内**（`NO_STATES`/`PLACEHOLDER`/`IN_SYNC`/四者相或），且**每个组合的语义都与该行条目的性质自洽**：

- 正常同步的 120 条 → `IN_SYNC`（`0x08`）✅
- 3 个「分享的文件夹」（本地未落盘）→ 仅 `PLACEHOLDER`（`0x01`），**没有 `IN_SYNC`** ✅ 自洽（未同步当然不 in-sync）
- 特殊目录节点 → `NO_STATES`（`0x00`）✅

这条**同时修正了上一轮的两个排除性结论**：「已排除属性位」——**低字节不是属性位，是高 16 位里的位与 `CF_PLACEHOLDER_STATE`**；上一轮把整值当整体去对属性直方图，才得出对不上的结果。

#### 未闭合的部分（照实说）

**高 24 位（`0x118081` / `0x118484` / `0x118888` / `0x218081` / `0x418484`）未解。** 已确立的事实：

1. **它与低 8 位独立**：`0x11808108` 与 `0x21808108` **低 16 位完全相同**，但高 8 位一个是 `0x11` 一个是 `0x21`——两者都是 `.wpsonline`，区别只在 `PINNED` 之外的某状态。
2. **它逐字节自成结构**：小端内存布局 `[08 81 80 11]` 呈「低字节 = 状态掩码，其上三个字节各有含义」的形态，`0x80`/`0x84`/`0x88` 三档递增，暗示**是一个计数器或阶梯式枚举**而非位掩码。
3. **可能对应 `CfGetPlaceholderInfo` 的 `CfGetCustomState`**——CfApi 允许提供方把自定义状态挂在占位符上，这个 32 位整数很可能就是「自定义状态槽」，前 8 位给 CfApi 标准状态用，其余留给自己。

**取法（下次别再猜）**：反编译 `fs_metadata_table` 的 **UPDATE/INSERT 语句构造点**，看 `activeState` 是哪个变量写进去的；`activeState` 这个列名本身也是线索（CfApi 侧的 `CF_PLACEHOLDER_STATE` 官方名就叫 "state"，加 `active` 前缀说明存的是**派生值**）。

**级别**：低 6 位 = 【D】+ 与官方位定义比对，**成立**；高 24 位 = 【?】，仍未知。

3. **`activeState` 的高 24 位未解**（低 6 位 = `CF_PLACEHOLDER_STATE` 掩码已于 2026-10-16 坐实，见 [§5.1b](#51b-2026-10-16-部分推进activestate-的位结构d有收敛但未闭合)）。
   已确立：高位与低位**独立**（`0x11808108` 与 `0x21808108` 低 16 位相同、高 8 位不同）；逐字节呈阶梯（`0x80`/`0x84`/`0x88`），疑为 CfApi 自定义状态槽。
   **取法（别再猜位数）**：反编译 `fs_metadata_table` 的 **UPDATE/INSERT 语句构造点**，看 `activeState` 由哪个变量写入、其来源是什么。

4. **`type` 字段的枚举映射（`1/2/3/4/6/7` → 文件/文件夹/根/虚拟节点/…）。**
   实测分布与「只有 `type=1` 有非零 size」的规律已给出强约束，但**确切名字未取到**。

5. **`file_version_state_table.action` 与 `sources` 的位含义。**
   实测 `action ∈ {1,2}`、`sources ∈ {32,4096}`、`sources2 ≡ 0`，且 `action` 与 `sources` 强相关。**取法**：反编译 `KSyncEngineSyncTask::_initLockCheckInfoByVfsMetaData` / `_initLockCheckInfoByFileCacheMetaData`（类名已定位，函数体未读）。

6. ~~「自定义状态位 → 覆盖图标资源 id」的映射表（P0-5 的缺口）~~ → **2026-10-16 判定为伪问题**（[§4.4](#44-2026-10-16-新证据覆盖图标wps-不注册-shell-覆盖图标处理器d否定性结论)）：**WPS 根本没有注册 Shell 覆盖图标处理器**，CfApi 的图标由 Windows 依占位符状态自绘。**这张映射表不需要存在**，无需再找。

7. **`qingnse64.dll` 的完整分析：工程已建，未逐函数挖掘**（16.7 MB）。
   只有字节层字面量结论（§2.1 的 `\cldapi.dll` + `CfGetPlaceholderInfo` + `CfGetSyncRootInfoByPath`）。

8. **`applyTransferStateToFile` 的真实来源。**
   它和 `appendCustomStateToFile` / `clearCustomStateFromFile` 一起出现在 CfApi 名表区（`0x000e7434`–`0x000e756c`），**怀疑是 `cldapi.dll` 的未文档化导出**，但未取到 `cldapi.dll` 的导出表证实。

9. **无任何运行时观察。** 本文全部为静态产物 + 本机只读数据。§3.2 的「点开才下载」、§3.5 的节流、§3.6 的 hydration 流程，都是**分支存在**级别的证据，不是**运行时真走到**级别的证据。

10. **`SyncRootManager` 里为何只有企业云盘一个根**（个人云盘根未登记）。实测只有 `WPSCloudProvider!…!<企业名>` 一条；【?】个人云盘是否走 `CF_SYNC_ROOT_REGISTER` 之外的机制，或该根登记在本机另一账户上下文。

---

相关：`docs/intelligence/2026-10-15-dingpan-file-storage-reverse.md`（钉盘，§5 为 CfApi 对照基线）、`docs/toolchain-rea.md`（REA 工具链备忘，本文 §1.2 的四条坑建议回填）。

### 5.2 `file_version_state_table` — 本地/云端版本对账（3 行）★

```sql
CREATE TABLE file_version_state_table (
  id INTEGER PRIMARY KEY AUTOINCREMENT, fileId VARCHAR(100), groupId VARCHAR(100),
  parentId VARCHAR(100), cacheItemId VARCHAR(100), localPath VARCHAR(512), fileName VARCHAR(260),
  fileSize INTEGER, localVersion INTEGER, localVersionModifiedTime INTEGER, localModifiedTime INTEGER,
  cloudVerson INTEGER, targetVersion INTEGER, taskBeginTime INTEGER,
  action INTEGER, sources INTEGER, sources2 INTEGER, extendInfo VARCHAR(512)
);
```
（`cloudVerson` 的拼写确实是 **Verson**，实测 schema 原样如此。）

**三行的完整版本三重态【D】**：

| `localVersion` | `cloudVerson` | `targetVersion` | `action` | `sources` | `sources2` | `fileSize` | `localPath` 空? | `fileName` 空? |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `1` | `0` | `2` | `1` | `32` | `0` | 10 849 809 | 否 | 是 |
| `0` | `0` | `1` | `1` | `32` | `0` | 0 | 否 | 是 |
| `0` | `0` | `0` | `2` | `4096` | `0` | 14 517 | 是 | 是 |

**三者关系【推，证据充分】：**
- `localVersion` = 本地那份内容对应的云端版本号；`cloudVerson` = 已知的最新云端版本；**`targetVersion` = 这次操作要达成的版本**。
- 第 1 行 `local=1, cloud=0, target=2`：本地比已知云端新 → **要上传，目标是让它变成第 2 版**。
- 第 2 行 `local=0, cloud=0, target=1`：本地无内容（`fileSize=0`）→ **要下载，目标是拿到第 1 版**。
- 第 3 行 `local=0, cloud=0, target=0`：`target=0` 且 `action=2` → **不是版本对齐，是另一种动作**（见下）。
- **`action` 实测只有 `{1:2, 2:1}`**；**`sources` 实测只有 `{32:2, 4096:1}`**；`sources2` **全为 0**。
- 【推】`action=1` 与 `sources=32` 强相关（2/2），`action=2` 与 `sources=4096` 强相关（1/1）。按位看：`32 = 1<<5`、`4096 = 1<<12`——**是位标志而非枚举**。`action 1 = 版本推进（上传/下载）`，`action 2 = 其它状态类任务`。【?】位含义未取到（需要反编译 `KSyncEngineSyncTask::_initLockCheckInfoByVfsMetaData` 一族）。
- `localVersionModifiedTime` 与 `localModifiedTime` **3/3 行相等**【D】。

### 5.3 `filecache_data_drive` — 扩展信息（2 行）★

**`extendInfo` 实测是一个 `key \u2029 value \u2028 key \u2029 value …` 的拼接串，字段分隔符是 U+2029（PARAGRAPH SEPARATOR），记录分隔符是 U+2028（LINE SEPARATOR）**——不是空格也不是 `=`。【D】

两行**完整**的 key 集合（值已按隐私要求省略业务相关内容）【D】：

```
fileVersionTime        4294967295    (= 0xFFFFFFFF，哨兵「无」)
roamingLocalModifyTime -28800000     (=-8h，UTC+8 时区偏移，哨兵)
isOnlyRoamingFileCache 0
transferVersion        1
findConflictTime       0
autoRename             0
errmsg                 文件大小超过限制        ← 该文件尝试上传被拒，原因是超出单文件大小上限
linkStatus             (空)
linkExpiredTime        -1
shareFrom              '' / wps            ← 两行取值不同
uploadTime             4294967295
newCloudVersionTime    4294967295
```

**key 语义【推】：**

| key | 含义 |
| --- | --- |
| `fileVersionTime` | 当前本地内容对应的版本落盘时间（`0xFFFFFFFF` = 无） |
| `roamingLocalModifyTime` | 漫游场景的本地修改时间（带时区偏移哨兵） |
| `isOnlyRoamingFileCache` | 是否**只**作为漫游缓存（不入云盘） |
| `transferVersion` | 传输协议版本 |
| `findConflictTime` | 上次发现冲突的时间 |
| `autoRename` | 是否已自动改名（冲突解决动作已执行） |
| `errmsg` | 上次失败原因（**本机实测值就是「文件大小超过限制」**） |
| `linkStatus` / `linkExpiredTime` | 分享链接状态/过期时间（`-1` = 不过期） |
| `shareFrom` | 来源渠道（`wps` = WPS 侧分享） |
| `uploadTime` | 上传完成时间 |
| `newCloudVersionTime` | 云端出现新版本的时间 |

**同表实测（非 `extendInfo`）【D】**：`historyId` 空、`lastSha1` 空、`fileSize` 两行都是 `10 849 809`、`fileVersion=1`、`uploadCode=19`、`downCode=0`、`isOlderVersion=0`、`abandoned=0`、`isCurrentSyncTask=1`、`roamingSuccess=1`；两行 **`taskId` 前 2 字符相同**、**`fileId` 前 2 字符不同**——即**同一个文件的两条任务记录**（`UNIQUE(taskid)`），印证「任务表而非文件表」。

> `uploadCode=19` 与 `errmsg=文件大小超过限制` 同现，**【推】`uploadCode` 是上传结果码，19 = 超限**。

### 5.4 云端元数据的本地投影（`file_metadata_table_drive` / `sharedtome_metadata_table`）

**`file_metadata_table_drive`（124 行）【D】**：`type` 用**字符串** `file(120) / folder(4)`；`fileState ∈ {0:118, 1:4, 16:2}`；`userAcl ∈ {2047:122, 0:2}`（2047 = 全部 11 个权限位，即**完全控制**）；`version ∈ {1:120, 2:2, 4:1, 89:1}`（**89 版说明有长历史**）；`name == localName` 124/124；`localParentId` 长度 ∈ {1:71, 32:53}——**长度 1 的是根（`'0'`），长度 32 的是指向另一个 `id`**（53 个能解析）；`linkId` 124/124 为空；`creatorId` 有 3 个不同值。

**`extraInfo`（124 行全部非空）实测的 key 集合（U+2029/U+2028 同款分隔）【D】**：`creator`、`cloudVersion`、`originType`、`fsha1`（**内容 SHA-1！**）、`syncTime`。例：`creator\u2029…\u2028cloudVersion\u20291\u2028originType\u2029file\u2028fsha1\u20296736ac4afe24e7098f746c7535ab22dbb15d2c9d`、以及目录行的 `syncTime\u20291730013231\u2028originType\u2029folder\u2028creator\u2029…`。

> ⚠️ **两个关键发现**：① **WPS 有文件级内容哈希（`fsha1`）**——这是我们全仓缺失的能力（钉盘用的是 md5）；② `extraInfo` 是**自由 KV 面**，加字段不改 schema，**这正是我们该抄的元数据扩展手法**（我们的 `es_file` 加列就要改表）。

**`sharedtome_metadata_table`（49 行）【D】**：`type ∈ {link:46, group:3}`；`userRole ∈ {'':46, member:3}`；`permission ∈ {write:20, '':25, read:4}`；`fileType ∈ {sharefile:46, '':3}`；`sid` 47 个不同值；`groupCorpId` 2 个不同值。**【推】** `type='link'` 是**分享链接拉进来的项**（`sid` = 分享 ID），`type='group'` 是**群组/企业共享**（带 `groupCorpId`）；`permission` 是**逐项权限**（read/write）。

### 5.5 `recyclebin_metadata_table` — 回收站模型（0 行，表结构在）★

```sql
CREATE TABLE recyclebin_metadata_table (
  id VARCHAR(32) PRIMARY KEY, fileId VARCHAR(50), groupId VARCHAR(50),
  parentId VARCHAR(50),           -- ★ 注意：比 file_metadata_table_drive 多这一列
  localParentId VARCHAR(32), name VARCHAR(256), localName VARCHAR(256),
  type VARCHAR(20) DEFAULT 'file', creatorId VARCHAR(50), version INTEGER, size INTEGER,
  createdTime INTEGER, modifiedTime INTEGER, userAcl INTEGER, fileState INTEGER,
  taskId VARCHAR(32), linkId VARCHAR(50), extraInfo VARCHAR(256)
);
```

**本机 0 行**【D】，但**表结构泄露了模型**：它比 `file_metadata_table_drive` **多一个 `parentId`（云端父 ID）**，同时保留 `localParentId`——意味着**回收站条目同时记住"云端原来在哪"和"本地原来在哪"**，恢复时两边都能还原。**这正是我们 `es_file` 缺的**（钉盘那份文档 §9 已列出同一差距，WPS 用同一手法）。

**【推】** 删除 → 行从 `file_metadata_table_drive`/`sharedtome_metadata_table` 迁到 `recyclebin_metadata_table`，占位符从 `fs_metadata_table` 摘除；恢复反向。`toDeleteFiles.db` 的 `to_delete_files` 表是**待删队列**，与回收站是两个阶段。

### 5.6 `data_ver_table` — 一张表一个版本号【D】

`cache.db` 的 `data_ver_table` 实测 5 行：`file_cache_drive=1`、`filetrans_cache=1`、`tempstoretransfer_cache=1`、**`file_verion_state_cache=1`**（注意又拼错成 `verion`）、`file_metadata_drive=1`。另有 4 个库各自带自己的 `data_ver_table`。**【推】** 这是**本地缓存 schema/内容版本闸门**——版本不符即触发整表重建。对照钉盘的 `spaceVersion + redolog`，WPS 这边**只看本地缓存版本，不看远端增量游标**（那部分推测在 `syncassistant.db` / `transferhelper.db.seq_record_table` 里，本轮未展开）。**两者不是同一种增量机制**，见 §8。

---

## 6. `ksyncengine.dll` 二进制面（P1-6）

**【R】** `ev_58891b06…`：7 段、**7 987 函数**、**2 962 串**、PE32（**x86 32 位**，非 64 位）；`.text` 860 160 B / `.rdata` 242 176 B。引入 11 个 DLL（`ksolite.dll` `krt.dll` `Qt5CoreKso.dll` `KERNEL32` `MSVCP140` `VCRUNTIME140` + 5 个 `api-ms-win-crt-*`）+ 4 个延迟导入（`SHELL32` `ole32` `ADVAPI32` `SHLWAPI`）；**`KSOLITE.DLL` / `KRT.DLL` / `QT5COREKSO.DLL` 未在分析中解析**（Ghidra 日志 `not found in project`），故 Qt 侧调用链是**黑区**。

**【S】** 同步状态机与任务谱系（完整 RTTI 名，562 项中的关键子集）：

```
同步任务类型（KSyncEngine*SyncTask）：
  KSyncEngineSyncTask（基类，hasNextAction/doNextAction/getLockFolderId/getCheckFolderId）
    ├─ KSyncEngineFileMonSyncTask      ← 本地文件监控触发
    ├─ KSyncEngineFullSyncTask         ← 全量：_fetchLocalDiff / _pullMetaData / _fetchCloudDiff
    │                                    / _makeUpTask / _makeDownTask / _checkInvalidLocalNode
    ├─ KSyncEngineWpsSaveSyncTask      ← WPS 保存文档触发
    ├─ KSyncEngineBrowseSyncTask       ← 用户在资源管理器里浏览目录触发
    ├─ KSyncEngineNotifySyncTask       ← 服务端推送触发
    └─ KCfApiSyncFolder* 版（CfApi 专属子类，同名覆盖）
上游任务生成器：
  KSyncEngineUpTaskMaker::handleAdd/handleModfied/handleRemove/handleRename/handleMoved
  KSyncEngineDownTaskMaker::…+ handlePropertyChange
CfApi 专属生成器：KCfApiSyncFolderUpTaskMaker::_handleFreeUp / _handleStateRepair
调度器：…UpTaskScheduler / …UpFileTaskScheduler / …DownTaskScheduler /
        …DownFileTaskScheduler / KSyncEngineTaskSchedulerBase
CfApi 专属调度器：KCfApiSyncFolderDownTaskScheduler::_handleCloudNewTask /
        _handleCloudModifyTask / _handleCloudCrossMoveTask
下游处理器（每种事件一个 handler.start / slot_taskFinished）：
  Up:   NewHandler(_handleFile/_handleFolder/_buildReleation)
        ModifyHandler  RemoveHandler(_dealNotUploadDelete)  RenameHandler  MoveHandler
        NewTransHandler  ── CfApi 版另有 NewHandler::_handleNotOnDiskFile / slot_placeholderRemoved
  Down: NewHandler  ModifyHandler(_handleFolder/_handleFile)  RemoveHandler  RenameHandler
        MoveHandler  PropChangeHandler  CrossMoveHandler(handleFileImpl/handleNew)
差异引擎：KSyncEngineVfsDiffMaker / KSyncEngineFolderDiffMaker(_judgeRemove/_judgeRename/
        _judgeMove/_judgeOtherAction) / KSyncEngineFileDiffMaker
        CfApi 版：KCfApiSyncFolderVfsFolderDiffMaker::_judgeFreeUp / _judgeStateRepair
VFS 抽象：KSyncEngineVfs { addWhiteList/removeWhiteList/getDiffList/getDiff/
        notifyItemStartUploading/slot_localFileChanged/slot_localFileRemoved/
        slot_syncFolderRootRemoved/slot_diffMade }
本地文件监控：KSyncEngineWinFileMon（ReadDirectoryChangesW 一族：
        KSyncEngineWinFileMonObj::readChange/getOverlappedEvent/dealFileMonNotify/
        fireFileChange/isKsoTempFile，按 File/Foler/Attr 三类对象分派）
RPC：KSyncEngineBatchRpcManager、KYunkit*Handler（Rename/BatchMove/BatchRemove/
        CreateFolder/GetFileInfoV5/BatchOpQuery/DirectJson）、rpcutils::*
引擎工厂：KSyncEngineBaseFactory<T, EnumType> × 3（Engine / Vfs / FileMon）
```

**【S】错误与边界分支的日志串**（精选，全部实测）：

```
UploadFailed / Upload_FileSizeLimit / Upload_SpaceFull / CloudHasNewVersion / OnlineFile /
FileAtVirtualFolder / LimitUploadFile        ← 自定义状态取值（也是「传播给资源管理器」的状态名）
Upload failed / File exceeds upload limit / Your cloud space was full /
Has new version in service / Online document / Deleted online document /
File uploading in the current directory is not supported / Synchronization to the cloud is not supported
Upload_FileSizeLimit ↔ 「文件大小超过限制」（与 cache.db 的 errmsg 实测值完全吻合）
CfApiVfs: connectSyncRoot error=%1 / CfApiVfs: disconnectSyncRoot error=%1
isSystemEnvCompatibleWpsDrive return false, cannt connectSyncRoot   ← ★运行环境闸门
registerSyncRoot %1 error=%2 / unregisterSyncRoot %1 error=%2
%1 has register by %2 / %1 %2 has register with other syncfolder %3  ← ★同步根冲突检测
on disk file [%1] exsit / create placeholder %1
can not set %1 filed %2 to %3 / can not get cfapi info of file %1
can not change file %1 prop / file %1 not found
cfapi state not in sync / file %1 not in sync, cloudFileId=%2 / repair state %1
has free up file state / retry free up / file %1 has free up state, cloudFileId=%2 / free up %1
vector too long / dstFolderPath is null / transFac is null
```

**`isSystemEnvCompatibleWpsDrive return false, cannt connectSyncRoot` 是重要的一条【S，推】**：WPS 在 `CfConnectSyncRoot` 之前会做一次**系统环境兼容性检查**（OS 版本、`cldapi.dll` 可用性等），不通过就**根本不连同步根**。**实现时必须抄这个闸门**，否则在 Win10 早期版本上会拿到未定义的失败。

---

## 7. Shell 扩展与注册表（P1-7）

已在 §4.3 完整给出。要点复述：

- `qingshellext64.dll` = **薄壳**（868 KB / PE32+ / 无任何 CfApi 字面量），注册 CLSID `{67F4D210-…}`（`qingshellext Class`）+ 9 个右键菜单挂载点。
- `qingnse64.dll`（16.7 MB）= **实际 UI 与查询层**：`QingNseContextMenu Class`、`nsemenu Class`、`WpsDrive BHO`、`WpsDriveThumbnailProvider Class`、`WPS在线文档图标`、`WPS云盘特殊文件夹保护`；字面量面含 `\cldapi.dll` + `CfGetPlaceholderInfo` + `CfGetSyncRootInfoByPath`（**只读查询**），以及 `showWpsDriveConflictByCfApiDlg` / `requestIsWpsDriveConflictByCfApi` / `KCloudProxyPrivate::requestIsWpsDriveConflictByCfApi`（**冲突对话框走 IPC，经 Qt 代理**）。
- **`pdata` 段名出现 `.detourc` / `.detourd` 两个异常段【D】**（`qingnse64.dll`，PE 段表实测）——**说明它被 hook 框架（Detours 风格）改造过**。这对后续静态分析是个警告：`qingnse64.dll` 的调用图不可全信。**`qingshellext64.dll` 与 `ksyncengine.dll` 无此段。**
- **`HKLM\…\Shell Extensions\Approved` 里无 WPS 条目**。

---

## 8. 与钉盘 CfApi 路线的差异（P2-8）

**先说共同点**（两者都走 CfApi，都属「Windows 云文件」范式）：都建占位符、都有 fetch 回调、都有 pin/unpin、都在远端改动时只更元数据。**下面是真正的差异，逐条有证据。**

| # | 维度 | 钉盘（`sync_frame.dll`） | WPS 云盘（`ksyncengine.dll`） | 证据 |
| --- | --- | --- | --- | --- |
| 1 | **CfApi 符号可见性** | 导入名直接出现在串扫描里（`CfCreatePlaceholders` / `CfConvertToPlaceholder` / `CfUpdatePlaceholder` / `CfGetPlaceholderInfo` 等 8 个），扩展侧 `SyncContextMenu` 是**薄壳** | **PE 导入表里一个 CfApi 都没有**；16 个函数名以**名表 + `LoadLibraryW`/`GetProcAddress`** 动态解析；且 `KSOLITE.DLL`/`KRT.DLL`/`QT5COREKSO.DLL` 三个引擎依赖是私有库 | 【S】`peimp.py` 对钉盘-era 描述 vs 本轮对 ksyncengine 的导入表解析；`0x000e756c`/`0x000e757c`/`0x000e7598` 字面量 |
| 2 | **引擎进程形态** | 单进程同步服务（`DingpanSyncService.exe` 2 MB） | **x86 32 位 DLL 挂在 WPS 主程序生态里**（`ksyncengine.dll` 是 PE32，且另有 `wpscloudsvr.exe` 977 KB 同域）——**我们若要复刻，CfApi DLL 可以是 64 位，但要注意 32 位宿主无法加载 64 位 `cldapi` 以外的扩展** | 【R】`ev_58891b06…` 报 `architecture: x86`；`wpscloudsvr.exe` PE32 |
| 3 | **远端模型的增量机制** | **`spaceVersion` + `listRedologs` 游标式增量**（§4 of 钉盘文档），带 `nextToken` 与全量兜底 | **本地 `data_ver_table` 只看缓存 schema 版本**，另有一整套**事件驱动任务谱系**（`FullSyncTask` / `FileMonSyncTask` / `BrowseSyncTask` / `NotifySyncTask` / `WpsSaveSyncTask` + `VfsDiffMaker`/`FolderDiffMaker`/`FileDiffMaker` 三级差异引擎）。**没有观察到 redolog 式远端游标** | 【D】`data_ver_table` 5 行；【S】§6 任务谱系全量 |
| 4 | **占位符状态的三层分离** | 主要是 **pin/unpin 两档**（`cfp_state_set_pinned` / `fuse_unpin_materialized_item`） | **pin state + in-sync state + custom state 三层**，各有独立封装函数（`setPinStateByPath`/`setPinStateUnspecified`/`setExcludedFile`、`setInSyncState`、`appendCustomStateToFile`/`clearCustomStateFromFile`），并有专门的**状态修复 handler**（`KCfApiSyncFolderRepairStateHandler` + `cfapi state not in sync` 检测） | 【S】§3.1 / §3.4 函数名与日志 |
| 5 | **「释放空间」是一等任务** | `on_notify_dehydrate` / `free_space_and_update_place_holder` | **独立的差分类别 + 独立任务 + 独立 handler + 重试**：`_judgeFreeUp` → `has free up file state` → `retry free up` → `KCfApiSyncFolderFreeUpHandler`。**WPS 把"释放"提升到与"新建/修改/移动"同级的事件类型** | 【S】§3.1 / §6 |
| 6 | **读取节流（有无/粗细）** | 未在钉盘文档中出现 | **明文的进程名单 + 智能限流器**（`KSyncEngineHydrationSmartLimiter`，29 个进程名两段拼贴 + `limite %1 read until %2`） | 【S】§3.5 |
| 7 | **形态观感：文件夹算不算"同步文件夹"** | 钉盘是**空间订阅**（`remote_folder_sync_status.depth`） | WPS 同时存在**两条并行 VFS 路线**：`KSyncEngineSyncFolderVfs`（普通同步文件夹）**与** `KSyncEngineCfApiSyncFolderVfs`（CfApi），由 `useCfApiSyncFolder` 开关切换 | 【S】`0x000d6320 useCfApiSyncFolder`；§2.2 两个 `*SyncFolderVfs` 类并存 |
| 8 | **内容哈希** | `md5` + `md5_reliable`（承认哈希可能不可信） | **`fsha1`**（`file_metadata_table_drive.extraInfo` 里实测，124/124 行都有） | 【D】§5.4 |
| 9 | **注册表痕迹** | 文档未记录 `SyncRootManager` 条目 | `SyncRootManager\WPSCloudProvider!<SID>!<uid>!<企业名>`，含 `Provider`/`Flags=2`/`IconResource`/`DisplayNameResource`/`ThumbnailProvider` 五值 + `UserSyncRoots` 子键；另有配套设置项 `hideDriveCfMenuStatus`/`hideDriveCfMenuEnabled`/`enableDriveThumbnail` | 【D】只读注册表；【S】值名列表 |

**一句话差异**：**钉盘把 CfApi 当成一个"下载器"接进来（薄封装 + 增量协议是主体）；WPS 把 CfApi 当成一套"文件系统语义"来做（三层状态 + 差异引擎 + 释放=一等事件 + 节流器），代价是符号全隐藏、依赖私有 Qt 库。我们要的只读原型，抄 WPS 的 §3.2 回调骨架 + 钉盘的 §4 增量协议，是最省的组合。**

---

## 9. 实现只读占位符原型的最小闭包

给「没碰过 CfApi 的工程师」的落地清单。**每一步都标注了本文对应证据。**

**第 1 步：注册同步根（一次性）**
1. 生成实例 GUID 与显示名。
2. 填 `CF_SYNC_REGISTRATION { ProviderName, ProviderVersion, SyncRootIdentity, SyncRootIdentityLength, FileIdentity, FileIdentityLength, FileIdentityInSync }`。
3. 填 `CF_SYNC_POLICIES`（`Hydration.Primary` / `Population` / `InSync` 三组策略）。
4. `CfRegisterSyncRoot(path, &reg, &pol, CF_REGISTER_FLAG_NONE)`。
5. 失败分支照抄 WPS 的检测：**「该路径已被别的同步根注册」要单独识别并报错**（`%1 has register by %2` / `%1 %2 has register with other syncfolder %3`）。

**第 2 步：连接并注册回调（进程生命周期）**
6. `CfConnectSyncRoot(path, &callbacks[0], NULL, CF_CONNECT_FLAG_NONE, &connectionKey)`。
   - **连接前先做环境闸门**（WPS 的 `isSystemEnvCompatibleWpsDrive`）：检查 `cldapi.dll` 可加载 + OS 版本。
7. **`CF_CALLBACK_REGISTRATION` 最小表 = 2 项 + 终止项**：
   ```c
   { CF_CALLBACK_TYPE_FETCH_DATA,        fetchDataCallback },
   { CF_CALLBACK_TYPE_CANCEL_FETCH_DATA, cancelFetchCallback },  // 可不实现，但槽要留
   { CF_CALLBACK_TYPE_NONE,              NULL }                  // 必须的终止项
   ```
   （WPS 的表更大，含 NOTIFY_DEHYDRATE / NOTIFY_RENAME / NOTIFY_MOVE / NOTIFY_DELETE，见 §3.2。）

**第 3 步：fetch 回调（核心，"点开才下载"）**
8. 回调里 `CfGetTransferKey` / `CfOpenFileWithOplock` 拿句柄——**这就是 WPS 名表里那两个「多余」函数**。
9. `CfExecute(CF_OPERATION_TYPE_TRANSFER_DATA)` 分块回报（或 `CfExecute(CF_OPERATION_TYPE_TRANSFER_PLACEHOLDERS)` 批量）。
10. `CfReportProviderProgress(connectionKey, transferKey, total, completed)` 报进度——**另一个「多余」函数**。
11. `CfExecute(CF_OPERATION_TYPE_TRANSFER_DATA)` 完成后 **`download %1 fileSize not equal with placeholder %2` 式的尺寸对账必须做**（WPS 有这个分支）。

**第 4 步：建占位符（列表阶段，零下载）**
12. 对每个云端条目填 `CF_PLACEHOLDER_CREATE_INFO`：
    ```
    RelativeFileName  = 本地名（注意与云端名可能不同，见 §3.3）
    FileSize          = 云端真实大小（不是 0！）
    LastWriteTime     = 云端 mtime
    FileIdentity      = 32 字节本地代理 ID（= fs_metadata_table.id 的做法）
    FileIdentityLength= 32
    FsMetadata        = NULL       ← WPS 134/134 行 extraInfo 全空，证明它不用
    Flags             = CF_PLACEHOLDER_CREATE_FLAG_NONE
    ```
13. `CfCreatePlaceholders(rootPath, &infos[0], count, CF_CREATE_FLAG_NONE, &processed)`，返回后**逐个检查 `infos[i].Result`**。

**第 5 步：远端改动只更元数据**
14. `CfGetPlaceholderInfo` 拿现有信息 → 改 `FileSize` / `LastWriteTime` / `FileIdentity` → `CfUpdatePlaceholder`（`CF_UPDATE_FLAG_…`）。**绝不触发下载**（对应 `_handleCloudModifyTask` → `KCfApiSyncFolderUpdatePlaceholderTask`）。

**第 6 步：pin / 释放**
15. 「始终保留在这台电脑上」→ `CfSetPinState(handle, CF_PIN_STATE_PINNED, CF_SET_PIN_FLAG_NONE, NULL)`（WPS 的 `setPinStateByPath`）。
16. 撤销 → `CfSetPinState(..., CF_PIN_STATE_UNSPECIFIED, ...)`（WPS 的 `setPinStateUnspecified`）——**不要用 `UNPINNED`**（§3.1）。
17. 「释放空间」→ `CfDehydratePlaceholder(handle, 0, fileSize, CF_DEHYDRATE_FLAG_NONE, NULL)`（WPS 的 `dehydratePlaceholder`，注意它先 `GetFileSize`）。

**第 6b 步：覆盖图标——不用做（§4.4 的净简化）**
18. ~~注册 `IShellIconOverlayIdentifier`~~ **不需要**。实测 WPS 未在 `ShellIconOverlayIdentifiers` 注册任何项（那 6 个 `.WorkspaceExt*` 全属百度网盘）——**CfApi 的占位符状态由 Windows 自己画图标**。这一步从最小闭包里删除。

**第 7 步：本地数据模型（照抄 §5.1）**
18. SQLite 建 `fs_metadata_table(id PK 32hex, parentId, fsFileId BIGINT, cloudFileId TEXT, fileName, localName, type, fileSize, createdTime, modifiedTime, state, extraInfo, keepOnThisDeviceState, activeState, volSerial)` + **三个索引（parentId / fsFileId / cloudFileId）**——WPS 三个都建了，说明三种查询都是热的。
19. 另建 `recyclebin_metadata_table`，**保留 `parentId`（云端父）与 `localParentId`（本地父）双列**（§5.5）。
20. 元数据扩展用 **KV 串列（`extraInfo`）** 而不是加列（§5.4），分隔符用 U+2029/U+2028 以免与文件名冲突。

**第 8 步（强烈建议）：读取节流**
21. 抄 §3.5 的两段进程名单 + 一个简单的「同进程连续读 → 降速」限流器。**不做这步，第一次打开目录会被索引器和杀软打爆。**

**评分口径**：做完 1–7，就是一个**能跑、能看、能点开**的只读占位符原型；加上第 8 步才具备「装到用户机上不出洋相」的素质。

> 2026-10-16 修订：第 6b 步（覆盖图标）**已从闭包中删除**（§4.4 实测 WPS 不注册覆盖图标处理器）。第 6 步的 `Flags` 取值请以 §3.3b 的**实测值**（`MARK_IN_SYNC` / 目录加 `DISABLE_ON_DEMAND_POPULATION`）为准，不要用本节的 `CF_PLACEHOLDER_CREATE_FLAG_NONE`。

---

## 10. 与 EasyShare 现状的差距清单

| 能力 | WPS 云盘（本文证据） | 钉盘（对照） | EasyShare 现状 | 结论 |
| --- | --- | --- | --- | --- |
| 桌面形态 | **CfApi 同步根**（`SyncRootManager` 五值 + `UserSyncRoots`） | CfApi 占位符 | 注册表 Shell NameSpace 指 UNC（`internal/namespace/namespace_windows.go:61-66`） | **缺** |
| 占位符创建 | `CfCreatePlaceholders` + `FileIdentity`=32hex 代理 ID | `CfCreatePlaceholders` | 无 | **缺** |
| 按需下载回调 | `cfApiFetchDataCallback` + `CfExecute` + `CfReportProviderProgress` | `on_fetch_placeholders` | 无（WebDAV 全量物化） | **缺** |
| 元数据只更不下载 | `_handleCloudModifyTask` → `UpdatePlaceholderTask` | `cloud modify with local is placeholder, update local size and modify time` | 无 | **缺** |
| 三层状态 | pin + in-sync + custom state（各独立封装） | 主要 pin/unpin | 无 | **缺** |
| 释放空间 | 独立事件类别 + handler + 重试 | `on_notify_dehydrate` | 无 | **缺** |
| 状态自愈 | `RepairStateHandler` + `cfapi state not in sync` 检测 | `fix_*_dirty_data` 三类 | 无 | **缺** |
| NTFS 身份绑定 | `fsFileId`（真 NTFS FileId）+ `cloudFileId` + `volSerial` 消歧 + 三索引 | `local_id` / `remote_id` | `es_file` 唯一键含 `file_path` | **半缺** |
| 回收站 | `recyclebin_metadata_table`（双 parentId）+ `to_delete_files` 待删队列 | 本地进系统回收站 + 远端历史 | 物理删除 | **缺** |
| 内容哈希 | **`fsha1`**（`extraInfo` 内） | `md5` + `md5_reliable` | 全仓无文件级内容哈希 | **缺** |
| 远端改名/移动 | `_handleCloudRenameTask` / `_handleCloudMoveTask` / `_handleCloudCrossMoveTask` | `renameDentry` / `moveDentry` | WebDAV 改名 = 下+上+删 | **缺** |
| 版本对账 | `file_version_state_table`（local/cloud/target 三值 + action + sources 位集） | `dentryVersion` + `preDentryVersion` CAS | 无 version 列 | **缺** |
| 元数据扩展 | `extraInfo` KV 串（不改 schema） | 加列 | 加列需改表 | **缺（手法可抄）** |
| 读取节流 | 29 进程名单 + `HydrationSmartLimiter` | 未观察到 | 无 | **缺（易被忽略）** |
| 同步根冲突检测 | `has register by` / `has register with other syncfolder` | 未观察到 | 无 | **缺** |

---

## 11. 仍未取到的证据

按价值排序，全部标注卡在哪一步：

1. ~~`CfConnectSyncRoot` 的回调表完整枚举~~ → **2026-10-16 已闭合**（[§2.1b (3)](…#21b-2026-10-16-闭合动态解析机制与回调表全量g反编译到脚)）：**5 项 + 终止项**，逐项带类型号与回调地址；两种独立方法（读调用点传的地址 + 全 `.rdata` 盲扫）互证，且**全库只有这一张表**。
   附带修正：**没有 `NOTIFY_RENAME` / `NOTIFY_MOVE` / `NOTIFY_FILE_CLOSE_COMPLETION`**（§3.2 的推断表据此订正，见 §3.2b）——改名/移动走**另一条路径**；**该路径已于同日定位**（§3.3c：自己的 `ReadDirectoryChangesW` 目录监视器 + `*RenameTask`/`*MoveTask` 族），非欠账。

2. ~~`CF_PLACEHOLDER_CREATE_INFO` 各字段的实际赋值（尤其 `FsMetadata` 是否真的 NULL）~~ → **2026-10-16 已闭合**（[§3.3b](#33b-2026-10-16-闭合cf_placeholder_create_info-的真实填充g反编译到脚)）：填充代码已读到；`FsMetadata` 确实**未被使用**（推断正确）；**但 `Flags` 与 `FileAttributes` 两处推断错误已更正**。

