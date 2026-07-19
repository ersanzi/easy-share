# RustFS 对象存储基础层

> 日期：2026-07-19
>
> 状态：基础实现和离线验证已完成；真实 RustFS 验证待 Docker daemon 恢复
>
> 主题：建立可迁移、可测试的自建对象存储数据面

## 目标

- 提供固定版本、仅绑定本机的 RustFS 开发环境，避免使用会漂移的 `latest` 镜像。
- 在业务代码与 S3 SDK 之间建立窄 `ObjectStore` 接口，不向上层泄漏 AWS 或 RustFS 类型。
- 使用 AWS SDK for Go v2 实现 RustFS adapter，并强制 path-style addressing。
- 覆盖 Multipart 上传、预签名分片、完成/中止、对象查询、预签名下载和删除。
- 建立默认可离线运行的单元测试，以及显式启用的 RustFS 一致性测试。

## 非目标

- 本次不接入 PostgreSQL，不实现文件元数据、版本表、上传会话表或幂等提交事务。
- 本次不实现云端 HTTP API、身份认证、租户隔离、桌面 UI 或 Windows CFAPI。
- 本次不建设生产 RustFS 集群、监控、备份、容灾、KMS 或升级流水线。
- 本次不表示 RustFS 已通过生产门禁；生产启用仍受 [ADR-0006](../adr/0006-rustfs-self-hosted-object-storage.md) 约束。

## 设计与兼容边界

- 数据面使用 AWS SDK for Go v2 和 S3 兼容 API；不调用 RustFS 管理面专有接口。
- `ObjectStore` 使用 EasyShare 自己的输入、输出和错误类型；provider 的 ETag 只作为 Multipart part token 或诊断信息，不作为内容哈希。
- 文件内容身份继续使用 SHA-256。未来 PostgreSQL 是文件元数据和版本事实来源，RustFS 只保存内容对象。
- 对象 key 由未来的内容/版本层生成，不允许在 adapter 内嵌用户路径规则。
- RustFS 要求 path-style addressing；adapter 固定开启该选项。
- 开发环境允许显式配置 HTTP，其他环境应使用 HTTPS。adapter 默认拒绝明文 endpoint。

## 实现任务

- [x] 固定 RustFS `1.0.0-beta.10` 镜像 tag 与 OCI index digest。
- [x] 增加 `deploy/rustfs` Compose 配置、示例环境变量和操作说明。
- [x] 实现 provider-neutral `ObjectStore` 类型、校验和错误分类。
- [x] 实现内存 fake，供状态机单元测试使用。
- [x] 实现 RustFS S3 adapter。
- [x] 增加单元测试和 opt-in RustFS 集成测试。
- [x] 运行 `gofmt`、`go test ./...` 和文档链接检查。

## 验收标准

1. `go test ./...` 在未安装或未启动 RustFS 时通过，集成测试明确显示跳过。
2. 非法 endpoint、明文 HTTP 未获许可、空凭证、空 bucket/key、非法 part number 和非法过期时间均被本地拒绝。
3. Multipart complete 前按 part number 排序并拒绝重复 part。
4. SDK 错误映射到可用 `errors.Is` 判断的 provider-neutral 类别。
5. 启用集成测试后完成：创建 Multipart、通过预签名 URL 上传、完成、Head、预签名下载、SHA-256 比对、删除与中止。
6. Compose 仅在 `127.0.0.1` 暴露开发端口，凭证不进入仓库。

## 验证记录

- 当前机器 Docker CLI 与 Compose 可用，但 Docker daemon 不可用，因此本次可以验证 Compose 静态配置，不能声称已运行真实 RustFS 集成测试。
- 镜像 OCI index digest 已通过 Docker Registry API 解析；实际拉取与目标架构 manifest 仍应在 Docker daemon 恢复后验证。
- `docker compose --env-file .env.example config`：通过，确认固定镜像、loopback 端口、环境变量和 named volumes 可解析。
- `go test ./...`：通过；现有 Core、WebDAV 和局域网传输测试未回归。
- `go vet ./internal/cloud/objectstore/...`：通过。
- `go test ./internal/cloud/objectstore/s3store -run '^TestRustFSIntegration$' -count=1 -v`：按设计跳过，因为未设置显式集成开关且 Docker daemon 不可用。
- 全部相对 Markdown 链接检查：通过。

## 已知风险与后续工作

- RustFS 当前版本仍为 beta；分布式模式、KMS、故障恢复和升级兼容必须另行验证。
- 下一步应在 Docker daemon 可用后执行真实一致性测试，再实现 PostgreSQL 元数据和上传会话状态机。
- 生产前必须补齐监控、容量告警、独立备份、恢复演练、TLS、密钥轮换和故障注入。

## 回滚

本次模块尚未接入现有 Core 运行路径。回滚时移除 `internal/cloud/objectstore`、`deploy/rustfs` 和新增 SDK 依赖即可，不影响当前局域网传输及 WebDAV 功能。


