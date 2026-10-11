# 情报专题 — 开源知识库/RAG 全景对标与自研取舍（2026-10-12，用户直配调研）

> 触发：用户提问「知识库这块学习下 GitHub/码云，看有没有可参考的开源，而不是自己造轮子」。
> 性质：调研文档，不改代码；结论作为知识服务自研/采用路线的依据。
> 增补（同日）：用户点名 WeKnora——补 §0，它修正了本文对"平台级选项"的判断。

## ⭐ §0 WeKnora（腾讯）—— 最贴近我们定位的开源同类，重点评估对象

[Tencent/WeKnora](https://github.com/Tencent/WeKnora) v0.8.0，MIT，13.7K star。LLM 知识管理框架：
RAG 问答 + ReAct Agent（MCP 工具/技能沙箱/网络搜索）+ 自动 Wiki（知识图谱）+ 跨会话记忆。

**覆盖面（vs 我们知识服务）**——它有而我们没有的：
- 解析：十余种格式（PDF/Word/Excel/PPT/EPUB/MHTML/XMind/图片…），Office 由 anydoc 在 Go 进程内解析，PaddleOCR-VL/OpenDataLoader 引擎可选；
- 检索：BM25 + Dense + **GraphRAG 图谱增强** + **父子分块** + pgvector HNSW + rerank + 端到端评估（召回/BLEU/ROUGE）；
- 知识运营：**分块编辑 + 版本历史 + diff 回滚**（人工修正检索片段）、FAQ/文档/Wiki 三类知识库、文件夹树、自动打标签、批量重解析；
- 平台能力：**空间 RBAC 四级角色 + 按 KB 资源归属 + 每空间审计日志**（直接覆盖我们挂起的"操作审计"思路）、权限范围 API Key + Principal、AES-256-GCM 凭据加密、SSRF 防护、Langfuse 全链路追踪、MQ 任务队列 + Worker 池治理；
- 生态：数据源同步（飞书/Notion/语雀/钉钉/GitLab/IMA/RSS）、IM 问答（企业微信/飞书/Slack/钉钉/QQBot/微信…）、网站嵌入 Widget、Chrome 插件、微信小程序、官方 MCP Server（29 工具）、**官方 DeepSeek Harness 插件**（与上一轮 dsh 对标闭环）；
- 部署：Docker/Helm，模块化组件可替换（8 种向量库/8 种对象存储/20+ 模型厂商），约 360 个 API 端点。

**它没有而我们有的**：RuoYi 账号/组织架构贯通（SSO + dept 语义——它的 RBAC 是空间角色制，无企业部门树概念）、WPS 加载项、桌面端 Core 网关、venv+systemd 极简部署、OKF 重建保证、驾驶舱/周报。

**对结论的修正**：§三"整平台替换缺真实候选"不再成立——**WeKnora 是"整服务替换/混合架构"的真实候选**。更新后的选项排序：
1. 观察期质量达标 → 维持自研（默认）；
2. 解析/检索质量触发器命中 → **组件级**：引 RAGFlow DeepDoc 当解析子服务（改动最小）；
3. 质量与功能面双重不达标，或 IM/数据源/Agent 需求爆发 → **整服务级**：WeKnora 替换知识服务，我们退为集成层（OIDC/API Key 桥接 RuoYi、WPS 与桌面端对接其 360 端点 API、权限语义映射 dept→空间 RBAC）。成本：部署面变重（app+docreader+pgvector+可选 neo4j/minio/langfuse 多容器）、驾驶舱/周报/OKF 需重建、7K 行编排退役。

**立刻可借鉴的单点设计**（不换平台也适用）：分块编辑与版本历史（人工纠错检索片段）、父子分块、FAQ 知识库类型（呼应"问过的不再问第二遍"）、按空间的审计日志（呼应操作审计）、数据源连接器（飞书/Notion 同步 vs 我们 watcher 目录监听）。

## 一、平台级全景（GitHub 四强 + 码云生态）

| 平台 | 定位 | 优势 | 相对我们 |
| --- | --- | --- | --- |
| **Dify** | LLM 应用开发平台 | 复杂工作流、技术中台、部署较易 | 无 RuoYi 集成、无部门级文档权限 |
| **RAGFlow** | 深度文档理解 RAG 引擎 | DeepDoc 解析（表格/布局）最强、检索精度口碑 | 重 Docker 栈；检索管线封装在内部难定制 |
| **FastGPT** | 企业知识库 | 中大型复杂 RAG 需求、私有模型集成 | 部署较复杂 |
| **MaxKB** | 企业级智能体平台 | 中小企业快速落地、低门槛 | 定位与 EasyShare 知识页部分重叠 |
| **qKnow**（Gitee GVP） | 国产智能体/知识图谱/知识库 RAG | 码云原生、GVP 认证、行业模块化 | 社区规模与成熟度待核实 |

码云生态结论：平台级项目以 **GitHub 原生 + Gitee 官方镜像**为主（Dify/FastGPT/MaxKB/RAGFlow 均有镜像，
镜像价值 = 国内网络可达）；Gitee 原生项目中 qKnow 是 GVP 代表，但未形成四强量级的社区。
另有 [2026 年 RAG 开源项目大全](https://www.cnblogs.com)（九大维度：框架/向量库/嵌入/文档处理/检索增强/评估/GraphRAG/低代码/Agentic）可作索引备查。

## 二、诚实的 build-vs-adopt 权衡

**换平台会失去的（我们的差异化，全在集成层）**：
1. RuoYi 账号体系贯通（SSO 今天刚落地：控制面令牌直通知识服务）；
2. **部门级 + 文档级权限感知检索端到端**（visible_depts 在生成链路生效——四强平台只有 dataset 级 ACL，无组织架构语义）；
3. WPS 加载项（自托管、同源免跨域）；
4. 桌面端 Core 网关集成（令牌不进前端）；
5. 部署极简（venv + systemd，无重容器栈——4C8G 单机要同时跑控制面/PG/Redis/RustFS，平台级普遍要多容器数 GB 内存）;
6. 驾驶舱/周报/OKF 重建保证（derived=知识源、向量=可重建缓存）。

**平台比我们强的（自研的真实劣势，不回避）**：
1. 文档解析格式广度与深度（RAGFlow DeepDoc 的表格/布局理解）；
2. 社区迭代速度（我们 1 人 vs 千级贡献者的踩坑红利）;
3. 评测体系成熟度（Ragas 等现成工具链）；
4. 企业功能长尾（工作流、多模型路由、应用模板）。

## 三、结论：不造轮子的正确姿势是"组件借力、编排自研"

**已经在用开源件的地方就是正确姿势**（这些轮子我们从来没自己造过）：
解析 = MinerU + PaddleOCR；向量库 = Milvus（预留，触发切换）；LLM/Embedding/Rerank = API 调用
（百炼/SenseNova 等）。**自研的只有约 7K 行"编排与集成层"**——而这层恰恰承载全部差异化
（权限感知、SSO、WPS、驾驶舱），是四强平台给不了的。

**整平台替换的触发器**（写清才不纠结）：观察期周报中「解析失败率」或「答案引用错误率」
持续不达标 → 优先评估**引入 RAGFlow DeepDoc 作为解析子服务**（组件级替换，保留编排层），
而非整平台替换。口径：编排与集成层自研是护城河，单点能力随时换最强开源件。

**已验证有效的借思想路线**（9 月情报扫描佐证）：contextual chunking（Anthropic 主流化佐证）、
混合检索 + rerank（业界基线参数：候选 20 → 重排取 5）、跳数类型评测（Mem0 评测法，P2 待做）。

## 四、待办联动

- 收件箱新增：**WeKnora 评估**——质量触发器命中时与 DeepDoc 组件级方案并列评估（整服务替换/混合架构候选）；其单点设计（分块编辑版本史/FAQ 类型/空间审计）随对应需求实现时参考；
- 既有：评测难例扩充、MinerU 冒烟已在池/在办；
- 新增观察指标建议：观察期周报加「解析失败率」列，作为上表触发器的数据源（P2，随周报使用反馈定）。

## 来源

- [MaxKB vs RAGFlow 对比](https://www.maxkb.cn/maxkb-vs-ragflow)、[CSDN 四平台对比](https://blog.csdn.net/yinzheshijie/article/details)、[腾讯云全面对比](https://cloud.tencent.com/developer/article)、[知乎六框架对比](https://zhuanlan.zhihu.com)、[RAG 平台深度拆解](https://huangf.org/posts/aiworkflow)
- [Gitee GVP（qKnow）](https://gitee.com)、[2026 RAG 开源项目大全](https://www.cnblogs.com)
- 既往：[Anthropic Contextual Retrieval](https://www.anthropic.com/engineering/contextual-retrieval)、docs/intelligence/2026-09-03-digest.md
