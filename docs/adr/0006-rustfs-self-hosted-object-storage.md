# ADR-0006：自建优先并采用 RustFS 作为对象存储

- 状态：接受（生产启用受门禁约束）
- 日期：2026-07-19
- 决策者：EasyShare maintainers
- 关联：[`0002-metadata-object-storage-consistency.md`](0002-metadata-object-storage-consistency.md)、[`../technology-evaluation.md`](../technology-evaluation.md)

## 背景

EasyShare 首发面向中国大陆，项目决定自行建设对象存储，而不是把托管云对象存储作为默认生产依赖。MinIO OSS 原仓库已经归档并声明不再维护，需要选择仍在演进、许可证适合自建和商用、同时提供 S3 兼容接口的新方案。

RustFS 使用 Apache-2.0，项目当前活跃，并通过官方兼容矩阵公开记录 S3 已实现、未实现和排除的测试。EasyShare 所需的常见对象读写、Multipart 和 Presigned GET/PUT 已在其支持范围内。

截至本 ADR 日期，RustFS 最新公开版本仍为 `1.0.0-beta.10` 及 preview 构建；官方 README 将 Distributed Mode 和 RustFS KMS 标记为测试中，兼容矩阵也明确不声称完整 S3 兼容。因此“采用 RustFS”不等于“未经验证即可承载正式用户唯一数据副本”。

## 决策

### 产品与部署方向

- EasyShare 采用**自建对象存储优先**的部署路线；
- RustFS 是云端 MVP 和后续自建生产环境的首选对象存储实现；
- 阿里云 OSS、腾讯云 COS 等不再是默认实现，但保留为灾备、迁移或未来托管部署候选；
- PostgreSQL 仍保存文件身份、版本、上传会话和变更日志，RustFS 只保存不可变文件内容。

### 客户端与协议边界

Go 云端服务使用 AWS SDK for Go v2 访问 RustFS 的 S3 API，并封装在 EasyShare 自己的窄 `ObjectStore` 接口后面。业务代码不得依赖 RustFS 管理结构、控制台 API 或未标准化扩展。

MVP 只依赖并验证以下能力：

- Put/Get/Head/Delete Object；
- Create/UploadPart/Complete/Abort Multipart Upload；
- Presigned PUT/GET；
- Range Get；
- 用户 metadata；
- 私有 Bucket 和最小权限策略；
- TLS endpoint；
- 大小和 EasyShare SHA-256 完整性校验。

以下能力不作为 MVP 正确性的前提：

- Bucket Versioning；
- ACL；
- Bucket Access Logging；
- RustFS KMS；
- Bucket Replication；
- RustFS 特有管理 API；
- 尚未通过 EasyShare 测试的 Multipart 列举边界行为。

### 版本和环境

- 镜像必须固定明确版本和 digest，禁止生产使用 `latest`；
- alpha/开发环境可以使用当前选定 beta 版本，但数据可丢弃或有独立备份；
- 版本升级必须先在同数据规模的 staging 环境完成兼容、重启、回滚和恢复演练；
- RustFS 数据目录、PostgreSQL 和 EasyShare 服务分开持久化和备份；
- 单节点部署只允许用于开发、内部 alpha 或明确接受停机风险的场景。

### 生产启用门禁

正式承载不可丢失的用户数据前，必须同时满足：

1. 选定版本不是 preview；若仍为 beta，必须由维护者明确批准风险并限定用户范围；
2. EasyShare ObjectStore conformance suite 全部通过；
3. 连续运行、进程杀死、机器重启、磁盘满、网络分区和部分磁盘故障测试通过；
4. 从独立备份恢复随机抽样对象，并通过 SHA-256 核对；
5. 选定部署拓扑的节点/磁盘故障容忍得到实测，不仅引用产品说明；
6. 完成一次升级和一次失败回滚演练；
7. TLS、最小权限凭据、密钥轮换、审计和监控具备可执行方案；
8. 已提交内容至少存在一个与主 RustFS 故障域独立的可恢复副本；
9. PostgreSQL 中所有 `COMMITTED` 内容可以通过巡检任务在 RustFS 中找到并验证；
10. RustFS 不支持或未覆盖的 S3 行为不会被 EasyShare 调用。

如果分布式模式在准备生产时仍未达到项目要求，不能用“多节点配置存在”代替生产成熟度。可选择延迟正式发布、限定为单节点内部试用，或通过新 ADR 临时切换到已通过门禁的其他对象存储。

## 不变量

