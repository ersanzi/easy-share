---
name: minutes-from-transcript
description: 把「已经转写好的会议文本」变成可交付的会议纪要：纪要/要点/待办/章节/关键词/AI 洞察/核心发言/思维导图/可视化汇报卡，并导出钉钉版式的 Word、Markdown 与汇报卡图。当用户给了一段会议转写（txt/srt/json，含 `[00:00 - 00:03]` 这类起止区间），要求「整理成会议纪要」「导出采访记录」「把录音文字做成文档」时使用。本 skill 不处理音频，只吃文本；不要求任何 API key，模型能力由你自己提供。
---

# 转写文本 → 会议纪要

**这个 skill 不需要你配置 LLM。** 你有模型能力，就用它回答本 skill 给出的提示词；
真正干活的部分（解析、归一、排版、导出）是纯工程的，不需要任何密钥。

工作方式分两半：

```
你（Agent）          做：判断环境、安装、读提示词文件、把模型原始输出写回文件
本 skill（工程）     做：解析输入、组装提示词、解析回答、导出 Word/MD/JSON/汇报卡
```

**省上下文的两条硬规矩**（Agent 场景下最容易把速度拖垮的地方）：

1. **不要读 `request.json`**。它包含完整转写与两份提示词；`prepare` 已经把每个请求的
   system/user 写成独立文件，直接读那些文件即可（见第 2 节）。
2. **不要把模型输出重新抄进 `answers.json`**。把原始输出写进文件，`answers.json` 用
   `"@相对路径"` 引用即可，省一遍几万字的 output token，也不会在 JSON 转义上出错。

## 0. 先判断环境（30 秒）

```bash
python --version      # 需要 3.10+
```

**没有 Python 或版本低于 3.10 → 先装，别跳过。**
- Windows：`winget install Python.Python.3.12`（装完重开终端）
- macOS：`brew install python@3.12`
- Debian/Ubuntu：`sudo apt install python3 python3-pip python3-venv`

**本 skill 不需要 torch、不需要下载模型、不需要 GPU**——它不做语音识别。

## 0b. 输入是 .docx 时：必须用 python-docx 取全文

会议转写常以 Word 形式给出。**别用会截断段落的预览工具读它**——实测踩过：
某个读取工具的「段落预览」会把 214 字的段落只回 74 字，看着像读完了，实际只拿到前三分之一，
后面切出来的议题自然就缺内容。转写是要逐字喂给模型的东西，**必须取全文**：

```bash
python -c "import docx,sys;print('\n'.join(p.text for p in docx.Document(sys.argv[1]).paragraphs))" 转写.docx > meeting.txt
```

需要的话先 `pip install python-docx`（本 skill 的依赖里已含），或 `python -m pip install python-docx`。
取完**核对字数量级**：`wc -m meeting.txt` 与实际会议时长对得上再往下走。

## 1. 安装（一次）

```bash
cd <本 skill 目录>/scripts
python -m pip install -e .
minutecraft --version     # 期望：minutecraft 0.1.0
```

装不上时用一键脚本：`bash install.sh`（或 Windows `powershell -ExecutionPolicy Bypass -File install.ps1`）。
`minutecraft` 不在 PATH 时用 `python -m minutecraft` 代替。

## 2. 三步出纪要（推荐，零配置）

### 第 1 步：让工程把「要问什么」准备好

```bash
minutecraft prepare meeting.txt -o request.json --title "产品周会"
```

**这一步不联网、不需要 key。** 它会同时写出提示词文件：

```
request.json                     ← 请求包（finish 要用；你不要读它）
request.prompts/
├── summary.system.txt           ← 下给模型
├── summary.user.txt             ← 下给模型（含压缩后的转写）
├── topics.system.txt
└── topics.user.txt
```

输出里的 `prompt_files` 就是这些路径。**把每个文件的 system/user 原样作为一次对话的
system 与 user 消息**，不要自己改提示词、不要把文件内容再复述进对话。

输出里还有两项，看一眼就知道这次成本：

- `material`：`{"chars", "lines", "turns", "compact"}`——实际喂给模型的字数。
  默认对材料做了压缩（见下面「速度」一节），`chars` 通常只有原始转写的 1/2 到 1/5。
- `plan`：`{"calls", "parallel", "recommend", "recommend_reason", "density", "density_reason",
  "density_requested", "output_bps", "note"}`——请求之间互不依赖时
  `parallel=true`。

