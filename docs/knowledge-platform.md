# EasyShare 知识平台：愿景与架构

> 本文记录 EasyShare 从消费级文件工具向企业知识管理平台演进的总体方向与架构决策。
> 这是长期方向文档，逐版交付情况以 [`progress.md`](progress.md) 和 [`iterations/`](iterations/README.md) 为准。
> 最后更新：2026-07-25

## 1. 定位演进

EasyShare 起步于消费级局域网文件传输与云盘工具（对标百度网盘 / AirDrop），但存储数据只是整条价值链的一环。最终目标是把散落在各处的文件，变成可检索、可问答、能辅助写作的**企业知识资产**，并在用户写材料时（如 WPS 中）提供基于这些知识的帮助。

核心转变：从"文件的搬运与存放"到"知识的沉淀与复用"。

## 2. 完整价值管线

```
采集传输 ──→ 存储 ──→ 解析清洗 ──→ 知识库 ──→ AI 写作辅助 ──→ WPS 插件交付
(已有)      (已有)     (文本化)     (RAG)      (LLM 生成)      (登录即用)
```

- **采集传输**（已有）：EasyShare 桌面端把文件采集、局域网传输、上传到云端。
- **存储**（已有）：RustFS 对象存储，文件只存一份。
- **解析清洗**：把 docx/pdf/xlsx/ppt/扫描件等转成干净、结构化的文本。整条管线质量的地基。
- **知识库**：文本切块、向量化、存入向量库，支持语义检索（RAG，非微调）。
- **AI 写作辅助**：检索相关片段 + 云端 LLM 生成回答或写作建议，答案可溯源到原文。
- **WPS 插件交付**：用户登录账号后，在 WPS 侧边栏获取基于知识库的帮助。

## 3. 关键技术决策

### 3.1 RAG，而非微调训练

目标是"基于企业文档提供帮助"，正确路线是检索增强生成（RAG）：

- 微调改变的是模型的风格与行为，往模型里注入知识靠的是检索。
- RAG 成本低、文档更新立即生效、答案可溯源到原文出处。
- 真正的微调只在需要固化某种特定公文文风时才考虑，现阶段不做。

因此"训练数据"这一环实际落地为"构建知识库"：清洗后的文本切块、向量化、入向量库。

### 3.2 Java 控制面 + Python 计算面

两个语言各取所长，协作而非二选一：

| 层 | 语言 | 职责 | 技术栈 |
| --- | --- | --- | --- |
| 控制面 | Java | 账号、认证、数据权限（RBAC）、多租户、文件元数据、业务编排、对客户端的 API 网关 | Spring Boot + MyBatis-Plus + PostgreSQL |
| 计算面 | Python | 文档解析、文本清洗、切块、向量化、向量检索、RAG、LLM 网关 | FastAPI + 解析库 + 向量库 |

**为什么分两层**：控制面是重业务、重事务、重权限的活，Java 生态成熟且团队有经验；计算面是重生态、重算法库的活，Python 在文档解析与 AI 检索上的库明显领先。强行单一栈会在其中一环吃亏。

**代价（已知悉）**：两套运行时与部署、运维复杂度上升、Java↔Python 接口契约必须设计干净。值得，但要维护好那条缝。

### 3.3 两者如何协作

共享存储 + 干净服务接口，两条线：

- **同步线（REST）**：Python 暴露 `POST /documents/process`（按对象引用创建异步处理任务）、任务/产物查询和 `POST /query`（带权限范围的检索+生成）等接口，Java 鉴权与权限裁决后调用。`POST /ingest` 仅保留为当前兼容验证入口。
- **异步线（消息队列）**：解析大文档慢，Java 往队列（RabbitMQ/Kafka）丢"解析这份文件"任务，Python 消费处理完回写状态，避免阻塞。
- **共享**：两边共用 RustFS 里的同一份文件，靠统一文件 ID 对应。Java 管"这个文件谁能看"，Python 管"这个文件的内容向量"。

当前 Java 尚未落地时，Python 使用 SQLite 保存单进程执行任务，仅用于重启恢复、进度和失败重试。它不是租户、权限、文件元数据或业务任务的真相源；这些职责仍保留给后续 Java + PostgreSQL 控制面。

