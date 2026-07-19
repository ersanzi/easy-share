# EasyShare 技术评估与产品雷达

> 评估快照日期：2026-07-19。项目活跃度、许可证和产品边界会变化，本文不是永久白名单。每次进入实现或生产发布前必须刷新证据。总体架构方向见 [`technical-selection.md`](technical-selection.md)。

## 1. 目标

EasyShare 的技术选型遵循“**成熟但先进**”：既不因为旧产品曾经流行就延续，也不因为新项目热度高就让 beta 组件进入核心数据路径。选型需要同时证明：

- 当前能力满足业务，而不是依赖已经移除或商业化的功能；
- 有稳定发布、升级路径、故障恢复和真实生产证据；
- 架构、协议和安全实践没有明显落后；
- 项目仍持续维护，许可证与商业策略可接受；
- 可以迁移和导出，不让核心数据被单一供应商锁死。

“先进”主要指协议、架构和工程能力适合未来演进，不等于使用最新语言、最高 GitHub Star 或最新主版本。

## 2. 评估门禁

候选产品必须先通过硬门禁，再进行加权比较。

### 2.1 硬门禁

生产核心路径默认要求：

1. 上游仓库和正式版本仍受维护，不处于 archived/EOL；
2. 存在非 preview/beta 的稳定发布，或供应商提供明确 SLA；
3. 许可证、商用方式和再分发义务已经审查；
4. 具备备份、恢复、升级、回滚和数据导出路径；
5. 安全公告与漏洞修复渠道明确；
6. EasyShare 所需协议能力通过自动化兼容测试；
7. 关键功能不是未公开、即将移除或必须临时购买才能使用；
8. 故障不会破坏文件内容、元数据或同步游标的核心不变量。

未通过硬门禁的项目只能进入 Spike、开发环境或可替换的非关键功能，不能成为默认生产依赖。

### 2.2 加权维度

| 维度 | 默认权重 | 重点证据 |
| --- | ---: | --- |
| 功能与协议匹配 | 25% | 必需 API、语义一致性、限制和扩展能力 |
| 生产可靠性 | 20% | 稳定版本、故障恢复、升级、生产案例 |
| 项目健康与治理 | 15% | 发布节奏、维护者、issue/安全响应、许可证变化 |
| 运维复杂度 | 15% | 部署、监控、扩缩容、备份、故障域和人力要求 |
| 安全与合规 | 10% | 身份、加密、审计、CVE、数据地域 |
| 可迁移性 | 10% | 标准协议、导入导出、替换成本、供应商锁定 |
| 综合成本 | 5% | 存储、流量、请求、机器和运维成本 |

权重可以按组件调整，但任何高分都不能抵消硬门禁失败。

## 3. 评估时机

每项重要依赖至少在以下时间刷新：

- ADR 从“提议”变为“接受”之前；
- 开始实现之前，确认 API 和版本没有变化；
- 首次进入生产之前，完成故障和升级验证；
- 之后每六个月，或上游发生许可证、商业模式、维护状态和重大版本变化时；
- 安全公告、仓库归档、核心功能移除或供应商涨价时立即重评。

评估记录必须包含日期、版本、来源链接、实验结果和退出方案。仅写“社区流行”“性能很高”或“某大厂使用”不能作为证据。

## 4. 接口标准与产品实现分离

核心数据路径优先冻结协议和内部契约，不把供应商产品名写入业务模型：

- 文件内容层使用 EasyShare 自己的窄 `ObjectStore` 接口；
- 首选 S3 API 语义，但只暴露 EasyShare 实际需要的能力；
- PostgreSQL、对象存储和身份供应商均通过配置和适配器隔离；
- 兼容不靠产品自称，而靠 EasyShare 的 conformance suite；
- 不为“理论可替换”设计覆盖所有厂商的巨大抽象层。

对象存储兼容测试至少覆盖：

- Put/Get/Head/Delete 和不存在对象语义；
- multipart 创建、列举、完成、终止和断点恢复；
- 预签名上传、下载和过期；
- checksum、ETag 和 metadata 行为；
- 条件请求、并发覆盖和幂等；
- 大文件、零字节文件和特殊 metadata；
- 生命周期清理、版本能力和服务端加密；
- 限流、超时、部分失败和重试；
- 删除后可见性及供应商声明的一致性边界。

## 5. 对象存储候选矩阵

### 5.1 当前项目决策

EasyShare 已确认中国大陆首发并采用自建对象存储，当前决策见 [`ADR-0006`](adr/0006-rustfs-self-hosted-object-storage.md)：

1. **首选实现**：RustFS，用于开发、云端 MVP 和生产候选；
2. **首要回退**：SeaweedFS，RustFS 未通过生产门禁时用同一测试重新评估；
3. **大型部署候选**：Ceph RGW，适用于已有专业存储团队的场景；
4. **托管对象存储**：保留为异地备份、迁移和未来托管版本候选；
5. **MinIO OSS**：不作为新部署默认；AIStor 只能按商业产品重新评估。

选择 RustFS 是项目方向决策，但不会豁免生产硬门禁。当前 beta 版本可以进入开发和受控 alpha；正式用户数据必须满足 ADR-0006 的独立备份、故障恢复、升级回滚和兼容测试条件。

### 5.2 当前快照

