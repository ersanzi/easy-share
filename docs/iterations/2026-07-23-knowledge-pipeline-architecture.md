# 知识平台管线架构选型

> 日期：2026-07-23
> 状态：已完成（设计决策，实现待里程碑 1）

## 背景

里程碑 0 跑通了端到端管线（HashEmbedder 占位 → 真实模型验收通过），但每一环都是最薄实现。要长成商业级产品，需要在里程碑 1 开始前把管线每环的技术选型定下来，避免后续返工。

## 设计原则

- 能不造轮子就不造，市面有成熟方案就选用
- MVP 也按商业级架构标准设计选型
- 自写薄编排串联各环，不绑定 RAG 框架（LlamaIndex / LangChain / Dify）

## 决策清单

| 环节 | 选型 | 关键理由 |
| --- | --- | --- |
| 文档解析 | Unstructured（主）+ PaddleOCR（扫描件 fallback） | 格式覆盖广、Element 分类对切块友好；PaddleOCR 中文 OCR 天花板 |
| 清洗 | 薄规则层（基于 Element 类型过滤） | 不需要框架 |
| 切块 | Unstructured chunk_by_title + max_characters | 尊重文档结构，不机械按字数切 |
| 向量库 | Milvus Standalone（etcd + Milvus，存储复用 RustFS） | 商业级天花板、混合检索、不额外跑 MinIO |
| Embedding | 百炼 qwen3.7-text-embedding（1024 维） | 已验收 |
| LLM | SenseNova deepseek-v4-flash | 已验收 |
| RAG 编排 | 自写薄层 + OpenAI SDK | 可控、好调试、权限过滤自由 |
| 控制面数据库 | PostgreSQL（里程碑 2） | 关系型业务 + 后续可启用 pgvector |
| 任务队列 | 现 BackgroundTasks，后 Celery + Redis | 不过早引入重基建 |

## 讨论过程与关键判断

### PaddleOCR 的定位

PaddleOCR 是 OCR 引擎（图片→文字），不是完整文档解析方案。PP-Structure 加了版面分析但仍需后处理。定位：Unstructured 统一编排，遇到无文本层的扫描件/图片 PDF 时 fallback 到 PaddleOCR。两者互补不替代。

### Milvus 不重

Standalone 模式只需 etcd + Milvus 两个容器。对象存储后端配置指向已有的 RustFS（S3 兼容），不需要额外跑 MinIO。内存 ~2-4GB，开发机完全承受。

### 不选 RAG 框架

- 架构是 Java 控制面 + Python 计算面，中间有 REST 边界和权限过滤，框架假设管全流程会别扭
- 商业级要稳定可控，不要框架 breaking change 风险
- 每环已用最好的库，串联只需 50-100 行
- 高级技巧（reranking、multi-query）可单点引入

### 权限过滤方案

采用方案 A：Java 算出可访问 doc_id 列表 → 传给 Python → Milvus scalar filter 在向量检索时过滤。Dify/RAGFlow 等主流产品也是这么做的。

## 待后续决策

- 消息队列：RabbitMQ vs Kafka（里程碑 2）
- 文件 ID 体系（里程碑 2，现用 UUID 占位）
- Reranking：bge-reranker 本地 vs 百炼 rerank API（里程碑 1 后期）
- PaddleOCR 集成方式：Unstructured OCR 后端 vs 独立分流服务

## 下一步

里程碑 1 实现：
1. 引入 Unstructured 替换现有 parsing/extractor.py
2. docker-compose 部署 Milvus Standalone（复用 RustFS）
3. 重写 kb/store.py 对接 Milvus（pymilvus）
4. 重写 kb/chunker.py 为结构感知切块
5. 用真实文档（docx/pdf）验收解析 + 检索质量
