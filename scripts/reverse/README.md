# 逆向取证脚本

本目录存放**可复用的逆向取证脚本**。配套方法论见 [`docs/toolchain-rea.md`](../../docs/toolchain-rea.md) §8。

## Ghidra 脚本（`.java`）

用于大目标（> 2 MB）的**多轮定向挖掘**。REA 每次调用都要重新导入分析工程（10 MB 级约 10–15 分钟），
而 Ghidra 的 headless 模式支持把工程存盘复用——**一次性导入，之后每次查询 8–24 秒**。

### 前置

```powershell
$env:JAVA_HOME          = 'D:\Develop\java21'            # Ghidra 12.1.4 要求 JDK 21+
$env:GHIDRA_INSTALL_DIR = 'D:\Develop\ghidra_12.1.4_PUBLIC'
```

### 建工程（一次性）

```powershell
& "$env:GHIDRA_INSTALL_DIR\support\analyzeHeadless.bat" `
    output\ghidra-proj <工程名> -import <目标.dll|exe> -overwrite
```

10.4 MB 实测 **270 s**（其中 `Windows x86 PE RTTI Analyzer` 32 s、`x86 Constant Reference Analyzer` 58 s）。

### 查询（复用工程，`-noanalysis`）

```powershell
$env:PROBE_OUT = "$PWD\output\probe.txt"     # 结果落盘；不设则打 stdout
& "$env:GHIDRA_INSTALL_DIR\support\analyzeHeadless.bat" `
    output\ghidra-proj <工程名> -process <目标.dll> -noanalysis `
    -scriptPath scripts\reverse -postScript ProbeStrings.java 'LocalModify' 'RemoteModify'
```

### 脚本清单（8 个）

| 脚本 | 用途 | 用法 |
| --- | --- | --- |
| `ProbeStrings.java` | 按**字符串针**找 defined data → 全部 xref → 对引用函数反编译 | `-postScript ProbeStrings.java <针1> [针2]...` |
| `ConstRefs.java` | 扫全 `.text` 指令流找**立即数**等于目标值的指令与所在函数，并反编译命中函数 | `-postScript ConstRefs.java <v1> [v2]...`（十进制或 `0x`） |
| `FieldUsers.java` | 找**访问某结构体偏移**的函数，按关键字过滤后反编译（定位字段读写点） | `-postScript FieldUsers.java <disp_hex> <kw1> [kw2]...` |
| `FuncCallers.java` | 找**引用某函数地址**的所有位置与所在函数（含调用者反编译）；用于追「谁注册/调用了它」 | `-postScript FuncCallers.java <addr1> [addr2]...` |
| `DumpFuncs.java` | 批量反编译给定地址（比 `ProbeStrings` 更直接，已知地址时用） | `-postScript DumpFuncs.java <addr1> [addr2]...` |
| `DumpBytes.java` | 转储指定地址区间的原始字节，并按 `CF_CALLBACK_REGISTRATION` 形态试解析 | `-postScript DumpBytes.java <start_hex> <len>` |
| `ScanCbTable.java` | 盲扫回调表（**64 位目标**，16 B/项） | `-postScript ScanCbTable.java` |
| `ScanCb32.java` | 盲扫回调表（**32 位目标**，8 B/项） | `-postScript ScanCb32.java` |

### 写脚本的坑（照抄省一次踩）

| 坑 | 症状 | 正解 |
| --- | --- | --- |
| `Data` 包路径 | `cannot find symbol: class Data / location: package ghidra.program.model.data` | `Data` / `DataIterator` 在 **`ghidra.program.model.listing`** |
| `getReferencesTo` 返回值 | `ReferenceIterator cannot be converted to Reference[]` | 用 `ReferenceIterator` + `while (hasNext())` |
| 类名与文件名 | `The class could not be found. It must be the public class of the .java file` | 文件名必须等于 `public class` 名 |

### 证据级别

**本目录脚本的产出一律标【G】**（自建工程 + 自写脚本反编译）——可复现，但**没有 REA Evidence ID**，
不能当 REA 证据引用。需要正式 Evidence 时，用 `rea analyze` / `rea decompile` 对**已定位的少数函数**单独出证
（记得 `$env:REA_GHIDRA_STARTUP_TIMEOUT_MS`，默认 330 s 对 10 MB 级目标不够，详见 toolchain §8.1）。

### 三条实战判据（省得重复踩）

1. **先确认目标位数再选扫描脚本。** Ghidra 的 imageBase 可判：`0x10000000` 段 = **32 位**（`ScanCb32`），
   `0x140000000` / `0x180000000` 段 = **64 位**（`ScanCbTable`）。拿 64 位 stride 去扫 32 位目标会**一无所获**。
2. **回调入口别从「谁调用它」找**——CfApi/FUSE 这类回调**只被表引用，没有任何代码直接调用**，
   `getReferencesTo` 会返回 0（本次五个回调里四个为 0）。正确入口是**反编译注册点**
   （`CfConnectSyncRoot` / `CfRegisterSyncRoot` 的调用者），直接读它传的结构体地址；再用盲扫交叉验证。
3. **盲扫结果必须过滤 `Unwind@…` 伪符号。** MSVC 的 C++ 异常 unwind 表与回调表结构同形，
   实测 32 位目标 14 个候选中 13 个是 unwind 表。

## Python 脚本

| 脚本 | 用途 |
| --- | --- |
| `minutes-enum-mine.py` | 钉钉听记：枚举常量挖掘 |
| `minutes-i18n-mine.py` | 钉钉听记：多语言文案挖掘 |
