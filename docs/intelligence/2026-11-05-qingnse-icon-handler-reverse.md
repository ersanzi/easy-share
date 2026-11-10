# qingnse64.dll 在线文档图标机制取证 — IconHandler / 双重扩展名 / 状态外显真相

日期：2026-11-05 　目标：WPS Office `12.1.0.29161\office6\qingnse64.dll`（**新构建**，见 §0）
承接：[`2026-10-15-wps-cfapi-placeholder-reverse.md`](2026-10-15-wps-cfapi-placeholder-reverse.md) §4.3 欠账（「自定义状态位 → 图标资源 id」映射）

## 0. 目标已变：WPS 在 11-03 自更新了 UI 侧二进制

原 10-15 文档记录的 `qingnse64.dll` SHA-256 为 `f8d4c657…`。**2026-11-03 该文件被 WPS 自更新替换**，新值：

| 产物 | 大小 | SHA-256（2026-11-05 实测） | 修改时间 |
| --- | --- | --- | --- |
| `qingnse64.dll` | 16 716 280 | `e5b4e439514b28d11c9b587d5b562ad6f01264aaf49a35da36ab34f8e8836185` | 2026-11-03 |
| `qingnse.dll` | — | `ec01877ee9b0fcf3…`（同日更新） | 2026-11-03 |
| `ksyncengine.dll` | 1 246 200 | `8747df16…`（**未变**，10-15 记录仍有效） | 2026-10-14 |
| `qingshellext64.dll` / `wpscloudsvr.exe` | — | 未变 | 2026-10-14 |

即：**同步引擎没动，UI 侧（图标/缩略图/Shell 集成）动了**——本轮全部结论针对当前在用的新构建，比补旧欠账更有价值。取证通路为自建持久化 Ghidra 工程（172 MB，导入+全量分析约 7 分钟）+ [`scripts/reverse/`](../../scripts/reverse/) 脚本定向查询（每轮 20–60 s），级别一律 **【G】**；另对关键函数出一枚 **【R】**（§5）。

## 1. 一句话结论

**「自定义状态位 → 图标」的映射不存在。** WPS 在线文档的 Explorer 图标走的是**文件扩展名关联 + IconHandler COM** 的组合：

1. 在线文档占位符的文件名是**双重扩展名** `真实文件名.真实扩展名.wpsonline`（如 `报告.doc.wpsonline`）；
2. ProgID `wpsonline` 下注册了 **IconHandler**（`HKCR\wpsonline\shellex\IconHandler` = `{FC397F71-A3E3-4198-86FB-39B7267E5E8C}`，即 10-15 文档登记的「WPS在线文档图标」CLSID，类名 `KWpsDrvieIconHandler`——"Drvie" 是 WPS 自己拼错的，照抄）；
3. IconHandler 的 `GetIconLocation` **剥掉 `.wpsonline`、取出真实扩展名**，查一张全局缓存表（`std::map<wstring, pair<wstring,int>>`，扩展名 → {图标文件路径, 图标索引}）返回给 Explorer——**在线文档显示的是真实文档类型（.doc/.ppt…）的系统图标**；
4. 业务状态（上传失败/空间满/在线文档）**不是**每文件图标位——10-15 文档 §3.4 记录的 custom state 取值承载这些语义，但资源管理器**不为它画独立覆盖图标**：标准同步态（in-sync/pinned）由 Windows 自带渲染（10-15 §4.4 已证无覆盖图标注册），「在线文档」这一态靠**扩展名本身**表达。

> 对 10-15 §4.3 欠账的处置：**问题本身被证伪**。「7 个自定义状态 → 15 个 PNG」的猜测不成立——15 个 PNG 在 `qingshellext64.dll`（缩略图提供者用），与状态映射无关；`WpsDriveThumbnailProvider` 是缩略图引擎（`KThumbnailSupporter::GetThumbnail*`，本轮只定位到日志锚点，未深挖），不承担状态图标。

## 2. 证据链（全部【G】，伪码在 output/probe_qingnse_r*.txt）

### 2.1 注册面：IconHandler 写进 `wpsonline` ProgID

`FUN_180443820`（自注册函数）内两条字面量（【S】+【G】双重确认）：

```
"Software\\Classes\\wpsonline\\shellex\\IconHandler"
"{FC397F71-A3E3-4198-86FB-39B7267E5E8C}"
```

