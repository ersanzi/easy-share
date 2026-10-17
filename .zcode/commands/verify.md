---
description: 五层轻量验证：Go/Python/前端/Java + 活栈（真实控制面）一键汇总（不含 NSIS 全量构建）
disable-model-invocation: true
---

# /verify — 五层轻量验证（活栈为第 5 层，环境齐备时跑）

内环快速回归，不必每次跑含 NSIS 的全量 `scripts/build.ps1`。依次执行下列各层并汇总：

1. **Go**：仓库根目录 `go test ./...`
2. **Python**：工作目录切到 `knowledge/`，执行 `.venv/Scripts/python.exe -m pytest -m "not integration"`（集成测试依赖 RustFS，不在内环）
3. **前端**：`npm --prefix frontend test`；若用户要求含类型检查再加 `npm --prefix frontend run build`
4. **Java 控制面（platform-drive）**：`cd platform-drive && mvn -o clean test`
   （**必须带 `clean`**：增量构建会给出陈旧结果，见 KI-8 的踩坑记录）
5. **活栈（真实控制面 + PG + RustFS）**——**仅当环境齐备时**：
   `go test ./internal/drive/ ./internal/account/ ./internal/spacedav/ -run TestLive -count=1`
   （需 `EASYSHARE_LIVE_DRIVE=1` 等环境变量，见 [`development.md`](../../docs/development.md)
   「活栈测试」一节）。环境没起就**明确标注「本轮未跑活栈」**，不要静默略过。

> ⚠️ **第 5 层（活栈）是 2026-10-16 补上的**，理由见 KI-18～KI-23：
> 那六个缺陷**全部**只能在真实控制面上暴露——单测里 `fileId`/`sessionId` 是自己编的小数字，
> 真实控制面发的是 19 位雪花 ID（超 JS 安全整数、被序列化成字符串）。
> 「单测全绿」与「真实可用」之间此前隔着六个缺陷，其中三个能静默丢数据。
>
> ⚠️ **第 4 层是 2026-10-16 补上的**。此前验证只覆盖三层，而 `platform-drive`
> 的 Java 测试**根本没被任何流程执行过**——父 POM 的 `maven.test.skip=true` 让
> `mvn test` 报 `BUILD SUCCESS` 却一个测试都不跑，于是数据丢失缺陷（KI-7）
> 一直藏着。详见 [`known-issues.md`](../../docs/known-issues.md) KI-8。
>
> **判据不是退出码，是输出里有没有 `Tests run: N`**。任何一层若看不到具体用例数，
> 都要怀疑它没真跑。

输出要求：

- 一张汇总表：层级 / 命令 / **用例数** / 结果（通过、失败、跳过）/ 耗时。
  **用例数必填**——没有它就分不清「通过」与「根本没跑」。
- 失败项：给出失败的包/文件、关键断言或错误输出的最后几行、最可能的定位建议；不要整屏贴日志。
- 全绿时提示：可进入 `scripts/build.ps1` 全量流水线，或等待用户的提交指令。
- 某层环境缺失（如 venv 不存在、依赖未装）：说明缺失项与修复命令，跳过该层继续其余层，不中断整轮。

注意：各层测试命令的权威清单以 `docs/development.md` 为准；若本命令与其冲突，以 development.md 为准并提醒用户更新本命令文件。
