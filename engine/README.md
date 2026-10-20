# minutecraft — 转写文本 → 会议纪要（通用引擎）

把「一段已经转写好的会议文本」变成可交付的会议纪要：**纪要 / 要点 / 待办 / 章节 / 关键词 /
AI 洞察 / 核心发言提炼 / 思维导图 / 可视化汇报卡**，并导出成钉钉版式的 Word 与 Markdown。

设计上是一个**独立、可复用、可单独维护**的工程，被两个方向同时消费：

- **产品内**：EasyShare 桌面端的「会议听记」（知识服务 `knowledge/app/meetings/` 经薄壳转发到这里）
- **产品外**：WorkBuddy / DSH / Codex 等 Agent 直接调用（命令行或 Python 库）

## 边界：只管文本，不管音频

```
录音 ──► 转写（ASR） ──► 【minutecraft】──► 纪要 / Word / 汇报卡
         ↑                                  ↑
    采集端职责                        本工程职责
    FunASR / whisper / 云 ASR
```

语音识别属于采集端。EasyShare 里它在知识服务的 `_asr_impl.py`（FunASR + cam++ 说话人分离），
本工程**内核不依赖 torch**。若 Agent 手上只有一个 wav，可装可选依赖 `[asr]` 顺带转写，见文末。

## 快速开始

```bash
cd engine
pip install -e .                       # 内核：openai + httpx + python-docx

export MINUTECRAFT_LLM_BASE_URL=https://api.example.com/v1
export MINUTECRAFT_LLM_API_KEY=sk-xxx
export MINUTECRAFT_LLM_MODEL=deepseek-v4-flash
# 内网端点不走系统代理时：export MINUTECRAFT_LLM_TRUST_ENV=false

minutecraft run meeting.txt --out out/
# out/产品周会 纪要.docx   out/minutes.md   out/minutes.json
# out/visual.json（色卡封面）   out/report.json（长网页报告数据）
```

不装包也能跑：`PYTHONPATH=engine python -m minutecraft run meeting.txt --out out/`

> 装包注意：**别把 `engine/` 放进 `PYTHONPATH` 又同时在该目录跑脚本**，也别把脚本放在有同名
> 模块（`json`、`build` 等）的目录里——`sys.path[0]` 会遮蔽标准库，`lxml` 会报 `cannot import name
> 'getargspec'` 这种八竿子打不着的错。用 `pip install -e .` 或从其他目录 `-m minutecraft` 都行。

## 输入：什么格式都收

| 形态 | 例子 | 说明 |
| --- | --- | --- |
| 纯文本（带时间戳） | `张三 [00:12] 部署我来跟进` | 自动识别说话人与 `mm:ss` / `hh:mm:ss` |
| 纯文本（冒号） | `张三：部署我来跟进` | 没有时间戳时按行序，时间记 0 |
| SRT / VTT | `00:00:01,200 --> 00:00:04,500` | 字幕导出直接可用 |
| JSON 记录 | `{"segments": [...], "speaker_names": {...}}` | EasyShare 听记记录原样丢进来即可 |
| 标准输入 | `cat a.srt \| minutecraft run -` | 管道友好 |

## 命令行

```
minutecraft run <输入> [--out DIR] [--template ID] [--title T]
                       [--no-keywords] [--no-mindmap] [--no-docx]
                       [--with-transcript] [--json]
minutecraft prepare <输入> -o request.json     # 只组装提示词，不调模型
minutecraft finish <请求包> --answers a.json   # 只解析回答与导出，不调模型
minutecraft schema [--json]       # 输入/产物结构自描述（Agent 组织提示用）
minutecraft templates [--json]    # 内置模板：会议纪要/周会/站会/面试/访谈/培训/头脑风暴
minutecraft visual minutes.json   # 只出可视化汇报卡结构（色卡封面用）
minutecraft render minutes.json -o report.png [--pdf report.pdf] [--width 1080]
                                  # 长网页报告出图（需本机 Chrome/Edge，不联网）
```

退出码：