**`plan.recommend == "split-topics"` 时必须照做**（`next` 第一行会以 `[建议]` 开头点名）。
这条是实测踩出来的：压缩后材料 ≥9000 字、或会议 ≥35 分钟时，单段 `topics` 的账是
「材料 + 提示词 ~10k tokens + 子代理自身开销 ~8k + 输出 6-10k tokens」，在 32K 级窗口的宿主上
会**把子代理上下文撑爆、跑完零产出**——实测一次 59 分钟会白烧 25 分钟才回头重走两阶段。
慢可以忍，跑完发现什么都没有不能忍。做法见第 3 节。

### 速度：慢在哪、怎么快（默认就是快的那条路）

| 措施 | 效果 | 怎么关 |
| --- | --- | --- |
| **材料压缩**：合并同一说话人的连续短句、丢掉「嗯/行嘞行/好的好的」这类语气词碎句，只在段首留时间戳 | 逐秒切段的 ASR 转写通常从 3 万字降到 6-7 千字（约省 4 倍输入 token） | `--full-text` |
| **关键词并入 summary**：不再单独为关键词发一次完整转写 | 模型调用从 3 次降到 2 次，Agent 少一个来回 | `--no-keywords` 连关键词也不要 |
| **章节/导图不重复写**：summary 只写纪要与汇报卡，`chapters` / `mindmap` 由 `finish` 从 topics 派生 | summary 回答少写约 1/3 的 output token（**Agent 慢主要慢在 output token**） | `--no-topics` 时自动恢复让模型写 |
| **两个请求并发**（`plan.parallel`） | 总耗时 ≈ max(summary, topics)，而不是相加；topics 最慢（82 分钟会约 250-300s） | — |
| **只要 Word/Markdown 时跳过 topics** | 直接省掉最慢的一次调用 | `--no-topics` |
| **长会两阶段议题**（见第 3 节） | 大纲 + 每议题并行扩写，墙钟 ≈ 大纲 + 最慢的一个议题；材料 ≥9000 字或 ≥35 分钟时 `plan.recommend` 会点名 | 默认关闭，加 `--split-topics` 开启 |
| **大纲腿只喂骨架材料**（实验项） | 大纲只要「时间锚点 + 每段首句」，输入再砍 60%-80%（两份样例实测 68% / 79%） | 默认关闭，加 `--lean-outline`（配合 `--split-topics`）开启 |
| **议题正文按宿主吞吐定档**（见下面「正文档位」） | 慢宿主自动出精简档：单议题 800-1400 字 → 200-400 字，最慢一腿的输出时间同比缩短 | `--density detailed\|concise` 强制 |

**Agent 必须做的一件事：把 summary 和 topics 并发发出**，不要「先答 summary、再答 topics」。
一条消息里发两个请求（或两个并行工具调用）通常比串行快一倍以上。

### 正文档位：报告写多长由吞吐决定，不用你猜

墙钟几乎全花在**输出**上。实测一次 59 分钟会的 summary 腿：9604 字节 / 13.5 分钟 ≈ **12 字节/秒**——
这种吞吐下让模型按详细档写，8 个议题就是八条十分钟以上的腿。所以档位由引擎判：

- `prepare` / `expand` 的 `plan.density` 会写明本次用 `detailed` 还是 `concise`，
  `plan.density_reason` 说清为什么，CLI 的 `next` 第一行以 `[正文] …` 开头点名。**照着答，别自己加戏**：
  提示词已经决定了条数与句长，你在对话里再催「写详细点」只会把最慢一腿拖长。
- **拿到自己的输出速度就告诉它，一次就够**：跑完任意一条腿，用 `(Get-Item 回答文件).Length ÷ 该腿耗时秒`
  （类 Unix `wc -c`）算出字节/秒，`prepare`（或 `expand`）加 `--output-bps 12`。
  ≥24 字节/秒出详细档，更低出精简档；这个值记在 `plan.output_bps` 里跟着请求包走，
  `expand` 会接着用，不用重报。也可以设一次环境变量 `MINUTECRAFT_OUTPUT_BPS`。
- 没测吞吐时按材料规模保守判：**长会（≥9000 字或 ≥35 分钟）先精简，短会保持详细**。
  觉得判歪了就 `--density detailed` / `--density concise` 显式指定——显式永远压过自动，
  且 prepare 显式指定过、`expand` 会沿用，同一场会不会前后跑出两种版式。
