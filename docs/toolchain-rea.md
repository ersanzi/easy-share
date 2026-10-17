# REA 逆向工具链备忘

> 本机 REA 的安装、可用性判据、耗时基线，以及**大目标（10 MB 级）取证通路**。**排障优先查本文**，不要每次重新 `rea doctor` 考古。
> 最后核实：2026-10-16（Windows 10/11 x64，REA 6.4.0 + Ghidra 12.1.4 + JDK 21）
>
> ⚠️ **先看 §8**：若目标 > 2 MB 且需要多轮定向挖掘，不要直接用 REA 硬跑（每次 10–15 min 且可能撞 330 s 超时）；
> 先按 §8.3 建持久化 Ghidra 工程，把地址与函数定准，再用 REA 对少数函数出正式 Evidence。

## 1. 本机可用配置

REA 装于 `C:\Users\lishi\.local\bin\rea.cmd`（pipx，见 `%APPDATA%\Python\Python312\Scripts\pipx.exe`）。

**必需的两个环境变量**（当前均为用户级，未持久化到系统）：

```powershell
$env:GHIDRA_INSTALL_DIR = 'D:\Develop\ghidra_12.1.4_PUBLIC'
$env:JAVA_HOME          = 'D:\Develop\java21'
```

| 变量 | 缺省时的症状 | 正确值依据 |
| --- | --- | --- |
| `GHIDRA_INSTALL_DIR` | 未设 → `rea doctor` 报 `ghidra: missing_analysis_engine` | 需指向**解压后的 Ghidra 发布目录**（其下应有 `Ghidra\application.properties` 与 `support\analyzeHeadless.bat`） |
| `JAVA_HOME` | 指向 JDK 17 → `provider_unavailable / unsupported_version`：`accepted a 64-bit full JDK 21 or newer` | Ghidra 12.1.4 硬要求 64-bit full JDK 21+；本机 `D:\Develop\java21` 可用，`java17` **不可用** |

> 坑：`java` 在 PATH 上已经是 `D:\Develop\java21\bin\java.exe`，但 REA **不看 PATH，只读 `JAVA_HOME`**。表现为「命令行 java -version 是 21，rea 却报 JDK 17」。

## 2. 可用性判据

不要用 `rea doctor` 的 `healthy: false` 当结论——它把 Hopper（macOS only）、IDA MCP、10 个客户端注册、REA skill 一并计入，这些在本机**永远**是 false，与能否分析 PE 无关。

**唯一有效判据：跑一次真实分析命令。**

```powershell
rea decompile <任意小 PE> --procedure entry --provider ghidra --format json
```

返回体含 `provider.id=ghidra` + `evidence_id` + `normalized_result` 即完全可用。

## 3. 耗时基线（实测）

| 命令 | 目标体积 | 耗时 | 备注 |
| --- | --- | --- | --- |
| `decompile --procedure entry` | 282 KB | **53 s** | 冷启动（含 Ghidra 工程导入） |
| `decompile` | 1.2 MB | **89 s** | 第二条命令，同一进程内无缓存复用 |
| `analyze` | 1.2 MB | **91 s** | 同样体量 |

线性外推：10 MB 级 ≈ **12–15 分钟**。**一次会话只做一次全量分析**，后续用同一命令行里的 `--procedure`/`--filter-output` 细分，不要反复重跑。

Ghidra 会话是**每进程临时**的（`import_mode: ephemeral-source-immutable`，关闭即删工程），因此**没有跨命令的缓存**——每次调用都重新导入。这是耗时无法摊薄的根本原因。

## 4. 输出控制

- `--token-limit` / `--token-offset` 与 `--format json` **互斥**（`UNSUPPORTED_OUTPUT_COMBINATION`）。要裁剪输出用 `--filter-output <key.paths>`。
- 支持的格式：`toon | json | yaml | md | jsonl`。
- `--filter-output` 的 key 路径要对得上实际结构，写错会**静默返回 `{}`**（不报错）。先不加 filter 跑一次小目标看结构，再上 filter。

## 5. 常见错误与正解