| 码 | 含义 | 怎么办 |
| --- | --- | --- |
| `0` | 成功，产物齐全 | 继续 |
| `2` | 输入无法解析 | 检查文件，别重试同样的输入 |
| `3` | LLM 或生成失败 | 看 stderr 的具体原因 |
| `4` | **产物已出但有降级**（缺 topics / 缺 keywords / 议题解析失败） | **不是失败，别重跑**；按 stderr 补答那一项 |

`4` 不是万能告警：它只在「产物出来了但有东西没生成」时出现，stderr 会逐条列出原因，
`--json` 的 `warnings` 是同内容。之所以单列一个码，是因为 **`exit 0` 会被 Agent 当成「全都齐了」**——
实测踩过 keywords 被静默降级、退出码仍是 0，得翻 warnings 才发现数据丢了。

`--json` 输出机器可读结果：

```json
{
  "ok": true,
  "title": "产品周会",
  "template": "meeting-minutes",
  "todos": 3,
  "keywords": ["知识平台", "部署", "客户演示"],
  "chapters": 4,
  "has_visual": true,
  "has_mindmap": true,
  "warnings": [],
  "files": {"docx": "out/产品周会 纪要.docx", "md": "out/minutes.md", "json": "out/minutes.json", "visual": "out/visual.json"}
}
```

## Python 库

```python
from minutecraft import MinuteEngine, Transcript, TranscriptTurn

transcript = Transcript(
    turns=[TranscriptTurn(start=0, end=3.2, text="部署我来跟进", speaker="0")],
    speaker_names={"0": "张三"},
    title="产品周会",
    created_at="2026-10-18T10:00:00",
)
result = MinuteEngine().build(transcript, template_id="meeting-minutes", out_dir="out")

result.summary   # {"minutes", "key_points", "chapters", "sections", "insights", "quote_kp", "visual"}
result.todos     # [{"task", "owner_hint", "due_hint"}]
result.keywords  # ["知识平台", ...]
result.mindmap   # markdown 层级大纲
result.files     # {"docx": ..., "md": ..., "json": ..., "visual": ...}
```

也可以在进程内复用已有的 LLM 客户端封装：`MinuteEngine().use_summarizer(my_summarizer)`
（对象只需提供 `summarize / extract_keywords / ask_json` 三个方法——EasyShare 知识服务就是这么接的）。

## 产物结构

完整定义见 `minutecraft/models.py` 的 `SCHEMA`，`minutecraft schema --json` 可导出。

| 字段 | 说明 |
| --- | --- |
| `summary.minutes` / `key_points` | 默认模板的纪要段与要点 |
| `summary.sections` | 非默认模板的小节结构（段落或 `- ` 列表） |
| `summary.chapters` | 智能切片：标题 + 起止秒 + 一句话概述 |
| `summary.insights` | AI 洞察：转写里没明写的判断与风险 |
| `summary.quote_kp` | 核心发言提炼：发言人 / 观点 / 逐字引用 |
| `summary.visual` | 可视化汇报卡：01-06 色卡模块（`layout` = cards/duo/flow/list/closing） |
| `summary.topics` | **报告议题**：长网页报告的正文骨架（议题 → 分节 → 要点 → 聚焦 → 洞察），独立一次调用产出 |
| `todos` | 待办一等实体：内容 / 负责人 / 期限 |
| `keywords` | 关键词 |
| `mindmap` | 三阶段 harness 产出的 markdown 大纲 |

> `visual`（色卡封面）与 `topics`（长图正文）是**两个不同产物**，别想着用一个套另一个：
> 前者是 6 个并列色块，后者是按时间轴的组织结构。

区块之间**互不牵连**：关键词或导图失败只进 `warnings`，纪要照常产出。

## Word 版式

仿钉钉 AI 听记的原生导出件（逐页实测其 PDF 后复刻）：

1. **封面**：「纪要」大标题 → 主题/时间/参与人 → 会议标题 + 一句话主题 → 分隔线 →
   01-06 带序号的彩色模块（配色 01/05 玫红、02 蓝、03 青、04 琥珀、06 绿），
   卡片含色块小标题、状态标签、要点、底色小结条 → 页脚水印
