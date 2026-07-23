# AI 模型选型与凭据配置

> 日期：2026-07-23
> 状态：已完成

## 背景

知识平台里程碑 0 的 Python AI 服务骨架已跑通端到端管线（入库/检索/权限过滤），但 embedding 和 LLM 一直使用占位实现（HashEmbedder / 空 LLM）。需要确定真实的模型提供方并完成配置，才能进行语义检索验收。

## 决策

| 用途 | 提供方 | 模型 | 端点 | 备注 |
| --- | --- | --- | --- | --- |
| Embedding | 阿里云百炼 DashScope | qwen3.7-text-embedding | `https://dashscope.aliyuncs.com/compatible-mode/v1` | 1024 维，OpenAI 兼容 |
| LLM | SenseNova | deepseek-v4-flash | `https://token.sensenova.cn/v1` | 推理模型，响应含 reasoning_content |

两者均为 OpenAI 兼容接口，与 `knowledge/app/config.py` 的 `llm_base_url` / `embedding_base_url` 设计完全吻合，无需改代码即可接入。

## 凭据管理

- 真实 API Key 存放在 `knowledge/.env`（已被 `.gitignore` 的 `.env` / `.env.*` 规则排除，不会提交）
- `knowledge/.env.example` 仅保留空值 + 提供商注释，供新开发者参考
- 不在代码、文档、迭代记录中记录真实密钥

## 代码影响

- `knowledge/.env`：更新 EMBEDDING_MODEL 为 `qwen3.7-text-embedding`，填入真实 API Key
- `knowledge/.env.example`：补充推荐提供商与模型注释
- `docs/knowledge-platform.md`：待决策 → 已决策
- `docs/progress.md`：更新进行中状态

## 排障备忘

- deepseek-v4-flash 是推理模型，`reasoning_tokens` 占 `max_tokens` 额度。如果结构化输出（JSON）被截断，需将 max_tokens 设到 8000+。
- DashScope 兼容端点路径是 `/compatible-mode/v1`，不是 `/v1`；如果报 404 先检查 base_url。
- 如果 embedding 调用报模型不存在，确认百炼控制台已开通 `qwen3.7-text-embedding` 模型权限。

## 下一步

启动 knowledge 服务，用真实模型跑一次完整的入库 → 语义检索 → LLM 生成回答，验收语义质量。