```
   EasyShare 桌面端 / WPS 插件
            │  (登录、上传、提问)
            ▼
   ┌─────────────────────┐
   │  Java 业务服务        │  账号·权限·文件登记·编排·大数据
   │  (控制面)            │  提问时先算出用户能看哪些文档
   └──────────┬──────────┘
       REST 同步 / 队列异步 │  共享 RustFS + 文件 ID 体系
            ▼
   ┌─────────────────────┐
   │  Python AI 服务       │  解析·切块·向量化·检索·RAG·LLM
   │  (计算面)            │  按 Java 给的权限范围检索并生成
   └─────────────────────┘
```

### 3.4 权限感知检索（数据权限的落点）

企业场景下用户不能检索到无权限的文档内容。分工：

- 提问时，Java 根据权限模型算出"该用户可访问的文档 ID / 租户范围"。
- Java 把这组过滤条件传给 Python 的 `POST /query`。
- Python 的向量检索只在该范围内进行。

权限逻辑留在 Java（强项），检索执行在 Python（强项），查询时握手。这是"两个都用、相互合作"最有说服力的场景。

### 3.5 LLM 抽象为可替换网关

现阶段用云端 API（团队已有 OpenAI 兼容接口接入经验）。但企业客户对数据出域敏感，将来大概率要支持私有化部署模型。因此架构上把 LLM 调用抽象成可替换网关：现在接云端，以后能换本地模型，不返工。Embedding 同理，独立配置、可替换。

## 4. 三个部署形态

一个品牌、一条管线、三个形态。它们串连的是数据与身份，不是代码与进程：

1. **EasyShare 桌面客户端**（已有）：本机优先，负责采集、局域网传输、上传到统一存储。保持消费级开箱即用。
2. **知识平台服务端**（新建）：联网多用户，含 Java 控制面与 Python 计算面，是整个愿景的新重心。
3. **WPS 插件**（后置）：写作助手客户端，登录后调用服务端 AI 接口。

统一账号（登录一次处处可用）+ 统一对象存储（RustFS）把三者串起来。

> 注：现有 EasyShare Core 仅监听 127.0.0.1、无账号，撑不起多用户。桌面端继续做采集与局域网传输，知识库/账号/AI 放在网络服务端。两者不冲突——桌面端是触手，服务端是大脑。

## 5. 分阶段路线（walking skeleton 策略）

策略：先让整条管线从头到尾流起来，每一环都做最薄版本，验证端到端闭环后再逐环加厚。不先把单环做深。

- **里程碑 0（已完成）**：Python AI 服务最小骨架——读一份文档 → 解析 → 切块向量化 → 提供问答接口。证明核心命题"文档能否变成可用知识"。账号、WPS、多文件格式均后置。
- **里程碑 1（进行中）**：第一段已完成 TXT/Markdown/DOCX/文本型 PDF/XLSX/PPTX 的统一解析、清洗产物、异步任务和版本化索引；下一段接入扫描件 OCR、Unstructured 结构增强与 Milvus。
- **里程碑 2**：Java 控制面接入——账号、权限、文件登记、权限感知检索。走向多用户企业级。
- **里程碑 3**：WPS 插件——登录、侧边栏、调用 AI 接口，完成最后一公里交付。

> 各里程碑的详细任务、交付物、验收标准与依赖见 [`knowledge-platform-roadmap.md`](knowledge-platform-roadmap.md)。

## 6. 目录结构约定

Python 计算面服务置于仓库根目录 `knowledge/` 下，与现有 Go 桌面端并列，互不干扰：

```
knowledge/                  # Python AI 服务（计算面）
  app/
    main.py                 # FastAPI 入口
    config.py               # 配置（RustFS / LLM / embedding / 向量库）
    api/routes.py           # 异步处理、任务、产物、兼容入库与查询接口
    jobs/                   # SQLite 执行状态与进程内任务执行器（过渡实现）
    parsing/                # 多格式解析、清洗、统一块模型与 Markdown 渲染
    pipeline/service.py     # RustFS → 产物 → 切块 → 索引编排
    kb/chunker.py           # 文本 → 切块
    kb/embedder.py          # 切块 → 向量（可替换网关）
    kb/store.py             # 向量库
    rag/retriever.py        # 检索相关片段
    rag/generator.py        # 片段 + 问题 → LLM 回答
    storage/rustfs.py       # 从 RustFS 读文件
  requirements.txt
  README.md
```

