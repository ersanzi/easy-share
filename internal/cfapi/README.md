# cfapi —— Windows Cloud Files（CfApi）最小绑定

把云盘内容以**按需占位符**形态呈现给资源管理器：列表零下载、点开才拉数据。
这是 roadmap [`docs/plans/2026-10-16-drive-production-roadmap.md`](../../docs/plans/2026-10-16-drive-production-roadmap.md)
**切片 5（只读占位符）** 的地基。

## 当前状态

| 部分 | 状态 |
| --- | --- |
| `cldapi.dll` 动态加载（失败可降级） | ✅ 完成 |
| `CF_SYNC_REGISTRATION` / `CF_SYNC_POLICIES` / `CF_PLACEHOLDER_CREATE_INFO` 布局 | ✅ 完成，**有 C 编译器实测断言** |
| `CfRegisterSyncRoot` / `CfUnregisterSyncRoot` | ✅ 完成，**真机往返实测通过** |
| `CfCreatePlaceholders`（含 `PlaceholderSpec` 构造） | ✅ 完成，**真机实测：逻辑大小 12345678 / 磁盘分配 0** |
| `CfConnectSyncRoot` + `FETCH_DATA` 回调（**含 `CfExecute` 回填**） | ✅ 完成，**真机实测：读占位符→回调触发→内容按需取回** |
| `CfDisconnectSyncRoot`、`CANCEL_FETCH_DATA`（空实现，与 WPS 一致） | ✅ 完成 |
| 释放空间（`CfDehydratePlaceholder`）、pin/unpin | ⬜ 未做（属写意图，只读原型不需要） |
| **只读执行**：占位符带 `FILE_ATTRIBUTE_READONLY`（`PlaceholderSpec.ReadOnly`） | ✅ 完成，**真机实测：写入被拒 + 水合仍正常**（KI-14） |
| **读取节流前置**：取发起方进程名（`CF_PROCESS_INFO` + `SetFetchObserver`） | ✅ 完成，**真机实测：进程名与 PID 与本进程一致** |
| **读取节流本体**：进程名单 + 窗口限流器（`limiter_windows.go`） | ✅ 完成，**10 条策略用例 + 1 条端到端；用户路径实测 18ms 未被拖慢** |
| **第 5 步**：`CfUpdatePlaceholder` 元数据更新（`Sync` 可重复的前提） | ✅ 完成，**真机实测：不下载内容、已水合内容完好、只读保留** |
| **只读覆盖范围**（删除/改名/新建是否被挡） | ✅ 已实测并记录边界（`readonly_ops_windows_test.go`）——只读保护**内容**，不保护**目录结构** |
| **幽灵清理**（云端已删 → 本地清理，但**保留已水合的**） | ✅ 完成，**实测：未水合的删、已水合的留（那是本地仅存副本）**（KI-16） |
| **编排层**：`CloudEntry` → `BuildTree`（扁平清单→逐层建树） | ✅ 完成，**真机实测多层树 + 全部零字节** |
| **挂载层**：`MountSpace`（注册→连接→拉清单→建树→按需拉取） | ✅ 完成，**全栈端到端实测** |
| 同步根生命周期与登录态联动（接入 `app.go`） | ⬜ 未做 |

### 全栈端到端已跑通

`TestMountSpaceEndToEnd` 用**模拟云端**（内存里三个文件 + 两个目录）走完整流程：

```
--- PASS: TestMountSpaceEndToEnd
    读前拉取 [docs/guide.md]；guide.md 已水合，readme.txt 磁盘分配 = 0
```

三条断言都成立：

1. **树结构正确且全部零字节**——列出的目录/文件都建出来了，磁盘分配 0；
2. **读文件时内容逐字节等于云端**——`FETCH_DATA` 回调按 `fileId` 反查路径、拉取、回填；
3. **只拉了被读的那个文件**——`opened` 记录只有 `docs/guide.md`，其余文件没产生任何流量。

这合起来就是「生产级形态」的最小可运行证明：**用户看到完整的树，磁盘不占地方，只有真正打开的文件才下载。**

### 已验证到什么程度

`go test ./internal/cfapi/ -v` 的关键输出：

```
--- PASS: TestCreatePlaceholder
    roundtrip_windows_test.go:138: 占位符：Stat 大小=12345678，磁盘分配=0
```

`TestFetchOnRead` 更进一步，把**完整往返**跑通：

