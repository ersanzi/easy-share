# 驾驶舱清洗 Diff：规则引擎动作明细 + Diff 视图

> 日期：2026-08-04
> 状态：已完成（全量回归通过）

## 目标

补齐知识质量驾驶舱设计文档（`docs/knowledge-quality-cockpit.md`）第一层"单文档透视"中缺失的**清洗 Diff** 视图：让用户一眼看到"清洗动了什么、该不该动"——被规则删除的内容用删除线 + 红色背景标注，附规则名称与命中计数。

此前该视图是设计文档第一期三项交付中唯一缺口（切块地图与结构化块已实现）。

## 变更清单

### 1. 规则引擎动作明细（`knowledge/app/parsing/rules.py`）

`RuleEngine.apply()` 在清洗时记录逐动作明细（`engine.actions`）：

- **整块删除**（`remove_block`）：页眉页脚等结构规则删除整块时，记录块 ID 与删除前全文
- **文本改写**（`text`）：PII 脱敏 / 自定义替换改写块或表格单元格时，记录修改前后文本
- 明细含 `rule_id / rule_name / kind / block_id / before / after`
- 防失控上限：`MAX_ACTIONS=1000` 条、单条文本截断 600 字符，避免超大文档撑爆 manifest

### 2. Manifest 持久化（`knowledge/app/pipeline/service.py`）

`cleaning_report` 新增 `actions` 字段随 manifest 落盘，与逐规则命中数同级，保证清洗可追溯、可对账。

### 3. 驾驶舱 API（`knowledge/app/debug/routes.py`）

`GET /debug/document/{file_id}` 返回新增 `cleaning_actions` 字段：

- 从 manifest 透出动作列表，兼容旧文档（无 `actions` 字段时优雅降级为空列表，不报错）
- 旧动作缺 `rule_name` 时按 `rule_id` 从 manifest 的规则表回填，保证 UI 始终可显示规则名

### 4. 驾驶舱 UI（`knowledge/app/debug/cockpit.html/js/css`）

单文档透视右栏新增第四个 sub-tab **清洗 Diff**：

- 顶部汇总条：动作总数 + 按规则分组的命中数 chips
- 每条动作卡片：规则名、动作类型（整块删除 / 文本改写）、块 ID
- 删除内容：删除线 + 红色背景（`diff-item-removed`），符合设计文档"删除线 + 红色背景"要求
- 文本改写：before 删除线 + after 绿色回显
- 同类动作去重展示（同一页眉重复 30 次只展示 1 条），上限 100 条 + 折叠提示

## 设计取舍

- **明细记录在规则引擎层**而非驾驶舱读原始块做 diff：引擎知道"删了哪一块、改了什么文本"，比事后字符级 diff 更可靠（无需重跑解析、不依赖原文 artifact 仍在）
- **动作冗余去重**：页眉页脚在每页重复命中，UI 全量展示无意义；按"规则+类型+文本"去重，保留代表性样本
- **上限保护**：千条动作/600 字符截断，数据量可控且不影响检索链路

## 验证

- 新增 `tests/test_cleaning_rules.py` 3 条：页眉整块删除明细、页码行文本改写明细、PII 脱敏 before/after
- 新增 `tests/test_cockpit.py`：`/debug/document` 返回清洗动作、未知文档 404
- `test_pipeline_records_cleaning_hits_in_manifest` 扩展：断言 manifest 内嵌 `actions` 及字段完整性
- Python 全量回归：**70 passed, 2 skipped**（PaddleOCR 集成用例）