1. RustFS 不是文件元数据和版本的事实来源；PostgreSQL 才是。
2. EasyShare 内容身份始终使用明确算法的 SHA-256，不使用 ETag 作为完整文件哈希。
3. RustFS 中的对象 key 不包含用户名、文件名或用户路径。
4. 任何 `COMMITTED` 文件版本都指向已验证且可读取的对象。
5. 复制、纠删码和多磁盘都不能替代独立备份。
6. 选择 RustFS 不允许绕过上传会话、幂等提交和孤儿对象清理状态机。
7. 上游 README 或兼容矩阵声称支持的能力仍需通过 EasyShare 自己的测试。

## 备选方案

### 阿里云 OSS 或腾讯云 COS

不作为当前默认，因为项目明确选择自建。它们保留为未来托管版本、异地备份和迁移目标，不能让其专有能力进入业务模型。

### SeaweedFS

作为首要自建回退候选保留。若 RustFS 的稳定性、升级或分布式模式未通过生产门禁，应使用同一 conformance suite 对 SeaweedFS 重新评估。

### Ceph RGW

适合已有专业存储团队和大规模私有化，但当前阶段运维成本过高，保留为大型部署候选。

### MinIO OSS

拒绝作为新默认。其原开源仓库已归档并声明不再维护，后续 AIStor 需要按商业产品重新评估。

### 业务代码直接使用 RustFS 特有接口

拒绝。会提高迁移成本，也会把仍快速变化的产品管理面带入文件业务。

## 影响

正面影响：

- 对数据和部署拥有更多控制权；
- Apache-2.0 更适合自建与商业使用；
- S3 API 和 AWS SDK Go v2 便于建立可移植的数据面；
- Rust 实现和当前活跃开发符合项目采用新一代基础设施的方向。

成本与风险：

- EasyShare 团队承担磁盘、节点、监控、容量、备份、升级和安全响应；
- beta 阶段可能存在数据格式、兼容性和升级变化；
- 分布式与 KMS 当前不能直接视为成熟生产能力；
- 自建存储的总成本包含人力和故障成本，不只是服务器价格。

## 实施顺序

1. 建立固定版本的 RustFS Docker 开发环境；
2. 实现窄 `ObjectStore` 接口和 AWS SDK Go v2 RustFS adapter；
3. 建立独立于 RustFS 官方测试的 conformance suite；
4. 完成 Multipart 上传、幂等提交、下载和 SHA-256 闭环；
5. 添加 Prometheus/日志、容量和对象巡检；
6. 实现独立备份与恢复校验；
7. 确定生产拓扑并执行故障、升级和回滚测试；
8. 门禁通过后才把 ADR 的生产约束视为满足。

## 开放问题

- 开发环境已固定 `1.0.0-beta.10` 与 OCI index digest；生产候选版本和目标架构 manifest digest 仍需在生产评审时确定；
- 生产采用单节点多盘还是多节点多盘，以及对应故障容忍；
- 独立备份目标是第二套 RustFS、SeaweedFS、云对象存储还是离线介质；
- 服务器数量、磁盘类型、容量增长和机房/云主机位置；
- beta 到稳定版本的数据格式与滚动升级兼容；
- RustFS 原生监控、审计和告警能否满足要求，哪些需要 EasyShare 补充；
- KMS 达到稳定前采用磁盘加密还是其他服务端加密方案。

## 验证

除通用对象存储测试外，RustFS 专项验证至少包含：

- 1 B、零字节、100 MB、多 GB 文件；
- Multipart 中断、重复 part、重复 complete 和 abort 后清理；
- 预签名 URL 过期、Header 变化和时钟偏差；
- RustFS 进程在写入和 complete 期间被强制结束；
- 数据盘只读、磁盘满、单盘失效和节点不可达；
- 服务重启后的未完成 Multipart 与已提交对象；
- 相同对象的并发 Head/Get/Delete；
- 全库 `ContentObject` 与实际对象的双向巡检；
- 从独立备份恢复并逐对象 SHA-256 抽检；
- 当前版本升级到目标版本，再执行失败回滚。

## 当前官方证据

- [RustFS GitHub 仓库](https://github.com/rustfs/rustfs)
- [RustFS Releases](https://github.com/rustfs/rustfs/releases)
- [RustFS S3 Compatibility Matrix](https://github.com/rustfs/rustfs/blob/main/docs/architecture/s3-compatibility-matrix.md)
- [RustFS 官方文档](https://docs.rustfs.com/)
- [RustFS AWS SDK for Go 示例](https://docs.rustfs.com/developer/examples/aws-sdk-go)
- [RustFS Docker 安装](https://docs.rustfs.com/installation/docker)
- [RustFS 升级文档](https://docs.rustfs.com/upgrade-scale/upgrade)