```
--- PASS: TestFetchOnRead
    回调触发 1 次；路径=\Users\...\hello.txt；读回 43 字节
```

即**「列表零下载、点开才拉」已在真机完整成立**：

1. 建占位符后，文件在磁盘上看得见、报云端真实大小、**占用 0 字节**；
2. 去读它的内容时，**系统回调我们**（`FETCH_DATA`），我们在回调里回填数据；
3. 读到的内容与我们给回调的字节**逐字节一致**。

> 仍未验证：占位符在资源管理器里的**实际外观**（未做 GUI 观察）、
> 真实云端（控制面 + RustFS）下的端到端（目前回调里给的是测试数据）。

## 实测得到的三个 API 语义（都有测试固化，别凭直觉写）

| 语义 | 实测结论 | 固化用例 |
| --- | --- | --- |
| `RelativeFileName` 能否带路径 | **不能**。传 `"a/b.txt"` 整批失败 `0x8007007B`(INVALID_NAME)。语义是「`rootPath` 决定父目录，本字段只给一个名字」→ **建树必须逐层** | `TestCreateDirectoryPlaceholder` |
| 目录能否建占位符 | **能**。`IsDir=true` + `FileAttributes=DIRECTORY` 可直接建出目录（不必先 `MkdirAll`） | 同上 |
| `CfDisconnectSyncRoot` 的参数 | **按值传 key**，不是指针。传 `&key` 得 `0x80070057`(E_INVALIDARG) | `TestMountSpaceEndToEnd` 的 `Close` |

> 第三条是真实踩坑：初版写成取地址，被端到端用例的 `Close` 报错抓出来。
> 教训是**光看函数名不够，要看声明是 `CF_CONNECTION_KEY` 还是 `CF_CONNECTION_KEY*`**。

## 已知局限：回调是同步的（**措辞已按实测收窄**）

当前 `Fetcher` 是**同步**接口，取数据在**系统回调线程**上完成，回调返回时工作已做完。
WPS 不是这么做：它的 fetch 回调只造一个 JobContext，用 Qt 队列连接派发给业务线程
**立刻返回**（取证 §2.1b (4)）。

**关于「同步会不会撞 I/O 超时」——我原先写的强表述经实测不成立**，现已收窄：

| 延迟（在回调线程上睡） | 实测结果 |
| --- | --- |
| 5 秒 | ✅ 成功（5.017s） |
| 20 秒 | ✅ 成功（20.005s） |

复现：`CFAPI_SLOW_SEC=20 go test ./internal/cfapi/ -run TestSyncFetchSlow -v`

**所以 20 秒以内没有观察到超时失败**。仍成立的是两条更弱但确定的判断：

1. **回调线程被占住期间无法处理其他请求** → 并发度受限于系统给的回调线程数。
   这是结构性的，与超时无关。
2. **等待时长没有上限保证**。我们只测到 20 秒仍然安全，**没有探到真实阈值**，
   而真实云端拉 GB 级文件可能远超这个量级（尤其在缺陷 1「每次读整份对象」未修时，
   `internal/cfmount` 会先把整个文件下下来）。

| | 同步（当前） | 异步（WPS） |
| --- | --- | --- |
| 回调线程占用 | 整个取数期间 | 仅入队瞬间 |
| 并发度 | 受回调线程数限制 | 由业务线程池决定 |
| 超时风险 | **20s 内未观察到**；更长未知 | 不受影响 |

**结论**：同步模型**当前可用**（含 20 秒级慢取数），不必为「可能超时」而阻塞接入；
但**接入真实云端时应先跑一次大文件实测**，据实测决定是否必须改异步。
不要基于本文原先把「同步有超时风险」当既成事实。

### 异步方案：已尝试，**未完成**（代码在 `experimental/`）

改法思路清楚（fetcher 改为受理式、回调立即返回、后台取数后再回填）。
一次「延迟 3 秒」的用例跑通过（`TestAsyncFetchSlow`），但其余用例全部失败/挂起：

| 用例 | 结果 |
| --- | --- |
| `TestAsyncFetchSlow`（延迟 3s，单块） | ✅ 成功过一次 |
| 多块回填 | ❌ `0x8007017C`（ERROR_CLOUD_FILE_INVALID_REQUEST） |
| 错误上报 | ❌ 读取永久挂起 |
| 时序探针（延迟 0 / 50ms / 500ms） | ❌ **三个延迟全部挂起** |

