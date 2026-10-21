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
| 纯文本（带时间戳） | `张三 [00:12] 部署我来跟进`<br>`[00:12] 部署我来跟进` | 自动识别说话人与 `mm:ss` / `hh:mm:ss` |
| 纯文本（起止区间） | `[00:00 - 00:03] 好的好的,`<br>`张三 [00:03 - 00:12] 部署我来跟进`<br>`00:03 - 00:12 部署我来跟进` | **ASR 分段导出最常见**：分隔符支持 `- – — ~ ～ --> 至 到`，时间支持 `mm:ss` / `hh:mm:ss` / 带毫秒；括号可选 |
| 纯文本（括号说话人） | `[说话人 1] 部署我来跟进`<br>`【张三】部署我来跟进` | 说话人标签会先校验（角色名/2-4 字中文名/英文名），正文里的中文冒号不会被误认成说话人 |
| 纯文本（冒号） | `张三：部署我来跟进` | 没有时间戳时按行序，时间记 0 |
| SRT / VTT | `00:00:01,200 --> 00:00:04,500` | 字幕导出直接可用（起止时间都保留） |
| JSON 记录 | `{"segments": [...], "speaker_names": {...}}` | EasyShare 听记记录原样丢进来即可 |
| 标准输入 | `cat a.srt \| minutecraft run -` | 管道友好 |

> 转写材料默认会被压缩再喂模型：合并同一说话人的连续短句、丢掉「嗯/行嘞行/好的好的」这类
> 语气词碎句，只在段首保留时间戳。逐秒切段的 ASR 转写通常能省掉 3/4 的输入 token。
> 要原样逐行喂，加 `--full-text`。

## 命令行

```
minutecraft run <输入> [--out DIR] [--template ID] [--title T]
                       [--no-keywords] [--no-mindmap] [--no-docx] [--no-topics]
                       [--full-text] [--with-transcript] [--json]
minutecraft prepare <输入> -o request.json     # 只组装提示词，不调模型（另写 request.prompts/）
minutecraft prepare <输入> -o req.json --split-topics  # 长会两阶段：只出议题大纲
minutecraft prepare <输入> -o req.json --split-topics --lean-outline  # 实验项：大纲腿只喂骨架材料
minutecraft expand req.json --outline outline.json -o expand.json  # 大纲 → 每议题一个扩写请求
minutecraft prepare/expand ... [--density auto|detailed|concise] [--output-bps 字节/秒]
                                  # 议题正文的密度档；auto 按宿主实测吞吐判（默认 auto）
minutecraft finish <请求包> --answers a.json   # 只解析回答与导出，不调模型（answers 支持 "@文件"）
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
| `summary.topics` | **报告议题**：长网页报告的正文骨架（议题 → 分节 → 要点 → 聚焦 → 洞察），独立一次调用产出；长会可用 `--split-topics` 拆成「大纲 + 每议题并行扩写」 |
| `summary.chapters`（派生） | 两步走开启 topics 时由议题的 title/start/end/gist 派生，不再单独问模型 |
| `mindmap`（派生） | 两步走开启 topics 时由议题分节分组派生（`## 议题` → `- 分组` → `  - 要点`），待办/洞察单独成支 |
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
| `keywords` | 1 | **~12s** | 短输出，快；**两步走已并入 summary，不再单独调用** |
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

### Agent 两步走模式的四项提速（默认已开）

上面那张表是 `run`（自带客户端、串行调用）的账；Agent 的 `prepare`/`finish` 模式另有四个更直接的杠杆：

1. **材料压缩**（`Transcript.render(compact=True)`，默认）：合并同一说话人的连续短句、丢弃
   「嗯/行嘞行/好的好的」这类纯语气词碎句、只在段首保留时间戳。逐秒切段的 ASR 转写实测
   33.8k 字 → 6.7k 字（省约 80% 输入 token），模型也少受噪音干扰。要原样喂用 `--full-text`。