- **精简档只收议题正文**：`summary` 纪要、待办、关键词一个字都不减。少条数不是漏答，
  `finish` 不因此出警告，出图也不留空框。

### 第 2 步：用你自己的模型回答

每个请求各按 `prompt_files` 里的 system + user 组织一次对话，**把模型返回的原始文本
原样写入文件**，再用 `answers.json` 引用：

```bash
# 例：把两个回答分别写盘（路径任意，相对 answers.json 所在目录）
# out/summary-answer.json
# out/topics-answer.json

cat > answers.json <<'EOF'
{
  "summary": "@summary-answer.json",
  "topics":  "@topics-answer.json"
}
EOF
```

| 键 | 作用 | 不答会怎样 |
| --- | --- | --- |
| `summary` | 纪要/要点/待办/关键词/洞察/汇报卡（`keywords` 就在这条回答里） | **必填**，缺了直接失败 |
| `topics` | **长网页报告的正文骨架**（4-8 个议题，每个带分节、讨论聚焦、洞察） | 长图回退成「按章节均分」的粗版式，并记一条警告；**章节与导图也一起没有来源** |

要点：
- **原样保存**，不要自己解析、不要修正格式——解析和兜底由第 3 步负责
- 值可以是 `"@文件路径"`（推荐）、直接字符串、或已解析的对象；`@` 相对路径以
  `answers.json` 所在目录为准
- 模型输出带 ```` ```json ```` 围栏也没关系，能识别
- **关键词默认就在 `summary` 回答的 `keywords` 字段里**，不用单独回答；
  只有 summary 漏了关键词、想补跑时，才额外给一个 `keywords` 键（值可以是裸数组 `["a","b"]`）
- 如果模型没返回可解析的 JSON，可以重新提问一次（提示它「只输出 JSON，不要解释」），实在不行让用户知道这一步失败了
- `topics` 是**一次独立的调用**：别把它塞进 summary 里，塞进去容易把整份纪要顶到 token 上限截断
- `topics` 的输出比 summary 长得多（82 分钟的会约 1 万 token），**给它留足输出预算**：OpenAI 兼容接口设 `max_tokens` ≥ 32768。推理型模型的思考也占这份预算，预算给小了会内容为空、只留下思考——那是穷尽预算，不是限流
- `topics` 要**照提示词的篇幅口径给**：详细档按议题时长给 1-4 个分节、每组 2-3 条 30-90 字整句；
  精简档就按它写的「1-2 分节、每组 1-2 条 ≤45 字」。**两档都怕同一件事**：只给一个分节、
  句子写成名词短语，出来就是「只有标题的空壳」——精简档收的是条数和句长，不是收掉结论
- **降级会让退出码变 4**：`exit 0` = 产物齐全；少答了 `topics` 会返回 **4** 并把警告打到 stderr（`--json` 里的 `warnings` 同内容）。**4 不是失败，别整条重跑**——补答那一项再 `finish` 即可

### 用并发子代理回答时的三条落盘规矩（实测踩过）

「子代理汇报已写入」**不等于**文件落盘。实测 8 路并发扩写时，`finish` 早跑了一次直接
`exit 2`（`topic-1.json` 还没写出来），最慢那路又过了 12 分钟才落盘。

1. **要子代理回执实测字节数，不要它自报**：提示词里写清「写完后执行
   `Test-Path <文件>` 与 `(Get-Item <文件>).Length`（类 Unix 用 `wc -c`），把路径与字节数原样回给我」。
   自报的字节是模型记的数，实测的才是磁盘上的。
2. **`finish` 前对账一次**：列目录逐个请求确认「文件在、长度非零」，缺哪个补哪个；
   不要因为一个没落盘就判「答案丢了」而重跑整场会。
3. **重新 `prepare` 换模式时，先比对提示词**：`request.prompts/summary.system.txt` 与
   `summary.user.txt` 若与上次**逐字节相同**，上次的 summary 回答可以直接复用——
   实测这样省掉一整腿 13.5 分钟。材料一样、只是 topics 改成两阶段时必然相同。

### 第 3 步：交给工程导出

```bash
minutecraft finish request.json --answers answers.json -o ./out --json
```

产物（`./out/`）：

| 文件 | 内容 |
| --- | --- |
| `会议标题 纪要.docx` | 钉钉版式 Word：封面色卡 + 文档体 + AI洞察 + 待办 + 章节 + 行动项表 |
| `minutes.md` | 全量 Markdown（章节与导图由议题派生） |
| `minutes.json` | 结构化结果（含 `visual` 与 `topics`） |
| `visual.json` | 色卡封面结构，直接喂给 `render/card.html` |
| `report.json` | 长网页报告的现成数据，`minutecraft render` 直接吃 |

**三步都不需要任何 API key。**

## 3. 长会可选：两阶段议题（大纲 → 每议题并行扩写）

单次 topics 调用在长会上要几分钟，输出越长越容易被截断；**材料一大还会撑爆子代理上下文**
（见第 2 节 `plan.recommend`）。可以拆成两阶段：

```bash
minutecraft prepare meeting.txt -o request.json --split-topics
# 第 1 步只产出 summary 与 topics_outline（大纲：title/start/end/gist，几十秒）