**关于原因，目前把握不足，不采信任何解释**：
我曾提出「回调返回到 CfExecute 的间隔太短」的假说，并写探针验证——
**500ms 也失败，假说被证伪**。而那个唯一成功的用例只跑过一次、未重复验证，
因此连「异步可行」本身都还不能下结论。

**已知的两条错路**（有实测支撑，可以确定的是这两条不通）：
1. 回调返回后**直接**用回调里的 key 回填 → `0x8007017C`；
2. 回调里 `CfOpenFileWithOplock` 取句柄、返回后再用 → **读取永久挂起**
   （oplock 与触发本次读取的句柄互锁）。

**还有一条运行期危害**（不只是清理不便）：卡住的测试进程
**连 `TerminateProcess` 都杀不掉**（Win32 错误 5），因为它驻留在**内核态的
CfApi 调用**里。它占着临时目录，且 `defer`/cleanup 都不会跑。
**结论**：把不可控时长的等待放进 CfApi 回调链是危险的——同步模型的前提是
「取数一定很快返回」，而真实云端不保证这一点。

**决定**：把异步代码移入 [`experimental/`](experimental/README.md)（`*.go.txt` 后缀，
不参与构建）。同步版本已在真机验证可用，是当前应当发布的状态；
异步要**先补实验**把「什么条件下能稳定成功」测清楚，再谈合入。

## 为什么全部走动态加载

`cldapi.dll` 随 Win10 1709+ 提供。静态导入会让旧系统**直接启动失败**；动态加载
则能「加载不到就降级回 WebDAV」。`Available()` 就是给上层做这个判断用的。

WPS 云盘的做法相同：它的 `ksyncengine.dll` 里**没有** CfApi 的 PE 导入，
而是 `LoadLibraryW(L"\\cldapi.dll")` + 每函数一个 `GetProcAddress` thunk
（取证 §2.1b）。

## 结构体布局：不要手写，也不要手算

CfApi 是 FFI 边界。**字段顺序或填充错一格，内核会读到垃圾指针**，表现为随机崩溃
而不是干净报错。本项目在这上面踩过一次：初版手写了填充字段，等价于重复填充。

现在的纪律是：

1. **照 Windows SDK 的 `cfapi.h` 逐字段誊写**（本机 `10.0.26100.0`），
   不凭记忆构造；
2. **不手写填充字段**——Go 会按 C 规则自动插入；
3. **用 C 编译器取期望值**，而不是手算。`testdata/layout_check.c` 是自包含的
   （不 include SDK 头，避免依赖 Windows Kits 安装），产物值抄进
   `layout_windows_test.go` 的常量。

复现（本机 MinGW）：

```bash
D:/Develop/mingw64/bin/gcc.exe internal/cfapi/testdata/layout_check.c -o /tmp/x && /tmp/x
```

实测（x86_64）：

```
ptr=8
FILE_BASIC_INFO size=40 FileAttributes=32
CF_FS_METADATA size=48 FileSize=40
CF_SYNC_REGISTRATION size=72 ProviderName=8 ProviderId=52
CF_SYNC_POLICIES size=24 Hydration=4 InSync=12
CF_PLACEHOLDER_CREATE_INFO size=88 FsMetadata=8 FileIdentity=56 Flags=68 Result=72 CreateUsn=80
```

> ⚠️ **本机无 32 位 multilib**（`gcc -m32` 链接失败），因此测试里**只断言 64 位**，
> 32 位用例会 skip。需要 32 位支持时应先补一次编译实测。

**注意一个反直觉点**：`FileSize` 与时间戳**不在** `CF_PLACEHOLDER_CREATE_INFO` 的直接
字段里，而是嵌在 `CF_FS_METADATA.FsMetadata` → `FILE_BASIC_INFO` 内。WPS 的填充代码
印证了这一点（它先填 `CF_FS_METADATA` 再整体传入）。

## 证据来源

本包的取值不是猜的，全部来自两份取证：

