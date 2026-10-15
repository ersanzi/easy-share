# REA 逆向工具链备忘

> 本机 REA 的安装、可用性判据与耗时基线。**排障优先查本文**，不要每次重新 `rea doctor` 考古。
> 最后核实：2026-10-15（Windows 10/11 x64，REA 6.4.0 + Ghidra 12.1.4 + JDK 21）

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

## 8. 相关

- 桩钉盘逆向取证（首次系统性使用本工具链）：[`intelligence/2026-10-15-dingpan-file-storage-reverse.md`](intelligence/2026-10-15-dingpan-file-storage-reverse.md)
- WPS 云盘对标（§1.1b 为 REA 结论更正）：[`wps-cloud-drive-benchmark.md`](wps-cloud-drive-benchmark.md)