| 报错 | 真实原因 | 正解 |
| --- | --- | --- |
| `provider_unavailable: unsupported_version` | `JAVA_HOME` 是 JDK 17 | 切到 JDK 21+，见 §1 |
| `provider_unavailable: missing_analysis_engine` | `GHIDRA_INSTALL_DIR` 未设 | 指向解压目录，见 §1 |
| `invalid_value: Unknown Ghidra procedure name or address: <导入名>` | 传入的是 **PE 导入表符号**（如 `CfCreatePlaceholders`），Ghidra 里那是外部符号，不是有函数体的过程 | 先 `rea xrefs`/`analyze` 拿到真实地址，再用地址或 `FUN_xxxxxxxx` 调 `decompile` |
| `UNSUPPORTED_OUTPUT_COMBINATION` | `--token-limit` 与结构化输出同用 | 改 `--filter-output` |
| 输出恒为 `{}` | `--filter-output` 的 key 路径不存在 | 去掉 filter 重跑看真实结构 |
| 目标路径被运行中进程占用 | REA 对路径有准入检查，被占用会撞共享冲突 | 把产物复制到中性目录；**复制不改变 SHA-256**，Evidence 的 `subject.digest` 仍可比对 |

## 6. 证据强度与引用方式

每次分析返回 `evidence_id`（`ev_<sha256>`），证据链是 `sha256(subject) + provider(id/version) + analysis_profile.digest + operation` 四元组。**写文档时引用 `evidence_id`，不要只写「反编译看到」**——`evidence_id` 可被 `rea evidence-export` 复现与交叉验证。

返回体自带的 `limitations[]` 是 REA 自报的观测边界，**必须照抄进结论**，不要吞掉。典型如：

- Ghidra 的 terminal-call 流模型会排除 fallthrough，原因未被本观测确立。
- 伪码是反编译器输出，**不是原始源码，也不等价于 Hopper 输出**。
- 无函数体或外部函数返回 `null`。
- 分析未完成时不暴露部分结果。

## 7. 目标形态与 provider 选择

| 目标形态 | provider | 入口命令 |
| --- | --- | --- |
| Windows 原生 PE（exe/dll） | `ghidra`（本机唯一可用） | `analyze` / `decompile` / `function` / `xrefs` / `inspect-pe-resources` |
| Electron / 网站 | electron / browser | `analyze-javascript-application`、`capture-web-screenshot` |
| Android APK | apktool / android | `inspect-android-package` |
| macOS Mach-O | hopper（**win32 不支持**） | — |

> `rea doctor` 中 `hopper: unsupported_host` 与 `ida: not_configured` 在本机是**预期状态**，不是故障。

## 8. 【2026-10-16 关键修正】330 s 超时可调，10 MB 级目标已跑通

上一轮（钉盘 `sync_frame.dll` 取证）记录的「`provider_timeout` 330 000 ms、10 MB 级目标超预算」**是配置问题，不是引擎能力边界**。本轮已定位并解决，`sync_frame.dll`（10 374 656 B）**首次分析成功出证**。

### 8.1 超时从哪来、怎么调

REA 不是 Python 包，是 Node 应用：`C:\Users\lishi\tools\rea\node_modules\rea-agents\`。

| 事实 | 位置 |
| --- | --- |
| 默认 330 s 常量 | `dist/config/ghidraStartupTimeout.js` → `DEFAULT_GHIDRA_STARTUP_TIMEOUT_MS = 330_000`，注释为「Ghidra import/analysis/bridge/readiness 的绝对截止时间」 |
| 环境变量名 | `dist/config/environment.js:34` → `REA_GHIDRA_STARTUP_TIMEOUT_MS`（zod 校验：正整数字符串，上限 2 147 483 647） |
| 传入引擎 | `dist/ghidra/GhidraProviderClient.js:235,298,312,329,336` |

```powershell
$env:REA_GHIDRA_STARTUP_TIMEOUT_MS = '1800000'   # 30 分钟
```

**实测**（同一 `sync_frame.dll`，SHA-256 `4697e02d…efc4`）：

| 配置 | 结果 |
| --- | --- |
| 默认 330 s | `provider_timeout`（上一轮记录） |
| `REA_GHIDRA_STARTUP_TIMEOUT_MS=1800000` | ✅ 成功，`ev_dc508b2cc52ed00486b4f95ed854ea019224bc9b1e1e513db6794aebc9da97b8` |

产物规模：11 段、`.text` 7 596 032 B、**32 337 个函数**、9 621 条串。

### 8.2 `--snapshot` 不是工程缓存（实测证伪）

`analyze` / `decompile` 都有 `--snapshot <dir>`，容易误以为能复用分析。**实测：不能。**

- 2 次调用都带同一 `--snapshot` 目录，第二次 `decompile` 仍耗 **86.65 s**，与无 snapshot 的基线一致。
- 该目录落盘 7 541 B，内容经 `dist/domain/analysisSnapshot.js` 确认是**结果级去重缓存**（按 `sha256 + kind + format + architecture + provider` 绑定），不含 Ghidra 工程。
- 根因仍是 `import_mode: ephemeral-source-immutable`：进程退出即删工程。

### 8.3 正解：自建持久化 Ghidra 工程 + 自写脚本（本轮验证）

REA 每次调用重付导入成本，但 Ghidra 自带 headless 模式**支持把工程存盘复用**。这是 10 MB 级目标做「多轮定向取证」的唯一经济解法。

```powershell
$env:JAVA_HOME='D:\Develop\java21'
$env:GHIDRA_INSTALL_DIR='D:\Develop\ghidra_12.1.4_PUBLIC'