# 用模型回答 summary 与 topics_outline 后，把大纲回答存成 outline-answer.json
minutecraft expand request.json --outline outline-answer.json -o expand.json
# → expand.prompts/topics-1..N.{system,user}.txt，每个只带该议题时间窗

# 并发回答所有 topics#N，然后：
cat > answers.json <<'EOF'
{"summary": "@summary-answer.json",
 "topics#1": "@topic-1.json",
 "topics#2": "@topic-2.json"}
EOF
minutecraft finish expand.json --answers answers.json -o ./out --json
```

- 每个扩写请求只看自己议题的转写（前后各留 30 秒上下文，`--margin` 可调），输出只有
  `focus / sections / insights`；标题与起止以大纲为准，合并时不会串时间轴
- **只有宿主能真正并发时才划算**；串行回答 N 个议题会比单次 topics 更慢
- 某个 `topics#N` 没答：该议题退回大纲（只有标题/时间/gist），警告会点名到具体的键；
  补答那一个再 `finish` 即可，不用重跑整场会
- **档位在这里定最终值**：`expand` 会沿用 `prepare` 显式指定过的 `--density`；没指定过就用
  `expand --output-bps <字节/秒>`（跑完 summary 或大纲腿之后自己测）让它判。长会 + 慢宿主
  落到 `concise` 是好事：每条扩写腿的输出量按同比例降下来，而这正是墙钟的大头
- **实验项 `--lean-outline`**（配合 `--split-topics`）：大纲腿只喂「说话人 + 段首时间戳 + 每段首句」
  的骨架材料，`material.outline_chars` 会告诉你实际喂了多少字（两份样例实测比全量少 68% / 79%）。
  实测一次 59 分钟会的大纲腿是 **44KB 输入换 1.4KB 输出**，全量材料换来的只是切分锚点。
  **代价只可能是议题切边位置**——扩写腿照旧吃完整时间窗，正文质量不受影响。
  默认关闭：切边质量还没做过多场 A/B，自己开之前先拿一场会对比议题数与边界，别顺手设成默认。

## 4. 要出图（两种图，别混）

**① 长网页报告（推荐，一份 PNG/PDF 交差）**——命令行直接出图，只要本机有 Chrome/Edge：

```bash
minutecraft render out/report.json -o out/report.png --pdf out/report.pdf
```

不用 Node、不用 npm、不用联网。模板与渲染代码在 `scripts/minutecraft/report/`。
找不到浏览器的脚本见 [`render/README.md`](render/README.md)（`--browser` 或手动截图）。
出的是一张**四壁完整的长图**：头图 → 议题导航 → 逐议题（时间戳/摘要/分节要点）→ AI 洞察 → 待办。
喂 `minutes.json` 也行，渲染时现场装配；喂 `report.json` 则直接用现成数据。

**前置：长报告要装 Pillow。** 报告超过 12000px 会分段截图再纵向拼接，那一步用 Pillow：
`pip install pillow`（或 `pip install 'minutecraft[render]'`）。没装会在**截图之前**就报错并给出命令，
也可以改用 `--pdf` 绕过拼接。短报告不需要它。

**② 色卡封面**——浏览器打开 `render/card.html`，把 `out/visual.json` 拖进去 → 渲染 → 打印/存 PDF。
它画的是钉钉那种 01-06 彩色模块封面，和 ① 是两个不同产物（`card.html` 与桌面端组件是同一版式）。

`visual.json` 结构：`sections[].layout` 取 `cards|duo|flow|list|closing`，
`accent` 取 `rose|blue|teal|amber|green`（钉钉原生件 01/05 同为玫红）。

## 5. 可选：无人值守模式

需要批量跑、没人盯着时，可以让工程自带 LLM 客户端：