| 取值 | 来源 |
| --- | --- |
| `cldapi.dll` 动态加载 | 取证 §2.1b（反编译到 `LoadLibraryW` + `GetProcAddress` thunk） |
| 回调类型编号（0/2/6/9/11 + 终止项） | 取证 §2.1b（`.rdata` 回调表实测，两法互证） |
| 连接标志 `6`（`REQUIRE_PROCESS_INFO \| REQUIRE_FULL_FILE_PATH`） | 取证 §3.2b |
| `Flags` = `MARK_IN_SYNC`（目录加 `DISABLE_ON_DEMAND_POPULATION`） | 取证 §3.3b（反编译 `cfapiutil::createPlaceholder`） |
| `FileAttributes` = 目录 `0x10` / 文件 `0x80` | 取证 §3.3b |
| `FsMetadata` 不被使用 | 取证 §3.3b + `extraInfo` 134/134 全空 |
| FILETIME 纪元差 `116444736000000000` | 取证 §3.3b（立即数 `0x19db1ded53e8000`） |

⚠️ **验证边界**：注册/注销、占位符创建、`CfConnectSyncRoot` + fetch 回调
**均已在真机跑通**（见本文顶部的状态表）。仍未验证的是**资源管理器里的实际外观**
（未做 GUI 观察）与**真实云端**下的端到端。
另外全部取值来自**静态取证**（反编译 WPS），运行时行为只覆盖了本包自己发起的调用——
**没有观察过 WPS 自己的运行时行为**。

## 读取节流：进程名拿到了（2026-10-16）

回调接上后**本机所有程序**读该目录都会触发下载——索引器与杀软会无差别扫盘。
WPS 为此做了一份进程名单 + 智能限流（取证 §3.5），前置条件是在
`CfConnectSyncRoot` 时置 `CF_CONNECT_FLAG_REQUIRE_PROCESS_INFO`（本包已置）。

原先这个标志**只是置了、没人读**：`CallbackInfo.ProcessInfo` 被声明为占位指针
却从未解引用。现已补上：

- `ProcessInfo` 结构（`CF_PROCESS_INFO`）；
- `processOf(info)` 取**进程名 basename + PID**，填进 `FetchRequest.ProcessName/ProcessId`；
- `Mount.SetFetchObserver` —— 在真实回调线程上观察每次取数（诊断与单测用）；
- 实测 `TestFetchReportsRequestingProcess`：回调报告的进程名与 PID
  **与本测试进程一致**（`cfapi.test.exe` / 真实 PID）。

### ⚠️ 一次真实的 FFI 翻车（值得单独记）

`CF_PROCESS_INFO` 的 Go 结构**第一版是凭记忆写的**，而且写错了：

| | 我凭记忆写的 | 真实 `cfapi.h` |
| --- | --- | --- |
| size | 32 | **48** |
| `ProcessId` | off 24 | **off 4** |
| 进程名 | `ProcessName` off 16 | **不存在该字段**；名字在 off 8 的 `ImagePath`（**全路径**） |
| 其它 | 有个 `Flags` | **没有 `Flags`**；另有 `PackageName`/`ApplicationId`/`CommandLine`/`SessionId` |

真机跑出来的现象很有代表性：**进程名是字面量 `"UNKNOWN"`、PID 是 398xxxxxxx 这种垃圾值**
——**不是崩溃、不是空值，而是"看起来有值"**。这类错误最难发现：
如果只断言「拿到了非空字符串」，它会**通过**。

修正方式正是本包开头那条纪律的延伸：**不凭记忆写布局**。
先对照
`Windows Kits\10\Include\<ver>\um\cfapi.h` 逐字段誊写，
再让 `testdata/layout_check.c` 用真实 C 编译器算出期望值，
最后在 `callback_layout_windows_test.go` 里断言。

> 教训：**「置了标志」不等于「读得到数据」**。标志只表示同意接收，
> 结构体是否被填充、偏移是否对得上，只能靠一次真实调用验证。
> 而验证不能只断言「非空」——要断言**值本身是对的**（这里是与本进程镜像名比对）。
## 幽灵清理：云端删了，本地也要删——但不能无脑删（KI-16）

`BuildTree` 只做「创建 / 更新」，**没有删除逻辑**。这在 `Sync` 只能跑一次时摸不到；
`Sync` 修成可重复（见上「已解」一节）后**立刻暴露**：
云端删掉的文件会**永久**留在本地，点开报错、每次 Sync 都不清理。

### 修复前先测了「幽灵里有什么」——结果决定修法

```
云端已删，本地仍持有 34 字节（磁盘分配 34）
```

**已水合**的幽灵在本地持有**完整内容**，而云端已经删了——
那是**本地仅存的一份**。无脑清理就是销毁用户数据，且用户毫无察觉。