| 候选 | 当前判断 | 优势 | 主要风险/验证点 | 雷达等级 |
| --- | --- | --- | --- | --- |
| RustFS | EasyShare 首选实现 | Rust 实现、Apache-2.0、当前开发活跃、核心 S3/Multipart/Presign 已覆盖 | 当前版本仍为 beta；分布式模式和 KMS 仍在测试；不能作为唯一副本 | Adopt for MVP / Production gated |
| SeaweedFS | 自建首要回退 | Apache-2.0、持续发布、架构灵活、包含 S3 Gateway | S3 语义、升级、元数据故障和团队运维经验需实测 | Assess/Fallback |
| Ceph RGW | 大型自建候选 | 生产历史长、能力完整、适合已有 Ceph 团队 | 部署和运维复杂，不适合当前阶段轻量起步 | Assess/大型部署 |
| Garage | 轻量/跨站点候选 | 面向分布式对象存储、资源需求较低 | 生态和团队规模较小、S3 子集、AGPL-3.0 合规 | Watch |
| 托管对象存储 | 灾备/迁移候选 | SLA、耐久性、运维和安全体系成熟 | 成本、供应商锁定，与自建优先方向不一致 | Assess/Backup |
| MinIO OSS | 新部署不采用 | 曾有成熟 S3 生态和使用经验 | 原仓库已归档并声明不再维护，产品路线转向 AIStor | Hold |
| AIStor | 商业候选 | 官方后续产品路线和商业支持 | 功能边界、分布式能力、许可证与成本需重新确认 | Assess/商业采购 |

这里的 “Adopt for MVP” 表示 RustFS 是当前实现目标，不表示 beta 版本已经获得无条件生产批准。生产等级由验证证据而非雷达标签决定。

### 5.3 RustFS 当前能力边界

RustFS 官方兼容矩阵明确支持常见 Bucket/Object 操作、Multipart 创建/上传/完成/终止、Presigned GET/PUT、Range/条件读取和用户 metadata；同时明确不声称完整 S3 兼容。Bucket Access Logging、Ownership Controls 等仍属于计划或排除范围，Multipart 列举的一些边界行为不在默认门禁内。

EasyShare 因此只冻结实际需要的 S3 子集，并自行运行 conformance suite。业务版本、审计和 SHA-256 完整性不依赖 RustFS 的 Bucket Versioning、Access Logging、ACL、ETag 或尚在测试的 KMS。

默认实施顺序：

1. 固定 RustFS 版本和镜像 digest，建立可重复 Docker 环境；
2. 使用 AWS SDK for Go v2 实现 EasyShare `ObjectStore` adapter；
3. 建立 Multipart、Presign、错误语义和故障恢复测试；
4. 完成 PostgreSQL 上传状态机与 RustFS 的端到端闭环；
5. 建立独立备份和逐对象 SHA-256 恢复验证；
6. 再评估多节点、多磁盘、监控、升级和生产发布。

## 6. 其他技术采用同一规则

| 领域 | 先冻结的标准/边界 | 产品选择原则 |
| --- | --- | --- |
| 数据库 | PostgreSQL SQL/事务语义、迁移和备份契约 | 选择仍受支持的稳定版本；托管与自建分别评估，不追逐刚发布大版本 |
| 身份认证 | OAuth 2.1/OIDC、设备会话和撤销语义 | 在云服务、Keycloak、ZITADEL、Ory 等候选中按运维与功能重评，不自研密码体系优先 |
| 后台任务 | 幂等任务、outbox、可恢复状态机 | PostgreSQL 队列能满足时不提前上重型平台；复杂长工作流出现后再评估 Temporal 等产品 |
| 本地 SQLite | SQL、WAL、迁移和单写边界 | Go 驱动需比较 CGO、纯 Go/WASM、Windows 打包、备份和性能，不按流行度决定 |
| Windows IPC | 版本、ACL、超时、取消和能力协商 | Named Pipe 的具体库和 JSON/Protobuf 由 CfAPI Spike 数据决定 |
| 局域网传输 | 认证、加密、哈希和断点续传语义 | QUIC 实现必须看稳定性、维护和 Windows 表现；不因协议先进就忽略故障恢复 |
| 可观测性 | OpenTelemetry 语义、结构化事件和隐私边界 | Collector、日志和错误平台按部署形态选择，保持导出标准避免平台锁定 |
| 安装更新 | 签名、原子升级、回滚和 Sync Root 清理 | NSIS、WiX/MSI、MSIX 通过真实生命周期验证后选择，不以“新”或“原生”代替测试 |

## 7. 退出机制

每个进入生产的外部产品都必须记录：

- 数据如何完整导出并校验；
- 双写或离线迁移方案；
- 允许停机时间和回滚点；
- 供应商特有 API 的使用清单；
- 许可证或价格变化时的替代候选；
- 最后一次完成恢复演练的日期。

没有退出方案的“可插拔”只是文档承诺，不视为真正可迁移。

## 8. 本次对象存储核查来源

以下均为项目官方仓库或官方文档，状态需在实际决策时重新核查：

- [MinIO GitHub 仓库与维护状态](https://github.com/minio/minio)
- [MinIO README：仓库不再维护及 AIStor 路线](https://github.com/minio/minio/blob/master/README.md)
- [SeaweedFS GitHub 仓库](https://github.com/seaweedfs/seaweedfs)
- [SeaweedFS S3 API 文档](https://github.com/seaweedfs/seaweedfs/wiki/Amazon-S3-API)
- [Ceph Object Gateway 官方文档](https://docs.ceph.com/en/latest/radosgw/)
- [Garage 官方文档](https://garagehq.deuxfleurs.fr/documentation/)
- [RustFS GitHub 仓库与发布状态](https://github.com/rustfs/rustfs)
