---
name: minutes-from-transcript
description: 把「已经转写好的会议文本」变成可交付的会议纪要：纪要/要点/待办/章节/关键词/AI 洞察/核心发言/思维导图/可视化汇报卡，并导出钉钉版式的 Word、Markdown 与汇报卡图。当用户给了一段会议转写（txt/srt/json）、要求「整理成会议纪要」「导出采访记录」「把录音文字做成文档」时使用。本 skill 不处理音频，只吃文本；不要求任何 API key，模型能力由你自己提供。
---

# 转写文本 → 会议纪要

**这个 skill 不需要你配置 LLM。** 你有模型能力，就用它回答本 skill 给出的提示词；
真正干活的部分（解析、归一、排版、导出）是纯工程的，不需要任何密钥。

工作方式分两半：

```
你（Agent）          做：判断环境、安装、把提示词交给你的模型、保存回答
本 skill（工程）     做：解析输入、组装提示词、解析回答、导出 Word/MD/JSON/汇报卡
```

## 0. 先判断环境（30 秒）

```bash
python --version      # 需要 3.10+
```

**没有 Python 或版本低于 3.10 → 先装，别跳过。**
- Windows：`winget install Python.Python.3.12`（装完重开终端）
- macOS：`brew install python@3.12`
- Debian/Ubuntu：`sudo apt install python3 python3-pip python3-venv`

**本 skill 不需要 torch、不需要下载模型、不需要 GPU**——它不做语音识别。

## 0. 输入是 .docx 时：必须用 python-docx 取全文

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

`request.json` 里有 `requests.summary`（system + user 提示词），还有 `requests.keywords` 与 `requests.topics`。
**这一步不联网、不需要 key。**

### 第 2 步：用你自己的模型回答

每个 request 各按 system+user 组织一次对话，**把模型返回的原始文本原样保存**到 `answers.json`：

```json
{
  "summary":  "<对 requests.summary 的原始输出，必填>",
  "keywords": "<对 requests.keywords 的原始输出，建议回答>",
  "topics":   "<对 requests.topics 的原始输出，建议回答>"
}
```

| 键 | 作用 | 不答会怎样 |
| --- | --- | --- |
| `summary` | 纪要/要点/待办/章节/洞察 | **必填**，缺了直接失败 |
| `keywords` | 关键词 | 跳过关键词，只记一条警告 |
| `topics` | **长网页报告的正文骨架**（4-8 个议题，每个带分节、讨论聚焦、洞察） | 长图回退成「按章节均分」的粗版式（议题只有标题和时间、没有分节正文），并记一条警告 |