同 DLL 还有 `FUN_1804085f0 / FUN_1804087d0 / FUN_180408960 / FUN_180408870` 一族写 `shellex\IconHandler` 的兄弟注册函数（多 ProgID 复用同一 CLSID）。`.wpsonline` 扩展与 `wpsonline` ProgID 名串同区共存（`0x180778888` / `0x180781b38`）。

### 2.2 GetIconLocation：双重扩展名剥壳 + 系统图标缓存

`FUN_180408960` = `KWpsDrvieIconHandler::GetIconLocation`（函数体内自带日志标签），流程：

1. 若对象上已缓存路径（`this+0x10`）非空 → 直接用，输出 flags `0x10`；
2. 否则 `FUN_180408690`（**扩展名解析**）：从路径**尾部找最后一个 `.`，再往前找上一个 `.`，取两段之间的子串**——对 `报告.doc.wpsonline` 恰好剥出 `doc`；
3. `FUN_18040af40`：查**单例缓存表**（RTTI 全名在案：`std::map<std::wstring, std::pair<std::wstring, int>>`——扩展名 → {图标文件路径, 图标索引}，lambda 初始化、双重检锁），未命中则计算并回填；
4. 组装输出（图标文件路径 + 索引），失败分支日志 `file not exsit %s` / `get iconinfo error` / `image res path is too long`（拼写照抄）；
5. `FUN_180408870` = `Extract`：从解析出的图标文件按大小位提取 HICON，无徽章绘制逻辑。

配套独立小函数 `FUN_18040c740` = `wpsdrive::getFileIconIndex`（空扩展名日志 `fileExt is empty`）：

```c
SHGetFileInfoW(fileExt, 0x80 /*FILE_ATTRIBUTE_NORMAL*/, &sfi, 0x2b8, 0x4010
               /* SHGFI_SYSICONINDEX | SHGFI_ICONLOCATION */);
return sfi.iIcon;   // 系统镜像列表的扩展名图标索引
```

### 2.3 UI 侧读占位符的协议（读侧补全）

`qingnse64.dll` 动态解析 **`CfGetPlaceholderInfo`**（thunk `FUN_1804a6180`，`GetProcAddress` + init-once 双检锁），**7 个调用者**（`output/callers_cfpinfo.txt`）：

| 调用者 | 行为（反编译） |
| --- | --- |
| `FUN_180175c80` | `CreateFileW(0, share=0, OPEN_EXISTING, 0x2000010)` → `CfGetPlaceholderInfo` → **「是不是占位符」布尔判定** |
| `FUN_180175d60` | 同上取 info 后，把 **FileIdentity 字符串与魔数 `COMPANY_ROOT_FILE_ID`（20 字符，长度 0x14 精确比对）** 比较——**同步根/企业根特殊节点用固定 FileIdentity 串标识** |
| 其余 5 个 | 各 UI 路径（缩略图/菜单/属性）的占位符信息读取 |

**新事实**：10-15 文档说 FileIdentity = 32 字符十六进制代理 ID（`fs_metadata_table.id`，由同步引擎生成）。本轮补充：**UI 侧约定了一个保留魔数串**——引擎给「企业云盘根」这种特殊节点发的 FileIdentity 不是随机 32 hex，而是字面量 **`COMPANY_ROOT_FILE_ID`**（20 字符 UTF-16，常量区 `0x18073b9d0` 逐字节读出，NUL 补齐）。实现我们的同步根时若要 UI 侧识别根节点，这是一条现成协议：FileIdentity 自描述、不依赖路径。

另：`wps::qing::GetOnlineFileConfigResult`（RTTI 在案）+ `KCloudProxyPrivate::GetOnlineFileConfig` 日志锚点——在线文档特性由**云端配置**开关（服务端下发），不是写死的。

## 3. 对 EasyShare 的反哺结论

