---
description: 三层轻量验证：Go/Python/前端测试一键汇总（不含 NSIS 全量构建）
disable-model-invocation: true
---

# /verify — 三层轻量验证

内环快速回归，不必每次跑含 NSIS 的全量 `scripts/build.ps1`。依次执行三层并汇总：

1. **Go**：仓库根目录 `go test ./...`
2. **Python**：工作目录切到 `knowledge/`，执行 `.venv/Scripts/python.exe -m pytest -m "not integration"`（集成测试依赖 RustFS，不在内环）
3. **前端**：`npm --prefix frontend test`；若用户要求含类型检查再加 `npm --prefix frontend run build`

输出要求：

- 一张汇总表：层级 / 命令 / 结果（通过、失败、跳过）/ 耗时。
- 失败项：给出失败的包/文件、关键断言或错误输出的最后几行、最可能的定位建议；不要整屏贴日志。
- 全绿时提示：可进入 `scripts/build.ps1` 全量流水线，或等待用户的提交指令。
- 某层环境缺失（如 venv 不存在、依赖未装）：说明缺失项与修复命令，跳过该层继续其余层，不中断整轮。

注意：各层测试命令的权威清单以 `docs/development.md` 为准；若本命令与其冲突，以 development.md 为准并提醒用户更新本命令文件。