Java 控制面后续置于独立目录（如 `platform/`），现阶段未建。

### 3.6 管线技术选型（2026-07-23 确定）

设计原则：**能不造轮子就不造，每环用市面上最成熟的库；自写薄编排串联，不绑定 RAG 框架。**

| 环节 | 选型 | 理由 |
| --- | --- | --- |
| 文档解析 | **Unstructured**（主编排）+ **PaddleOCR**（扫描件 fallback） | Unstructured 覆盖 20+ 格式、输出 Element 分类（Title/Table/NarrativeText）对切块友好；PaddleOCR 中文 OCR 精度开源天花板，处理无文本层的扫描件/图片 PDF |
| 清洗 | 薄规则层，基于 Unstructured Element 类型过滤（去 Header/Footer/PageBreak 等） | 简单，不需要框架 |
| 切块 | Unstructured `chunk_by_title`（尊重文档结构）+ max_characters 上限 | 按标题/段落/表格边界切，不机械按字数；后续可叠加语义切块 |
| 向量库 | **Milvus Standalone**（docker-compose：etcd + Milvus，对象存储复用 RustFS，不额外跑 MinIO） | 商业级天花板（亿级向量、混合检索）、国产生态好、Standalone 模式资源可控 |
| Embedding | 阿里云百炼 `qwen3.7-text-embedding`（1024 维，OpenAI 兼容） | 已验证 |
| LLM | SenseNova `deepseek-v4-flash`（OpenAI 兼容，推理模型） | 已验证 |
| Reranking | 待定（bge-reranker 本地 / 百炼 rerank API） | 里程碑 1 后期按需引入 |
| RAG 编排 | **自写薄层**（~100 行），OpenAI SDK 直调 LLM | 完全可控、好调试、权限过滤自由加；不引入 LlamaIndex/LangChain 避免框架锁定 |
| 任务队列 | 现阶段 SQLite + `ThreadPoolExecutor` 单进程执行器；里程碑 2 由 Java + 消息队列接管 | 当前先验证幂等、重试和重启恢复，不把过渡执行库当业务真相源 |
| 控制面数据库 | **PostgreSQL**（里程碑 2） | 关系型业务数据（用户/角色/租户/文件元数据/知识库配置）；后续可启用 pgvector 扩展 |

**解析流程细节**：

```
文件进入 → 判断类型
  ├── 原生数字文档（docx/xlsx/pptx/可复制 PDF）→ Unstructured 直接提取
  └── 扫描件 / 图片 PDF（无文本层）→ PaddleOCR 识别 → 再走 Unstructured 标准化
```

**Milvus 部署**：

```yaml
# docker-compose（知识平台基建）
services:
  etcd:       # Milvus 元数据
  milvus:     # 向量引擎，minio.address 指向 RustFS 127.0.0.1:9000
  # RustFS 已有，不重复部署
```

**不选 RAG 框架（LlamaIndex / LangChain / Dify）的理由**：

- 架构是 Java 控制面 + Python 计算面，中间有 REST 边界和权限过滤逻辑，框架假设自己管全流程，硬塞会别扭
- 商业级产品要稳定和可控，不要框架版本 breaking change 风险
- 每一环已经用了最好的库，中间串联只需 50-100 行代码
- 高级 RAG 技巧（reranking、multi-query）可单点引入，不需要整个框架

## 7. 待决策

- ~~向量库选型~~（已决策）：Milvus Standalone。
- ~~Embedding 提供方~~（已决策）：百炼 qwen3.7-text-embedding。
- ~~LLM 提供方~~（已决策）：SenseNova deepseek-v4-flash。
- ~~控制面数据库~~（已决策）：PostgreSQL。
- 消息队列选型（异步解析）：RabbitMQ vs Kafka，里程碑 2 前后确定。
- 文件 ID 体系：当前 Python API 已要求调用方传入稳定 `file_id + version_id`；里程碑 2 接入 Java 时由 Java 文件登记正式生成并治理这两个身份。
- Reranking 方案：bge-reranker 本地部署 vs 百炼 rerank API，里程碑 1 后期评估。
- PaddleOCR 集成方式：作为 Unstructured 的 OCR 后端接入，还是独立服务判断后分流。