要点：
- **原样保存**，不要自己解析、不要修正格式——解析和兜底由第 3 步负责
- 值写成**字符串**最稳；直接写解析好的对象也行（会重新序列化后再解析），`keywords` 给裸数组 `["a","b"]` 也会被识别
- 模型输出带 ```` ```json ```` 围栏也没关系，能识别
- 如果模型没返回可解析的 JSON，可以重新提问一次（提示它「只输出 JSON，不要解释」），实在不行让用户知道这一步失败了
- `topics` 是**一次独立的调用**：别把它塞进 summary 里，塞进去容易把整份纪要顶到 token 上限截断
- `topics` 的输出比 summary 长得多（82 分钟的会约 1 万 token），**给它留足输出预算**：OpenAI 兼容接口设 `max_tokens` ≥ 32768。推理型模型的思考也占这份预算，预算给小了会内容为空、只留下思考——那是穷尽预算，不是限流
- `topics` 要**贴着篇幅给**：10 分钟以上的议题图个 2-4 个分节、每分节 2-4 组、每组 2-3 条 30-90 字的完整句子。只给一个分节、句子写成名词短语，出来就是一份「只有标题的空壳」
- **降级会让退出码变 4**：`exit 0` = 产物齐全；少答了 `topics`/`keywords` 会返回 **4** 并把警告打到 stderr（`--json` 里的 `warnings` 同内容）。**4 不是失败，别整条重跑**——补答那一项再 `finish` 即可

### 第 3 步：交给工程导出

```bash
minutecraft finish request.json --answers answers.json -o ./out --json
```

产物（`./out/`）：

| 文件 | 内容 |
| --- | --- |
| `会议标题 纪要.docx` | 钉钉版式 Word：封面色卡 + 文档体 + AI洞察 + 待办 + 章节 + 行动项表 |
| `minutes.md` | 全量 Markdown |
| `minutes.json` | 结构化结果（含 `visual` 与 `topics`） |
| `visual.json` | 色卡封面结构，直接喂给 `render/card.html` |
| `report.json` | 长网页报告的现成数据，`minutecraft render` 直接吃 |

**三步都不需要任何 API key。**

## 3. 要出图（两种图，别混）

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

## 4. 可选：无人值守模式

需要批量跑、没人盯着时，可以让工程自带 LLM 客户端：

```bash
export MINUTECRAFT_LLM_BASE_URL=... MINUTECRAFT_LLM_API_KEY=... MINUTECRAFT_LLM_MODEL=...
minutecraft run meeting.txt --out ./out --json
```

**这不是必需路径。** 交互式使用一律走第 2 节的三步，避免多一处密钥落点。

## 5. 退出码（**别重试错的东西**）

| 退出码 | 含义 | 怎么办 |
| --- | --- | --- |
| 0 | 成功，产物齐全 | 继续 |
| 2 | 输入解析不出来 / 请求包或回答文件格式不对 | 检查文件，别重试同样的输入 |
| 3 | 生成失败（回答无法解析、模板不存在等） | 看 stderr 的具体原因 |
| **4** | **产物已出，但有降级**（缺 topics / 缺 keywords / 议题解析失败…） | **不算失败，别整条重跑**；按 stderr 里列出的警告**补答那一项**再 `finish` |

`finish` 返回 3 且提示「summary 回答无法解析」时，**回到第 2 步重新问模型**，通常是模型没按 JSON 输出。
`render` 返回 3 且提示「找不到 Chrome/Edge」时，**不要去重试渲染**——换浏览器路径，或把 HTML 交给用户手动截图。
返回 4 时 stderr 会把每条警告原样打出来（JSON 里 `warnings` 同内容），照着补即可。

## 6. 遇到问题

| 现象 | 原因与解法 |
| --- | --- |
| `minutecraft: 未找到命令` | 用 `python -m minutecraft` |
| `不是有效的请求包` | `request.json` 不是 `prepare` 产出，重新生成 |
| `缺少 summary 回答` | `answers.json` 里键名必须是 `summary` |
| `summary 回答无法解析为纪要结构` | 模型没输出 JSON；重问并强调「只输出 JSON」 |
| Word 打不开 | `python -c "import docx"` 检查依赖 |
| 中文文件名报错 | 先 `mkdir` 输出目录；标题里的 `/ \ : * ? " < > \|` 会被自动去掉 |
| 长会议提示词很长 | 正常，超过约 45000 字的转写会自动分段浓缩（那是 `run` 模式的事，三步模式下由你处理） |

## 边界（别越界承诺）

- **不处理音频**：只吃转写后的文本。要转写请另找 ASR 工具
- **不含声纹 / 热词 / 实时上屏**：那些是产品态能力
- **三步模式不生成三阶段导图**：`finish` 只用模型回答里内联的 `mindmap` 字段。
  需要更细的多阶段导图请用 `run` 模式（它会额外发起几轮模型调用）
- **出图需要本机浏览器**：`render` 用系统 Chrome/Edge 截图；纯 LLM、无执行环境的 Agent 只能读 JSON 自己渲染
- **不上传任何数据**：三步模式全程只在你和模型之间传文本，产物落在本地