### 修法：只删未水合的

| 情形 | 处理 | 理由 |
| --- | --- | --- |
| 未水合幽灵（磁盘分配 0） | **删除** | 不持有任何字节，删了不丢数据 |
| 已水合幽灵（磁盘分配 > 0） | **保留** + 计入 `TreeStats.SkippedHydrated` | 内容是本地仅存，删了就是真丢 |

判据 `GetCompressedFileSize > 0`；**查询失败按「有水合内容」处理（保守不删）**——
代价不对称：少删一个幽灵只是碍眼，误删一个水合文件是真丢数据。

清理在 `BuildTree` **之后**（反过来会把本轮刚建出来的也当幽灵删掉）。

### 仍未做

1. **已水合幽灵没有用户提示**：现在只是保留，没告诉用户
   「云端版本已删，本地这份是仅存的」。接入时应提示。
2. **目录幽灵的观感未做 GUI 观察**：目录里若全是保留的水合幽灵，
   递归清理不会删它们，目录因此留下。行为可取，但确认过观感才算完成。
## 读取节流：限流器已落地（2026-10-16）
### 一个自伤的教训：**别用墙钟耗时断言逻辑**

本包最初那条端到端用例写的是：

```go
start := time.Now()
os.ReadFile(full)
if time.Since(start) > 100*time.Millisecond { t.Errorf("疑似被限流") }
```

**这是错的**，而且错得很典型：它把「机器忙不忙」混进了「限流器判得对不对」。
并行跑四层回归时它**真的误报过一次**（run 3，全仓唯一失败项）。

更值得记的是：**我在同一轮里刚诊断掉 Python 侧的同类错误**——
那里是个固定 4 秒轮询预算（KI-15），我还专门写了段「别用时间判断逻辑」
的注释。然后转头在 Go 侧写下了一模一样的东西。

已改成断言**决策**而非**耗时**：
- `explorer.exe` 必须拿到 **0 延迟**（这是「双击不卡」的保证，与负载无关）；
- 未知进程**延迟必须有界**（≤ `maxDelay`），否则同步回调会被长时间占住。

> 教训：**「断言耗时」几乎总是可替换为「断言决策」。**
> 前者依赖运行时环境，后者只依赖代码；前者会假阴性，后者不会。
> 只有当耗时**本身就是需求**时（例如超时契约），才该断言它。

上一轮拿到了**发起方进程名**（前置条件）；这一轮把**限流器本身**补上
（`limiter_windows.go`），并接进 fetch 回调：

```
进程名 → Limiter.Delay() → 需要就 sleep 一会儿 → 才去 open（下载）
```

**必须在 `open` 之前**：数据都下载完了再睡，流量已经花掉了，节流就没有意义。

### 策略（三条判据，按序）

| 判据 | 处理 | 理由 |
| --- | --- | --- |
| 用户交互进程（`explorer.exe`、WPS 全家桶、文件浏览器，共 12 项） | **零延迟** | 用户双击就该立刻打开；慢一点都是体验事故 |
| 名单里的后台扫描（杀软/索引/其它网盘，共 16 项） | 按窗口限流 | 它们不在乎快慢，我们在乎流量 |
| **名单外的未知进程** | **也限流** | 名单永远不完整（索引器宿主进程会变、新工具层出不穷）。把「不认识」当「安全」会让名单一过时就全盘失效；当「可疑」最坏只是让新工具慢一点。**代价不对称**——与 KI-14 的取舍同源 |

名单**逐字取自 WPS 取证 §3.5**（29 项，WPS 二进制字面量）。自行增删等于把
「已知有效」换成「我猜的」。

### 两个刻意的上限

- **单次延迟封顶 200ms**：本回调是**同步**的，占着回调线程不放会拖垮整个同步根
  的正常读取（见上文「已知局限」）。限流只能**降速**，不能**阻塞**。
- **按进程独立配额**（1 秒 8 次）：共用一个计数的话，一个扫描器就能吃光配额，
  连带拖慢另一个本来无辜的系统进程。

### 测试

10 条策略用例（纯函数，**不需要 cldapi.dll**，任何 Windows 机器都能跑）+
1 条端到端（真实挂载后读文件，断言用户路径**未被拖慢**）。