# 一次性：导入 + 全量分析 + 存盘（10.4 MB 实测 270 s）
& "$env:GHIDRA_INSTALL_DIR\support\analyzeHeadless.bat" `
    <工程目录> <工程名> -import <目标文件> -overwrite

# 之后每次取证：复用工程，跳过分析（实测 8–24 s／次）
& "$env:GHIDRA_INSTALL_DIR\support\analyzeHeadless.bat" `
    <工程目录> <工程名> -process <文件名> -noanalysis `
    -scriptPath scripts\reverse -postScript <脚本.java> <参数...>
```

**耗时对比**（`sync_frame.dll`）：

| 路径 | 单次成本 | 可否复用 |
| --- | --- | --- |
| REA `decompile`/`analyze` | 10–15 min | ❌ 每次重导入 |
| 自建工程 + `-noanalysis` 脚本 | **8–24 s** | ✅ 工程常驻 |

脚本已版本化到 [`scripts/reverse/`](../scripts/reverse/README.md)（配套说明见该目录 README）：
- `ProbeStrings.java`：按字符串针找 defined data → 全部 xref → 对引用函数反编译（`PROBE_OUT` 环境变量落盘）。
- `FieldUsers.java`：按结构体偏移定位字段读写点（读/写侧分开看，用于判断枚举取值域是否在客户端）。
- `ConstRefs.java`：扫全 `.text` 指令流（1 308 832 条），找立即数等于目标值的指令与所在函数，并反编译命中函数——用于定位字符串里没有的数值常量。

### 8.4 写 Ghidra 脚本的两个坑

| 坑 | 症状 | 正解 |
| --- | --- | --- |
| `Data` 的包路径 | `error: cannot find symbol / class Data / location: package ghidra.program.model.data` | `Data` 与 `DataIterator` 在 **`ghidra.program.model.listing`**，不是 `...model.data` |
| `getReferencesTo` 返回迭代器 | `incompatible types: ReferenceIterator cannot be converted to Reference[]` | 用 `ReferenceIterator` + `while (refs.hasNext())`，不要按数组写 |

脚本类名必须与文件名一致（`ProbeStrings.java` → `public class ProbeStrings`），否则报 `The class could not be found`。

### 8.5 取证通路的取舍

| 需求 | 走哪条 |
| --- | --- |
| 要 **Evidence ID**（可复现、可交叉验证、写文档引用） | REA（代价：每次 10–15 min，用 `REA_GHIDRA_STARTUP_TIMEOUT_MS` 放宽） |
| 要 **多轮定向挖取**（追数据流、找常量、批量反编译） | 自建 Ghidra 工程 + 脚本（代价：一次性导入 + 秒级查询） |
| 两者都要 | 先用自建工程把问题问清、把地址与函数定准，再用 REA 对**已定位的少数函数**出正式 Evidence |

> 注意：自建工程路径的产出**没有 REA Evidence ID**，写文档时须按【S】/【G】级别标注，不能冒充 REA 证据。

---

## 9. 相关

- 桩钉盘逆向取证（首次系统性使用本工具链）：[`intelligence/2026-10-15-dingpan-file-storage-reverse.md`](intelligence/2026-10-15-dingpan-file-storage-reverse.md)
- WPS 云盘对标（§1.1b 为 REA 结论更正）：[`wps-cloud-drive-benchmark.md`](wps-cloud-drive-benchmark.md)
- WPS CfApi 占位符逆向（§2.1 的 16.7 MB `qingnse64.dll` 分析仍欠）：[`intelligence/2026-10-15-wps-cfapi-placeholder-reverse.md`](intelligence/2026-10-15-wps-cfapi-placeholder-reverse.md)