2. **文档体**：青绿章节标题 + 两级项目符号 → AI洞察 → 待办（方框复选框）→
   章节（时间戳 + 概述）→ 核心发言提炼（观点 + 灰斜体原话）→ 全局行动项汇总三列表格
3. **附页**（`--with-transcript`）：转写全文

## 环境变量

| 变量 | 说明 |
| --- | --- |
| `MINUTECRAFT_LLM_BASE_URL` / `_API_KEY` / `_MODEL` | OpenAI 兼容端点；回退读 `LLM_BASE_URL` / `LLM_API_KEY` / `LLM_MODEL` |
| `MINUTECRAFT_LLM_TRUST_ENV` | `false` 时直连、不走系统代理（内网端点常见需要） |
| `MINUTECRAFT_THINKING` | 思考档位 `auto`（默认）/`none`/`low`/`medium`/`high`。`auto` 按转写长短**自动降档**：≤3k 字 medium、3k-8k low、8k 字以上（约 1 小时会）none。`run --thinking` 可覆盖 |
| `MINUTECRAFT_SUMMARY_MAX_TOKENS` | 纪要单次调用的输出预算，默认 `32768`；给少了会 `finish_reason=length` 且内容为空 |
| `MINUTECRAFT_TOPICS_MAX_TOKENS` | 报告议题的输出预算，默认 `32768` |
| `MINUTECRAFT_LLM_RETRIES` / `_RETRY_BASE` | 429/5xx 的重试次数与退避基数，默认 `4` / `3s`（指数退避 3→6→12） |

## 为什么这么慢（实测数据，别再怀疑是「卡住了」）

一条完整链路上模型调用是**串行**的，而这份模型的**单次调用的延迟和输出长度成正比**——
它先思考、再写答案，思考也计费也计时。用一场 82 分钟、13.4k 字转写的真实会议实测：

| 步骤 | 调用次数 | 实测耗时（默认档） | 说明 |
| --- | --- | --- | --- |
| `summary` | 1 | **90-150s** | 回答本身就 >8192 token，预算给小了会白跑一次 |
| `keywords` | 1 | **~12s** | 短输出，快 |
| `topics` | 1 | **246-298s** | completion 24410 token = 思考 18803 + 输出 5607 |
| 导图 harness（仅 `run`） | 1 + 每议题 1 次 | **~194s**（5 议题 = 6 次） | 阶段二**逐议题串行**，议题越多越慢 |

**慢的四个真因**：

1. **推理模型的思考 token 占了大头**。测到的一次 `topics`：`completion_tokens=24410`，
   其中 `reasoning_tokens=18803`——真正写进答案的只有 5607 token（15267 字）。
   思考的时长由模型自己决定，**但你可以调档位让它少想**（见下节，这是唯一的解法）。
2. **输出预算不足会整段重来**。曾经把 `summary`/`topics` 设成 8192，模型写到 8192 被 `length` 截断、
   `content` 为空，于是这次调用全废，再发一次——**用户感受是「卡住两倍时间」**。现已默认 32768。
3. **导图 harness 是 N+1 次串行**。`run` 会额外跑三阶段管线：1 次切议题 + 每个议题 1 次深挖。
   5 议题就是 6 次调用 ≈ 194s。**两步走（`prepare`/`finish`）不跑它**，导图取 summary 回答里内联的大纲。
4. **429 限流按指数退避重试**（3→6→12s，最多 4 次）。免费/共享额度池打满时，这一项能凭空加十几秒到几十秒。

### 怎么变快：调「思考档位」，不是调预算（实测）

**唯一真正有效的一个开关就是 `thinking`**（翻成 OpenAI 的 `reasoning_effort`）。同一份 82 分钟转写：

| 档位 | topics | summary | keywords | 整条 build |
| --- | --- | --- | --- | --- |
| `high` | 246s | 90s+ | 12s | **4-6 分钟** |
| `low` | 113s | 16s | 3s | **124-187s** |
| `none` | **32s** | **27s** | **1.5s** | **62-69s** |

**默认是 `auto`：内容越多，档位越低。** 这条方向是实测定的，不是拍脑袋——

