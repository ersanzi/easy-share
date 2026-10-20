# 报告渲染（长网页版式）

`minutecraft render` 把纪要记录渲染成**一页长图 / PDF**，不需要 Node、npm、联网或任何 API key。
数据与版式分开放：引擎只负责把纪要翻成模板认的结构（`minutecraft/render.py` 的 `build_report`），
模板 `report.html` 只认 `window.__REPORT__` 这段注入的数据。

## 用法

```bash
minutecraft render out/minutes.json -o out/report.png            # 出 PNG
minutecraft render out/minutes.json -o out/report.png --pdf out/report.pdf   # PNG + PDF
minutecraft render out/minutes.json -o report.png --width 750    # 窄版（发微信/手机看）
minutecraft render out/minutes.json -o report.png --json         # 给 Agent 看的结果
```

**前置**：本机有 Chrome 或 Edge（脚本按常见安装位置自动找，找不到用 `--browser` 指定）。
找不到浏览器时退出码 **3**，提示会说明两条出路：装浏览器，或直接把 HTML 拖进浏览器手动截图。

## 输入

`run` / `finish` 产出的 `report.json` 直接出图（已装配好，最省事）；喂 `minutes.json` 也行，渲染时现场装配。

| 字段 | 版式位置 |
| --- | --- |
| `title` | 头图大标题 |
| `category` / `subtitle` / `tags` | 头图上的标签行、一句话主题、胶囊标签 |
| `duration_seconds` | 头图「听记时长」（缺失时由 `load_transcript` 从最后一句时间戳推，不留 0 分钟） |
| `speaker_names` | 头图「参与人」计数 |
| `summary.topics` | **正文骨架**：议题 → 分节 → 要点（引擎独立一次调用产出，见下） |
| `summary.chapters` | 旧产物的回退：只有时间轴与一句话，分节靠 `visual.sections` 均分 |
| `todos` | 文末待办清单 |
| `summary.insights` | 文末 AI 洞察 |

原则：**有才画，没有就整块不出现**——不显示「—」占位，也不为空字段造一块空框。

## 议题结构从哪来（B 案：独立一次模型调用）

`visual` 是给**色卡封面**用的 6 个并列块，`chapters` 只有一句 gist——两者都不是报告正文。
所以引擎在总结之外**单独问一次**「把转写切细成 4-8 个议题 + 分节 + 要点」，形状：

```json
{"topics": [{"title": "议题", "start": "mm:ss", "end": "mm:ss", "gist": "一句话",
             "focus": ["本段讨论聚焦于…"], 
             "sections": [{"title": "分节", "groups": [{"title": "看法", "items": ["要点"]}]}],
             "insights": ["针对本议题的判断"]}]}
```

- **两步走**：请求包多一项 `requests.topics`，Agent 答完存进 `answers.json` 的 `topics` 键
- **不回答不会坏，但会明说**：`finish` 回退到「章节均分」旧路径（议题只有标题与时间、没有分节正文），
  同时在 `warnings` 里写一条指名补救动作的警告。**别再改回静默回退**——实测 Agent 跳过 `topics` 时，
  产物里 15 个议题 10 个是空壳，而没人知道长图被降级了
- **一次调用的代价**：比塞进总结更安全——塞进去容易把整份纪要顶到 token 上限截断，
  分开后失败只丢长图正文

### 篇幅口径（按钉钉听记实测反推，不是拍脑袋）

参考一份 82 分钟会议的真实产物（1640×36978 截图）：切 10 个议题，单个议题 1900-2700px，
内含 2-4 个分节、每分节 2-4 组、每组 2-3 条整句要点。按 1080 宽折算约 2500px/议题。
提示词照此收紧（`MeetingSummarizer._TOPICS_SYSTEM`）：

| 议题时长 | 分节 | 每分节分组 |
| --- | --- | --- |
| ≥ 10 分钟 | 2-4 个 | 2-4 组 |
| 3-10 分钟 | 1-2 个 | 2-3 组 |
| < 3 分钟 | 1 个 | 1-2 组（**不凑数**） |

要点写 30-90 字的完整句子（谁、做了什么、什么后果），数字/期限/系统名用反引号标出。
实测同一份转写：改口径前 8 个议题里 6 个只有 1 个分节；改后 8 个议题全部有正文，
共 55 条要点，长图从 8970px 到 10992px。

`report.html` 除了渲染议题结构，也接受手工补的 `groups` 结构（`title` + `items`），
用于「先定版式、后改引擎」阶段。

## 与前端是什么关系

| 产物 | 实现 | 用在哪 | 谁在改 |
| --- | --- | --- | --- |
| **长网页报告**（本页） | `minutecraft/report/report.html` | 命令行 / Agent 出长图与 PDF | **只此一处**，桌面端不内嵌 |
| 色卡封面 | `skill/render/card.html` + `frontend/src/components/MeetingVisualReport.vue` | 外部复制使用 / 桌面端内嵌 | 这两处是同一版式的两个实现，**改要一起改** |

按产物画线，不按「实现个数」画线：长报告**没有**第二实现，也没有「一起改」的义务；
色卡封面才是有两份实现、要同步的那个。两条规矩管的是两个不同的东西，别互相套。
两者结构来源也不同——长报告吃 `summary.topics`，色卡吃 `summary.visual`，
**不要**把其中一个的字段约定直接套到另一个上。

## 已知边界

- 长图超过 12000px 会分段截图再拼接，这一步要 Pillow（`pip install minutecraft[render]`）
- 截图走 `--window-size` 定宽；`--width` 只影响页面宽度，不缩放字体
- 模板里的 `focus` / `insights` 允许 `<span class="warm|hl">`、`<b>`、`<em>` 标重点，其余 HTML 一律转义
