# 示例文件

| 文件 | 说明 |
| --- | --- |
| `meeting-transcript.txt` | 一段**真实**会议转写（约 1 分钟，来自 EasyShare 听记记录）。注意它的说话人全是「说话人 1」——这是 ASR 没做说话人分离时的真实形态，所以生成结果里不会出现多发言人 |
| `range-timestamp-transcript.txt` | **ASR 分段导出的另一种常见形态**：每行 `[起 - 止] 内容`，夹着大量「嗯/行嘞行/好的好的」碎句。用来验证区间时间戳解析与材料压缩（语气词会被丢掉、短句会合并） |
| `visual.json` | 上面那段转写跑出来的汇报卡结构，可直接拖进 `../render/card.html` 看版式 |
| `report-sample.json` | **长网页报告**的结构样板：3 个议题 / 6 个分节 / 聚焦框 / 洞察 / 待办，用来先看版式。`minutecraft render report-sample.json -o report.png` |

想自己跑一遍：

```bash
cd ../scripts && pip install -e .
minutecraft run ../examples/meeting-transcript.txt --out /tmp/minutes-out --json
minutecraft prepare ../examples/range-timestamp-transcript.txt -o /tmp/req.json --json   # 看 material 压缩后字数
minutecraft render ../examples/report-sample.json -o /tmp/report.png                      # 看长网页版式
```

格式说明见 [`../../SKILL.md`](../../SKILL.md) 第 3 步。