2. **关键词并入 summary**：请求从 3 个降到 2 个（`summary` / `topics`），省掉一次完整转写的
   输入和一个 Agent 来回；`finish` 仍然接受单独补答的 `answers.keywords`。
3. **章节/导图不再重复写**：开启 topics 时 summary 提示词里去掉 `chapters` 与 `mindmap`，
   `finish` 用议题的 title/start/end/gist 派生章节、用分节分组派生导图。实测这两项占 summary
   回答约 1/3 的 output token——**Agent 的墙钟时间主要由 output token 决定**，这一刀直接砍在
   最贵的地方。宿主若仍主动给了这两项，以模型原文为准。
4. **summary 与 topics 并发**：两者互不依赖，`prepare` 会在 `plan.parallel` 里明确写出，
   总耗时从相加变成取最大值；只要 Word/MD 不要长图时加 `--no-topics`，直接省掉 246-298s 的那次调用。

`prepare` 还会把每个请求的 system/user 写成 `request.prompts/*.txt`，`finish` 的 `answers`
支持 `"@文件"`：Agent 不必把 request.json 整份读进上下文，也不必把模型几万字的原始输出
重新序列化进 answers.json——这两处省的是**上下文与 output token**，不影响产物质量。

### 长会：两阶段议题（`--split-topics`，可选）

单次 topics 的耗时随会议时长线性上涨（82 分钟约 4-5 分钟），输出越长越容易撞 token 上限。
`prepare --split-topics` 把它拆成：

1. **大纲**：只切 4-8 个议题（title/start/end/gist），输出几十行；
2. **扩写**：`expand` 为每个议题切出「时间窗 ± 30 秒」的转写，生成一个只写
   `focus / sections / insights` 的小请求；请求之间互不依赖，可全部并发。

支持并发的宿主墙钟 ≈ 大纲 + 最慢的一个议题，且单个议题的输出更不容易被截断；
**串行宿主会更慢**，所以默认关闭。合并时标题与起止以大纲为准，避免十个请求各写一套时间轴；
某个 `topics#N` 没答只会让该议题退回大纲，警告点名到具体键。

**什么时候该用它，`prepare` 自己说**：压缩后材料 ≥`SPLIT_RECOMMEND_MIN_CHARS`（9000）字、或会议
≥`SPLIT_RECOMMEND_MIN_MINUTES`（35）分钟且本次走单段 topics 时，`plan.recommend` 写 `split-topics`、
`plan.note` 与 CLI 的 `next` 第一行（`[建议]` 开头）同步点名。阈值不是拍脑袋：一次 59 分钟会
（压缩后 15960 字 / 71 行）在 32K 级窗口的宿主子代理上跑单段 topics，**上下文耗尽、零产出**，
白烧 25 分钟才回头重走两阶段。**只建议、不自动切**——短会少一次往返更快，而自动改默认路径会让
快路随阈值漂移。

**实验项 `--lean-outline`（默认关）**：大纲腿的材料换成 `Transcript.render_skeleton()`——每段只留
「说话人 + 段首时间戳 + 开头句」。一次 59 分钟会的大纲请求实测是 **44KB 输入换 1.4KB 输出**，
全量材料买回来的只是切分锚点；两份样例的骨架材料比压缩材料再少 68% / 79%
（`material.outline_chars` 报的就是这个数）。扩写腿照旧吃完整时间窗，所以代价**只可能是议题切边
位置**，不伤正文。没跑过多场 A/B 之前不要设成默认。

### 议题正文的密度档：`--density` / `--output-bps`

宿主的墙钟几乎全花在**输出**上（实测一次 59 分钟会的 summary 腿：9604 字节 / 13.5 分钟
≈ 12 字节/秒）。所以「报告写多长」不该让人每次回忆开关，而应按自家吞吐自动定：