| 指标 | `low` | `none` |
| --- | --- | --- |
| 耗时（同一份转写） | 143-187s | **62-76s** |
| 议题 / 分节 / 要点 | 8-10 / 14-20 / 42-74 | 7-10 / 20 / **52-63** |
| 纪要正文 | 316 字（有时**返回空正文**，见下） | 273-316 字 |

长会和短会的诉求本来不同：短会（≤3 分钟）时间不值钱，让它多想清楚；**一小时的会本来就要等，再让它想 20 分钟只是纯浪费**。

**踩过的坑：`low` 档曾经返回过「合法 JSON 但正文全空」**——`minutes: ""`、`key_points: []`，
解析一路绿灯，最后产出 371 字节的 `minutes.md` 和一份空白 Word，**看起来像成功**。
现在 `_is_hollow_minutes` 会识别这种空壳并重试一次，仍为空才放弃——宁可重试，也不交空文档。

**关键事实：压「预算」压不出速度，只会压出空内容。** 把 `max_tokens` 从 32768 砍到 16384 不会让它想少点，
只会让它在思考阶段撞墙、`content` 变空（141s 白等）。要让它少想，**必须动档位**。
端点若不接受 `reasoning_effort`，`_create_with_retry` 会剥掉这个字段退回默认档重试，不会让整份纪要挂掉。

### 关于「分段并行」的实测结论

分段并行的诱人之处是「单议题只要 23s」。但**切分本身不省时间**：
「只切骨架」这种小任务实测仍要 21-60s、思考量照样大，因为模型要对**整场 13k 字**建立理解。
而且按时间硬切会让同一件事被两段各写一半，拼接处语义断裂（「半截议题」）。
**能省的是档位，不是切分**——先调 `--thinking`，再考虑分段。

## 长网页报告出图

`minutecraft render minutes.json -o report.png` 把纪要渲染成一页长图（头图 → 议题导航 → 逐议题分节 →
AI 洞察 → 待办），同时可出 PDF。版式数据与模板分离：模板是 `minutecraft/report/report.html`（数据文件，
`pyproject.toml` 里已声明 `package-data`，装包不会丢），装配逻辑是 `minutecraft/render.py` 的 `build_report`。

前置只要本机有 Chrome/Edge；找不到时退出码 3 并给出替代方案。长于 12000px 的报告分段截图后拼接（要 Pillow：
`pip install minutecraft[render]`）。字段口径与结构样板见 [`minutecraft/report/README.md`](minutecraft/report/README.md)。

## 给 Agent 的接入提示

```
1. minutecraft schema --json           # 先读结构，别猜字段
2. minutecraft run <转写文件> --out out/ --json
   - 退出码 3 且 stderr 提示「LLM 未配置」→ 先让用户配环境变量，不要重试
   - 结果里的 files 是绝对路径，可直接交给用户或后续工具
3. 要长图/PDF：minutecraft render out/minutes.json -o out/report.png
   要自己画页面就取 out/visual.json（色卡结构），不要解析 docx
```

## 测试

```bash
cd engine && python -m pytest -q          # 13 例：输入归一 / 产物形状 / 导出，全部离线
```

## 可选：顺带转写

```bash
pip install -e ".[asr]"                   # torch + funasr + modelscope（GB 级）
python -c "from minutecraft.asr import transcribe_wav; print(transcribe_wav('a.wav')[:2])"
```

## 与 EasyShare 的关系

`knowledge/app/meetings/{engine,summarizer,templates,mindmap_harness,docx_export}.py`
已改为薄壳转发到本工程，实现只有这一份。知识服务仍负责：录音采集、声纹、热词、
区块状态与任务队列、听记存储与权限——这些是产品态，不属于通用引擎。

## 打包分发（给别人 / 给 Agent 用）

需要整目录复制给外部时，用 [`../skills/minutes-from-transcript/`](../skills/minutes-from-transcript/)：
那是本工程的**发布副本 + Agent 说明书（SKILL.md）+ 出图渲染器（render/card.html）**。

`engine/` 是开发源；改完必须同步，否则复制出去的版本会落后：

```bash
python scripts/check_skill_sync.py           # 对账（CI 也跑）
python scripts/check_skill_sync.py --apply   # 同步到 skill 副本
```
