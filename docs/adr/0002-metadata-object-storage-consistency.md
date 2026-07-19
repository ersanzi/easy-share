# ADR-0002：元数据与对象存储一致性

- 状态：提议
- 日期：2026-07-19
- 决策者：EasyShare maintainers
- 关联：[`0001-file-identity-and-version-model.md`](0001-file-identity-and-version-model.md)、[`../technical-selection.md`](../technical-selection.md)、[`../technology-evaluation.md`](../technology-evaluation.md)

## 背景

云端元数据保存在 PostgreSQL，文件字节保存在 S3 兼容对象存储。二者没有共同事务。上传可能在客户端断网、服务重启、预签名 URL 过期、对象完成但数据库提交失败等任意步骤中断。

如果把“对象已上传”直接等价为“文件版本可见”，可能出现元数据指向不存在或未验证对象；反过来，数据库失败也可能遗留持续计费的孤儿对象。

## 决策

采用“控制面上传会话 + 对象存储 multipart + 显式提交”的状态机。API 服务负责授权、状态和提交，对象字节由客户端直接上传到对象存储。

```text
CREATED → UPLOADING → UPLOADED → VERIFYING → COMMITTED
    └──────────────→ ABORTED / EXPIRED / FAILED
```

推荐流程：

1. 客户端以稳定的 idempotency key 创建上传会话，提交目标 file ID、base version、大小、哈希算法和内容哈希；
2. 服务端检查权限、配额和并发前提，创建 upload ID，并返回 multipart 分片计划和预签名 URL；
3. 客户端持久化 upload ID、对象 key 和每个已完成分片；
4. 客户端完成对象存储 multipart，并调用 complete API；
5. 服务端确认对象存在、大小和分片结果，执行必要的内容校验；
6. 在一个 PostgreSQL 事务中创建 `FileVersion`、更新 `FileEntry.current_version_id`、写配额变化和 change journal；
7. 事务成功后状态变为 `COMMITTED`，同一 complete 请求重试返回相同结果；
8. 后台任务清理过期 multipart、未引用对象和长时间停留的中间状态。

对象在 `COMMITTED` 前不得作为普通文件版本向其他客户端可见。数据库事务之外的异步动作使用 transactional outbox 或可重复扫描的任务记录，不能依赖“提交后刚好成功发出一条消息”。

下载由 API 鉴权后返回短期预签名 URL，或在需要附加策略时由专用数据面代理；MVP 默认选择预签名 URL。

本 ADR 接受 S3 API 语义和对象存储边界。项目随后通过 [`ADR-0006`](0006-rustfs-self-hosted-object-storage.md) 选择自建优先并采用 RustFS 作为首选实现；该选择不改变本 ADR 的一致性状态机，也不允许业务层绑定 RustFS 特有 API。RustFS 当前 beta 和分布式能力风险由 ADR-0006 的固定版本、独立备份、兼容测试与生产门禁控制。

## 不变量

1. 可见的文件版本只引用已提交、可读取并完成完整性验证的对象。
2. 相同 idempotency key 不创建多个逻辑版本。
3. complete、abort 和清理操作都可以安全重试。
4. 上传会话有明确过期时间，预签名 URL 有较短有效期。
5. 客户端提供的 hash 只是待验证声明，不能单独作为授权依据。
6. 配额只按明确的产品规则记账，并能通过数据库记录重算。
7. 日志不记录预签名 URL、Token 或对象存储密钥。

## 备选方案

### 文件全部经过 Go API 代理

MVP 不采用。实现简单，但大文件会占用 API 带宽、连接和内存，扩容成本高。可为私有化或特殊审计场景保留独立适配器。

### 先提交元数据，再异步上传对象

拒绝。其他设备可能读取到不存在的内容，失败补偿复杂。

### 使用 S3 ETag 校验完整文件

拒绝。multipart ETag 通常不是文件 MD5，也不能替代明确的内容哈希。

### 用分布式事务协调 PostgreSQL 和对象存储

拒绝。S3 不参与通用两阶段提交，复杂度与收益不匹配。

## 影响

正面影响：

- 大文件流量绕开 API 服务；
- 客户端能够恢复 multipart；
- 中断状态可观测、可清理；
- 元数据对外只暴露已提交版本。

成本与风险：

- 需要上传会话表、后台清理和对象生命周期规则；
- 不同 S3 服务对校验能力支持不同，需要兼容性测试；
- 完整 SHA-256 的服务端验证可能增加 I/O，应根据供应商能力设计。

## 开放问题

- 首个对象存储供应商和地域；
- 分片大小、最大并发数和单文件上限；
- 哈希由对象存储原生校验、后台读取校验还是客户端与服务端组合校验；
- 秒传是否只限同一用户，以及配额如何计量；
- 对象存储故障时的降级和多地域策略。

## 验证

至少注入以下故障并证明可恢复：

- 创建会话后客户端退出；
- 部分分片上传后 URL 过期；
- multipart 完成后 API 请求丢失；
- 对象完成但 PostgreSQL 事务失败；
- complete 请求重复到达；
- base version 已被另一设备更新；
- 配额在上传期间被其他任务耗尽；
- 清理任务重复运行且不会删除已引用内容。