```bash
export MINUTECRAFT_LLM_BASE_URL=... MINUTECRAFT_LLM_API_KEY=... MINUTECRAFT_LLM_MODEL=...
minutecraft run meeting.txt --out ./out --json
```

**这不是必需路径。** 交互式使用一律走第 2 节的三步，避免多一处密钥落点。

## 6. 退出码（**别重试错的东西**）

| 退出码 | 含义 | 怎么办 |
| --- | --- | --- |
| 0 | 成功，产物齐全 | 继续 |
| 2 | 输入解析不出来 / 请求包、大纲或回答文件格式不对（含 `@文件` 不存在） | 检查文件，别重试同样的输入 |
| 3 | 生成失败（回答无法解析、模板不存在等） | 看 stderr 的具体原因 |
| **4** | **产物已出，但有降级**（缺 topics / 缺 keywords / 某个 topics#N 没答…） | **不算失败，别整条重跑**；按 stderr 里列出的警告**补答那一项**再 `finish` |

`finish` 返回 3 且提示「summary 回答无法解析」时，**回到第 2 步重新问模型**，通常是模型没按 JSON 输出。
`render` 返回 3 且提示「找不到 Chrome/Edge」时，**不要去重试渲染**——换浏览器路径，或把 HTML 交给用户手动截图。
返回 4 时 stderr 会把每条警告原样打出来（JSON 里 `warnings` 同内容），照着补即可。

## 7. 遇到问题

| 现象 | 原因与解法 |
| --- | --- |
| `minutecraft: 未找到命令` | 用 `python -m minutecraft` |
| `不是有效的请求包` | `request.json` 不是 `prepare` 产出，重新生成 |
| `answers.summary 引用的文件不存在` | `@路径` 写错；相对路径以 `answers.json` 所在目录为准 |
| `finish` 返回 **2** 且提示某个 `@文件` 不存在 | **先对账再判断**：并发子代理「汇报已写入」不等于落盘。列目录确认文件与长度，缺哪个补哪个，别当成答案丢了去重跑整场会（见第 2 节落盘规矩） |
| topics / 大纲子代理跑完**零产出**、或报上下文耗尽 | 单段材料撑爆了子代理窗口（压缩后 ≥9000 字或会议 ≥35 分钟基本必踩）。改用 `prepare --split-topics`；重 prepare 后先比对 `summary.*.txt` 提示词，逐字节相同就别重答 summary |
| `缺少 summary 回答` | `answers.json` 里键名必须是 `summary` |
| `summary 回答无法解析为纪要结构` | 模型没输出 JSON；重问并强调「只输出 JSON」 |
| `议题大纲无法解析` | `expand --outline` 收到的不是大纲回答；确认文件内容是 `{"topics":[...]}` 或带围栏的同类 JSON |
| 警告点名 `topics#2` | 只补答该议题的扩写文件，改 `answers.json` 对应键后再 `finish` |
| 转写是 `[00:00 - 00:00] 嗯,` 这种逐秒区间 | 已支持：整份材料会丢掉语气词、合并同说话人短句；正文里的中文冒号不会再被误认成说话人 |
| 传了区间格式但一句都没解析出来 | 确认每行是「区间 + 文本」；分隔符支持 `- – — ~ ～ --> 至 到`，时间支持 `mm:ss` / `hh:mm:ss`；说话人可写 `张三 [00:00 - 00:03]`、`[张三]`、`张三:` |
| Word 打不开 | `python -c "import docx"` 检查依赖 |
| 中文文件名报错 | 先 `mkdir` 输出目录；标题里的 `/ \ : * ? " < > \|` 会被自动去掉 |
| 长会议提示词很长 | 正常，超过约 45000 字的转写会自动分段浓缩（那是 `run` 模式的事，三步模式下由你处理） |

## 边界（别越界承诺）

- **不处理音频**：只吃转写后的文本。要转写请另找 ASR 工具
- **不含声纹 / 热词 / 实时上屏**：那些是产品态能力
- **三步模式不跑三阶段导图**：`finish` 用 summary 回答里的 `mindmap`；开启 topics 时
  由议题派生（分节分组 → 层级大纲）。需要多轮 harness 导图请用 `run` 模式
- **出图需要本机浏览器**：`render` 用系统 Chrome/Edge 截图；纯 LLM、无执行环境的 Agent 只能读 JSON 自己渲染
- **不上传任何数据**：三步模式全程只在你和模型之间传文本，产物落在本地