1. **我们不需要为「状态图标」写任何 Shell 扩展**（强化 10-15 §4.4 的净简化）：同步态由 Windows 渲染；「在线/不可下载」这类**文件级恒定属性**用**扩展名后缀 + ProgID 图标关联**表达即可（甚至可以只挂 `DefaultIcon`，不需要 IconHandler——WPS 用 IconHandler 是为了剥双重扩展名显示真实类型图标）。
2. **双重扩展名方案可抄**：`<真实名>.<真实ext>.cfplaceholder` 这类命名让资源管理器「显示什么图标」与「占位符身份」解耦——真实类型图标照常出现，我们的扩展名只做标记。代价是用户可见的文件名多一段后缀，**产品上未必可接受**（OneDrive/钉盘都不加后缀；它们靠 SyncRootManager 的 ThumbnailProvider/属性页表达差异）。拍板建议：**短期不做**，占位符保持真实文件名；把「双重扩展名 + IconHandler」记为可选方案。
3. **`COMPANY_ROOT_FILE_ID` 魔数**：若将来在 UI/Shell 侧要判「这是企业根节点」，用 FileIdentity 魔数比对是 WPS 的现成协议，比逐路径判断稳（路径会改，FileIdentity 不变）。
4. **在线文档特性是云端配置下发**（`GetOnlineFileConfig`）：我们对应的能力开关（如「此空间是否启用占位符形态」）也应走控制面配置而非客户端写死——与 ADR-0007 的控制面集中管理一致。
5. **`qingshellext64.dll` 的 15 个 PNG** 与状态映射无关（本轮证伪），归属缩略图链路——后续做缩略图时再对标 `KThumbnailSupporter`。

## 4. 本轮未闭合（诚实清单）

1. ~~`COMPANY_ROOT_FILE_ID` 的完整串内容~~ → **已闭合**：字面量 `COMPANY_ROOT_FILE_ID`（常量区逐字节读出）。
2. 缓存表 lambda 的**计算体**未反编译（推断为 SHGetFileInfoW 同款，与 `getFileIconIndex` 互证，标【推】）。
3. `Extract` 的图标文件路径**具体指向哪个 DLL/ico**（qingnse.dll 图标组？）未追到终点——需要反编译缓存表构造侧。
4. `WpsDriveThumbnailProvider` 的缩略图渲染逻辑未深挖（与状态图标问题已解耦，优先级降低）。

## 5. REA Evidence（【R】）

**`ev_1e34a41d0e374e1d8b079ba56e66573063a1a01e29c141437bb8fd82af2d69c3`**（REA 6.8.0 / ghidra 12.1.4）：
`decompile --procedure FUN_180175d60`，subject = `qingnse64.dll` sha256 `e5b4e439…6185`（§0 新构建），伪码含
`COMPANY_ROOT_FILE_ID` 比对分支，与 §2.3【G】结论互证。产物 JSON 存 `/tmp`（会话临时），复现命令：

```powershell
$env:GHIDRA_INSTALL_DIR='D:\Develop\ghidra_12.1.4_PUBLIC'; $env:JAVA_HOME='D:\Develop\java21'
$env:REA_GHIDRA_STARTUP_TIMEOUT_MS='1800000'    # 16.7 MB 目标默认 330 s 不够
rea decompile C:\Users\lishi\AppData\Local\Temp\wpsre2\qingnse64.dll --procedure FUN_180175d60 --provider ghidra --format json
```

**REA 自报 limitations（照抄，不吞）**：
- 会话在 Ghidra 默认自动分析完成后才提供操作；分析未完成不暴露部分结果。
- 导入的 Program 与临时工程是临时的，关闭即删；注释操作只改会话数据库元数据，从不写原始可执行字节。
- 取消活动请求会终止临时数据库并丢弃会话注解。
- 伪码是 Ghidra 反编译器输出，不是原始源码，也不等价于 Hopper 输出。
- 无函数体或外部函数返回 null；其他反编译失败会显式报错。
- Ghidra Listing 报告 `0x180175f1a` 处对 `FUN_1806a36fc` 的 terminal call（hasNoReturn=true）；fallthrough 被 Ghidra 流模型排除，原因未被本观测确立。
- Windows Ghidra P0 只接受 native x86/x86-64/ARMNT/ARM64 PE；managed / hostile / sensitive / mutable-path 目标不支持。

## 6. 复现清单

```powershell
# 工程（一次性，已建）：C:\Users\lishi\AppData\Local\Temp\ghidra_proj\wps_qingnse（172 MB）
$env:JAVA_HOME='D:\Develop\java21'; $env:GHIDRA_INSTALL_DIR='D:\Develop\ghidra_12.1.4_PUBLIC'
& "$env:GHIDRA_INSTALL_DIR\support\analyzeHeadless.bat" `
    C:\Users\lishi\AppData\Local\Temp\ghidra_proj wps_qingnse `
    -process qingnse64.dll -noanalysis -scriptPath scripts\reverse `
    -postScript ProbeStrings.java 'IconHandler' 'FC397F71'   # → output/probe_qingnse_r4.txt
# 各轮输出：output/probe_qingnse_r1..r8.txt、output/callers_cfpinfo.txt
```