**其中一条当场抓到了实现的 bug**：空进程名的归类写在读 map **之后**，
于是空名请求读到的是一个从没人写过的键（配额恒为 0），
**完全不受限**——正好给了扫描器一个绕过口子。是 `TestEmptyProcessNameStillThrottled`
把它逼出来的，不是审阅。归类必须在读 map 之前。

### 与 KI-14 的关系（**都还没解决的那条**）

节流已就位，但**占位符只读**与**Sync 可重复**这两件事的耦合仍未解：
当前之所以还没丢数据，只是因为重建本身是坏的。详见 KI-14 的时序陷阱。
## 只读是**执行层**保证的（KI-14）

切片 5 叫「只读占位符」，但这个「只读」不能只是一个说法——
因为本包**只注册读回调**（`FETCH_DATA` / `CANCEL_FETCH_DATA`），
用户的编辑、改名、删除**都不会被回调**，云端无从知晓。

实测（2026-10-16）确认了这条链，三段都有证据：

1. 不打只读位时占位符是 `?rw-rw-rw-`，**用户编辑成功**；
2. 改动**没有上传**（无写回调）；
3. 一旦重建带上 `SUPERSEDE`（当前因另一个 bug 没带），**本地内容被覆盖成 0 字节**。

> ⚠️ 注意第 3 点的时序陷阱：**当前之所以还没丢数据，是因为重建本身是坏的**
> （`CfCreatePlaceholders` 报 `ERROR_ALREADY_EXISTS` 而整批失败，见下）。
> 谁去修「重建失败」这个更显眼的 bug，谁就会同时打开数据丢失那条路。
> 两个缺陷互相掩盖——这是本包最值得记住的一处。

**修法**：`PlaceholderSpec.ReadOnly` → `|= FILE_ATTRIBUTE_READONLY`。
实测写入被拒（`Access is denied`），且**水合不受影响**（读回内容正确）——
后者必须实测：水合的本质是内核往这个文件写数据，只读位完全可能把「点开才拉」一起弄坏。

### 已解：`Sync` 现在可重复调用（2026-10-16）

原先 `Sync` 第一次刷新就报 `ERROR_ALREADY_EXISTS`（`CfCreatePlaceholders` 不覆盖）。
当时**刻意不修**——因为直觉上的修法（`SUPERSEDE` 覆盖重建）会清掉本地已水合内容
（KI-14 实测被覆盖成 0 字节），两个缺陷互相掩盖。

正解来自 WPS 取证 §9 第 5 条：**`CfUpdatePlaceholder`**（只更元数据、绝不下载）。
现已落地：

- `Mount` 在 `CfCreatePlaceholders` 报「已存在」时改走 `updateExisting()`；
- **两条错误路径都要认**：实测该错误从**调用级**返回（不是条目级 `Result`），
  我第一版只处理了条目级，于是刷新仍然报错；
- 实测 `TestSyncIsRepeatableWithoutLosingContent`：第二次 Sync 成功、
  内容完好、**未触发下载**、只读保留、大小跟上云端。

#### ⚠️ 一个差点接进去的功能性错误

`OpenPlaceholderHandle` 第一版用 **`GENERIC_READ`** 打开句柄。实测：
**仅打开句柄就触发整份水合**（磁盘分配 0 → 37 字节）。

若这样接进去，「元数据同步」会变成「每次同步下载整个文件」——
`列表零下载` 的核心承诺直接失效，而且**没有任何报错**。

改用 **`FILE_READ_ATTRIBUTES`**（只要属性访问权）后分配保持 0。
`CfUpdatePlaceholder` 是纯元数据操作，不需要数据访问权。

> 这不是「权限给多了不好看」，是**功能正确性**问题：
> 一个看起来无害的 `GENERIC_READ` 把「零下载」变成了「全下载」。

### 原记录：`Sync` 曾不可重复（已解决，保留原因分析）

`mount_windows.go` 的 `Sync` 注释写着「可重复调用（例如定时刷新）」，
实测第一次刷新就 `0x800700B7`（`ERROR_ALREADY_EXISTS`）——因为 `BuildTree`
每条都用 `CreateFlagNone` 重建，而占位符已在磁盘上。

修法是 per-info `PlaceholderCreateFlagSupersede`（常量已定义、**当前刻意未用**）。
**暂不加**的理由见上面的时序陷阱：先确认只读对**所有**写入路径（含改名、删除）都生效，
再让重建可重复。重建的语义（全量重建会丢掉哪些本地状态）属切片 5b 的设计问题，
不适合顺手改。
