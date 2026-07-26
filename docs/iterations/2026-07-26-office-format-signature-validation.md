# Office 文件格式签名校验

> 日期：2026-07-26
> 状态：已完成

## 用户问题

Local Lab 上传 `员工手册.docx` 后，任务在解析阶段失败：

```text
DocumentParseError
解析 员工手册.docx 失败: File is not a zip file
```

## 现场诊断

RustFS 中源对象大小为 67072 字节，SHA-256 为 `6d047f2b47f261a7b7bfdfc2ecd47fcf84f9d6140cc15da4dfe2f51eebba1730`。文件头为：

```text
D0 CF 11 E0 A1 B1 1A E1
```

该签名表示 Microsoft Compound File Binary Format（OLE 复合文档），即旧版 Office 二进制格式。真正的 `.docx/.xlsx/.pptx` 属于 Office Open XML（OOXML），底层是 ZIP 容器，并分别包含核心成员：

- DOCX：`word/document.xml`
- XLSX：`xl/workbook.xml`
- PPTX：`ppt/presentation.xml`

因此本次失败不是上传损坏，而是旧版 `.doc` 文件被命名为 `.docx`。仅修改扩展名不会转换文件格式。

## 目标

1. 在进入 `python-docx/openpyxl/python-pptx` 前校验 OOXML 容器和核心成员。
2. 对 OLE 旧版 Office 文件给出明确、可操作的中文提示。
3. 对普通损坏文件和 OOXML 类型错配分别给出稳定错误信息。
4. 通过解析器单元测试和 Local Lab 异步任务测试覆盖回归。

## 技术决策

- 不在本轮直接支持旧 `.doc/.xls/.ppt` 解析；建议用户使用 Word/WPS 打开后“另存为”现代 OOXML 格式。
- 校验基于文件内容而非扩展名：先识别 OLE 签名，再校验 ZIP，最后检查 OOXML 核心成员。
- 若 ZIP 内实际包含另一种 OOXML 核心成员，则直接提示实际格式与扩展名不一致。
- 保持任务错误码 `DocumentParseError` 不变，只改善面向用户的错误详情。

## 代码影响

- `knowledge/app/parsing/extractor.py`
- `knowledge/tests/test_parsing.py`
- `knowledge/tests/test_lab.py`
- `knowledge/README.md`
- `docs/troubleshooting.md`
- `docs/progress.md`

## 排障方法

1. 不要根据文件名判断 Office 格式。
2. 查看前 8 字节：`D0 CF 11 E0 A1 B1 1A E1` 表示旧版 OLE Office 文件。
3. 对 `.docx/.xlsx/.pptx` 使用 ZIP 工具检查核心成员。
4. 用户侧修复应使用 Word/WPS 的“另存为”，不能只重命名扩展名。

## 验证记录

- `knowledge/.venv/Scripts/python.exe -m pytest knowledge/tests/test_parsing.py knowledge/tests/test_lab.py -q`：20 passed。
- `knowledge/.venv/Scripts/python.exe -m pytest knowledge/tests -q`：35 passed，1 skipped；跳过项为默认关闭的真实 RustFS 集成测试。
- 构造 OLE 文件头 `D0 CF 11 E0 A1 B1 1A E1` 并以 `员工手册.docx` 上传，任务稳定返回 `DocumentParseError`，错误详情包含旧版 `.doc`、扩展名不一致和 Word/WPS“另存为”指引，不再出现 `File is not a zip file`。
- 使用真实 XLSX 内容配 `.docx` 扩展名，错误会识别实际内容为 `.xlsx`。
- 使用合法 ZIP 但缺少 `word/document.xml`，错误会指出缺少的核心结构。

## 用户侧验收步骤

1. 重启 Python 服务，使解析器新代码生效。
2. 重新上传原 `员工手册.docx`，确认错误提示变为旧版 `.doc` 格式不匹配，而不是 `File is not a zip file`。
3. 用 Word/WPS 打开原文件，选择“另存为”并保存为真正的 `.docx`。
4. 上传新生成的 `.docx`，确认八阶段任务完成并能查看 `clean.md/document.json/manifest.json`。
5. 不要对旧任务直接点击重试；旧任务引用的 RustFS 对象内容没有改变，应上传转换后的新文件。