| 档 | 单议题正文 | 口径 |
| --- | --- | --- |
| `detailed` | 800-1400 字 | 原口径：按议题时长给 1-4 分节、每组 2-3 条 30-90 字整句 |
| `concise` | 200-400 字 | sections 1-2、groups 1-2、每组 1-2 条 **≤45 字**，focus 只 1 条、insights 0-1 条 |
| `auto`（默认） | — | 见下面的判定顺序 |

**只收议题正文**：`summary` 纪要、待办、关键词一个字都不减——那是 docx/md 的骨架，砍它就是砍结论。
两档共用同一套不变量（JSON 形状、`warm` 高亮、反引号数字、空标题禁令、不编造），
差别只在条数与句长，由 `test_both_topics_prompts_keep_the_shared_invariants` /
`test_both_expand_prompts_keep_the_shared_invariants` 钉住。少条数**不是漏答**：`finish` 不因此出警告，
出图也不留空框（`minutecraft/render.py` · `build_report` 本来就是「有则画、没有不画」）。

`auto` 的判定顺序（`engine.py` · `resolve_density`）：**显式指定 > 沿用 prepare 的显式指定 >
实测吞吐（本次传的 `--output-bps`，或 prepare 记在 `plan.output_bps` 里的那个）> 材料规模**。
吞吐单位取**输出字节/秒**——宿主只需 `(Get-Item 回答文件).Length ÷ 该腿秒数`，
不用自己换算 token（UTF-8 下一个中文字约 3 字节）。阈值 `DENSITY_DETAILED_MIN_BPS`（24 字节/秒 ≈ 8 字/秒）：
高于它详细档不会把墙钟拖长，低于它详细档会把最慢一腿拖到十分钟以上，直接改出精简档。
拿不到吞吐时按规模保守判：长会（≥9000 字或 ≥35 分钟）先精简，短会保持详细。
**吞吐只报一次**：`expand` 会接着用 prepare 记下的值，否则同一场会会在第二阶段悄悄变档
（冒烟实测踩过：`prepare --output-bps 12` 判 concise，`expand` 却回退成「吞吐未知」按规模重判）。
判定结果与**理由**都写进 `plan.density` / `plan.density_reason`，并顶到 CLI 的 `next`
（`[正文] concise 档：…`）——Agent 照着 next 做，看不到就会自己加戏写长。

```bash
minutecraft prepare meeting.txt -o req.json --split-topics          # 先按规模保守判一档
minutecraft expand req.json --outline outline.json -o exp.json --output-bps 12   # 宿主自测吞吐后重判
minutecraft prepare meeting.txt -o req.json --density detailed      # 明确要详细的，谁也别自动改
```

环境变量 `MINUTECRAFT_OUTPUT_BPS` 可以设一次不用每次传；值非法就当没给（退成按规模判），
不能让一个手滑的环境变量把链路搞挂。

### 关于「按时间硬切」的实测结论

盲目按时间切片仍然不划算：切点会把同一件事拆成两半，拼接处语义断裂（「半截议题」），
而「只切骨架」这件小事实测也要 21-60s——模型仍要对整场会议建立理解。
两阶段之所以有意义，不是「切得越碎越快」，而是**第一阶段先把议题边界定准**，
第二阶段每个请求只读自己那段、只写自己的正文，从而把一次超长输出换成 N 个可并行的短输出。
不能并发的宿主，先调 `--thinking` 仍然是最有效的单点提速。

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
cd engine && python -m pytest -q          # 53 例：输入归一 / 产物形状 / 两步走 / 导出，全部离线
```

> 从仓库根目录跑时用 `PYTHONPATH=engine python -m pytest engine/tests -q`：
> 本机若装过旧版 `minutecraft`（editable），直接 `pytest` 会导入旧包导致假失败。

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
python scripts/check_skill_sync.py           # 对账（本地跑，不在 CI）
python scripts/check_skill_sync.py --apply   # 同步到 skill 副本
```
