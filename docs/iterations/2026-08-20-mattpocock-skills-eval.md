# Matt Pocock Skills 评估：工程方法论技能包的吸收边界

> 日期：2026-08-20
> 状态：已完成（纯调研，无代码变更）
> 研究基线：[mattpocock/skills](https://github.com/mattpocock/skills) `0ab1b63`（MIT，纯 Markdown 技能）
> 关联：与同日 [TencentDB-Agent-Memory 对标](2026-08-20-tencentdb-agent-memory-benchmark.md) 同属"外部工具评估"，但成本结构完全不同（零运行时、零依赖）。

## 1. 用户问题

用户询问 mattpocock/skills 对 agent 辅助开发 EasyShare 的帮助是否值得引入。此前已决策（2026-08-20）：不引入 loopx / TencentDB-Agent-Memory 作为开发工具，保持既有开发步骤，方向是建项目原生命令（见 progress.md 2026-08-20 对标条目与 AGENTS.md）。

## 2. 它是什么

Matt Pocock 的 agent 技能包（"Skills for real engineers — not vibe coding"），34 个技能全为纯 Markdown SKILL.md，无运行时依赖。定位是反框架（明确反对 GSD/BMAD/Spec-Kit"接管你的流程"），小而可组合、鼓励改造。核心链路：

```text
grill-with-docs（拷问需求 + 沉淀域语言）
  → to-spec（对话综合为规格）
  → to-tickets（拆 tracer-bullet 票，声明阻塞边）
  → implement（驱动 tdd 实现）
  → code-review（标准轴 + spec 符合度轴，并行子代理）
```

设计体系的两个关键分层：

- **user-invoked vs model-invoked**：前者仅人可触发（编排器，`disable-model-invocation: true`），后者 agent 可自动触达（可复用纪律）。判定标准："model 能否有用地自主拿起它"。
- **技能间依赖用显式指令表达**：写"Call the Skill tool with X"而非裸提 `/x`，命中率更高且 harness 中立。

代表性设计（grilling）：把需求建模为**设计树**，每轮只问"前沿"问题（前置决策已就绪的），每题附推荐答案；事实类问题派子代理自查，决策类问题才问用户；前沿为空才算对齐完成。

## 3. 评估结论

**帮助大，但价值在选择性吸收而非全盘安装。** 这是三个被评估外部工具中唯一"纯收益低风险"的：装错删文件即可，无沉淀成本。全盘安装的代价是上下文索引噪声（25 个正式技能）与既有纪律的冲突（issue tracker 流派 vs progress.md 单一真相源）。

### 3.1 建议安装（5 个，即插即用、零冲突）

| 技能 | 类型 | 价值 |
| --- | --- | --- |
| `grilling` | model-invoked | 需求拷问原语，防"agent 做的不是我想要的" |
| `tdd` | model-invoked | 红绿重构循环，知识平台 Python 侧测试纪律 |
| `diagnosing-bugs` | model-invoked | 诊断循环：先建红灯反馈回路→最小化→假设→插桩→修复→回归 |
| `handoff` | user-invoked | 会话交接文档，多会话连续开发 |
| `research` | model-invoked | 对高信源调研并沉淀带引用的 Markdown（本次对标学习的标准化形态） |

安装方式：将对应 `skills/<bucket>/<name>/` 目录复制到用户级技能目录（本机已有 `~/.agents/skills/`，`setup-matt-pocock-skills` 即此方式安装）。

### 3.2 借鉴设计模式（用于自建项目原生命令）

- user-invoked / model-invoked 分层与 frontmatter 约定——/iterate、/verify 等命令按此规范建。
- 技能间显式 Skill tool 调用串联（每次一个技能，两个技能是两次调用）。
- docs 页四要素（What it does / When to reach for it / Common questions / It's working if）——自建命令的文档模板。
- "共享语言"（ubiquitous language）思想：AGENTS.md 可增设术语表段，先试验价值，不另建 CONTEXT.md。

### 3.3 明确不装

- **issue tracker 流派**（setup-matt-pocock-skills 的配置产物 / to-spec / to-tickets / triage / wayfinder）：依赖 GitHub Issues 或本地 markdown 票箱，与 progress.md 单一真相源 + iterations 纪律冲突（同 loopx 否决理由）。本机已装的 `setup-matt-pocock-skills` 不运行即无副作用，搁置。
- **CONTEXT.md / domain-modeling**：AGENTS.md 已承担指令真相源职责。
- 其余（wizard / teach / to-questionnaire / wait-what / grill-me 等）：当前场景收益不明显，需要时再单独评估。

## 4. 完成记录

- 已完成：全仓库结构分析、README 与元文档（invocation / writing-docs）精读、grilling / implement / setup 三个代表技能精读、分级结论输出。
- 测试结果：纯调研无代码变更，无测试影响（2026-08-20）。
- 后续工作：① 择机安装 §3.1 五个技能（用户确认后执行）② 自建 /iterate、/verify 等原生命令时引用 §3.2 设计规范。
