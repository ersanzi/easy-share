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
# out/产品周会 纪要.docx   out/minutes.md   out/minutes.json   out/visual.json
```

不装包也能跑：`PYTHONPATH=engine python -m minutecraft run meeting.txt --out out/`

## 输入：什么格式都收

| 形态 | 例子 | 说明 |
| --- | --- | --- |
| 纯文本（带时间戳） | `张三 [00:12] 部署我来跟进` | 自动识别说话人与 `mm:ss` / `hh:mm:ss` |
| 纯文本（起止区间） | `[00:00 - 00:03] 好的好的,`<br>`张三 [00:03 - 00:12] 部署我来跟进` | **ASR 分段导出最常见**：分隔符支持 `- – — ~ ～ --> 至 到`，时间支持 `mm:ss` / `hh:mm:ss` / 带毫秒 |
| 纯文本（冒号） | `张三：部署我来跟进` | 没有时间戳时按行序，时间记 0 |
| SRT / VTT | `00:00:01,200 --> 00:00:04,500` | 字幕导出直接可用（起止时间都保留） |
| JSON 记录 | `{"segments": [...], "speaker_names": {...}}` | EasyShare 听记记录原样丢进来即可 |
| 标准输入 | `cat a.srt \| minutecraft run -` | 管道友好 |

> 转写材料默认会被压缩再喂模型：合并同一说话人的连续短句、丢掉「嗯/行嘞行/好的好的」这类
> 语气词碎句，只在段首保留时间戳。逐秒切段的 ASR 转写通常能省掉 3/4 的输入 token。
> 要原样逐行喂，加 `--full-text`。

## 两步走（Agent 用，零 LLM 配置）

```bash
minutecraft prepare meeting.txt -o request.json   # 只组装提示词，不调模型
# → 把 requests.summary 与 requests.topics 并发交给你的模型，回答存成 answers.json
minutecraft finish request.json --answers answers.json -o out/   # 只解析与导出
```

- 关键词默认并入 `summary` 回答（`keywords` 字段），不再单独发一次请求
- `summary` 与 `topics` 互不依赖，**并发发出**；`topics` 最慢（82 分钟会约 250-300s）
- 只要 Word/Markdown、不要长图时加 `--no-topics`，省掉最慢的一次调用

## 命令行

```
minutecraft run <输入> [--out DIR] [--template ID] [--title T]
                       [--no-keywords] [--no-mindmap] [--no-docx] [--no-topics]
                       [--full-text] [--with-transcript] [--json]
minutecraft schema [--json]       # 输入/产物结构自描述（Agent 组织提示用）
minutecraft templates [--json]    # 内置模板：会议纪要/周会/站会/面试/访谈/培训/头脑风暴
minutecraft visual minutes.json   # 只出可视化汇报卡结构
```

退出码：`0` 成功、`2` 输入无法解析、`3` LLM 或生成失败。`--json` 输出机器可读结果：

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
| `todos` | 待办一等实体：内容 / 负责人 / 期限 |
| `keywords` | 关键词 |
| `mindmap` | 三阶段 harness 产出的 markdown 大纲 |

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

## 给 Agent 的接入提示

```
1. minutecraft schema --json           # 先读结构，别猜字段
2. minutecraft run <转写文件> --out out/ --json
   - 退出码 3 且 stderr 提示「LLM 未配置」→ 先让用户配环境变量，不要重试
   - 结果里的 files 是绝对路径，可直接交给用户或后续工具
3. 要自己画页面就取 out/visual.json（色卡结构），不要解析 docx
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
