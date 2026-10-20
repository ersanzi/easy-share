# 会议纪要 Skill（minutes-from-transcript）

把转写文本变成会议纪要的**独立能力包**。整目录复制到任何地方即可使用，不依赖 EasyShare，**也不需要任何 API key**。

## 它是什么

```
skills/minutes-from-transcript/
├── SKILL.md        ← Agent 读的说明书：三步走流程、退出码、排障
├── scripts/        ← 真正干活的工程（minutecraft，可 pip install）
├── render/         ← 出图用的自包含网页（不用 Node，双击就能用）
└── examples/       ← 可跑通的样例输入与预期产物
```

**核心设计：模型能力由宿主 Agent 提供，工程只做确定性的事。**

```
你（Agent）  ── 用自己的模型回答提示词 ──┐
本 skill     ── 解析输入 / 组装提示词 / 解析回答 / 导出产物 ──┘
```

这样复制到哪里都能用，不用为它单独申请密钥、配置端点。

## 三步出纪要

```bash
cd scripts && python -m pip install -e .       # 一次

minutecraft prepare meeting.txt -o request.json        # ① 组装提示词（不调模型，含 summary/keywords/topics 三项）
# ② 把 request.json 里的 requests 交给你的模型，回答原样存成 answers.json
minutecraft finish request.json --answers answers.json -o out/    # ③ 导出（不调模型）
```

产物：`纪要.docx`（钉钉版式）、`minutes.md`、`minutes.json`、`visual.json`、`report.json`。

`answers.json` 有三个键，只有 `summary` 必填（另外两项不答会记警告并降级）：

| 键 | 作用 | 不答会怎样 |
| --- | --- | --- |
| `summary` | 纪要/要点/待办/章节/洞察 | **必填** |
| `keywords` | 关键词 | 跳过 |
| `topics` | 长网页报告正文（4-8 个议题，含分节/聚焦/洞察） | 长图回退「按章节均分」的粗版式（只剩标题与时间），并记一条警告 |

出图两条路，按需要选：

```bash
minutecraft render out/minutes.json -o out/report.png --pdf out/report.pdf   # 长网页报告（需 Chrome/Edge）
```

或者浏览器打开 `render/card.html`，把 `visual.json` 拖进去 → 渲染 → 打印/存 PDF（色卡封面，只需浏览器）。

## 无人值守模式（可选）

批量跑、没人盯着时才需要：

```bash
export MINUTECRAFT_LLM_BASE_URL=... MINUTECRAFT_LLM_API_KEY=... MINUTECRAFT_LLM_MODEL=...
minutecraft run meeting.txt --out ./out --json
```

## 能力与边界

| 能做 | 不做 |
| --- | --- |
| 纪要 / 要点 / 待办 / 章节 / 关键词 / AI 洞察 / 核心发言 / 导图大纲 | 音频转写（只吃文本） |
| 钉钉版式 Word、Markdown、结构化 JSON、汇报卡网页 | 声纹 / 热词 / 实时上屏（属产品态） |
| 长网页报告 PNG/PDF（`render` 子命令，需本机 Chrome/Edge） | 云端存储与权限（产物只落本地） |
| 7 套场景模板（周会/站会/面试/访谈/培训/头脑风暴） | 三步走不跑三阶段导图（要用 `run`） |
| 三步走（零配置）与 `run`（自带客户端）两条路径，产物结构一致 | |

## 与本仓库的关系

`scripts/` 是 [`../../engine/`](../../engine/) 的**发布副本**，开发改 `engine/`，改完同步：

```bash
python scripts/check_skill_sync.py           # 对账（CI 也跑）
python scripts/check_skill_sync.py --apply   # 同步
```

出图能力已收进引擎（`scripts/minutecraft/report/`），`render/` 只剩色卡封面 `card.html`。
`card.html` 与 `frontend/src/components/MeetingVisualReport.vue` 是**同一套钉钉色卡版式的两个实现**
（一个给外部复制使用、只需浏览器；一个给桌面端内嵌）。**改色卡版式时两处一起改**。

**但这两份只管色卡封面，不管长网页报告。** 长报告（`minutecraft render` 出的那张长图）是另一个产物：
它由引擎的 `report/report.html` 渲染、结构来自 `summary.topics`（议题 → 分节 → 要点），
桌面端不内嵌它，也没有同款 Vue 实现——**长报告改版式只改 `engine/minutecraft/report/report.html` 一处**。
别把「两处一起改」这条规矩套到长报告上：抄过去只会造出一个没人维护的第二实现。
