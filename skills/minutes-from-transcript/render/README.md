# 报告渲染（长网页版式）

从本版起，长网页报告由 `minutecraft render` 直接出图，**模板与渲染代码都在 `scripts/minutecraft/report/`**
（`report.html` + `render.py`），不再在本目录另存一份，避免「复制出去的那份」和产品行为漂移。

## 用法

```bash
minutecraft render out/minutes.json -o out/report.png            # 出 PNG
minutecraft render out/minutes.json -o out/report.png --pdf out/report.pdf
```

前置：本机有 Chrome 或 Edge（找不到用 `--browser` 指定）；没有浏览器时退出码 3 并给出替代方案。

详细字段说明、结构样板与边界见 [`../scripts/minutecraft/report/README.md`](../scripts/minutecraft/report/README.md)。

## 本目录还剩什么

| 文件 | 用途 |
| --- | --- |
| `card.html` | 自包含的**色卡封面**渲染器：双击打开、粘 `visual.json`、点渲染 → 打印/存 PDF |

`card.html` 与 `frontend/src/components/MeetingVisualReport.vue` 是**同一套钉钉色卡版式的两个实现**，
**改这个色卡版式时两处必须一起改**。

但**长网页报告不在这条规矩里**：`minutecraft render` 出的长图由
`scripts/minutecraft/report/report.html` 渲染，结构来自 `summary.topics`（议题 → 分节 → 要点），
桌面端不内嵌它、也没有同款 Vue 实现。**长报告改版式只改 `report.html` 一处**——
给它再造一个第二实现，只会多一份没人维护的副本。
