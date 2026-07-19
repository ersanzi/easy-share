# ADR-0004：本地状态与任务持久化

- 状态：提议
- 日期：2026-07-19
- 决策者：EasyShare maintainers
- 关联：[`../architecture.md`](../architecture.md)、[`0003-incremental-sync-and-conflicts.md`](0003-incremental-sync-and-conflicts.md)

## 背景

当前传输任务保存在内存，Core 退出后丢失。云端上传、下载、同步和 CfAPI 水合可能持续数小时，并在断网、睡眠、升级或崩溃后恢复。JSON 配置文件无法安全承担并发状态更新、分页查询、事务和数据库迁移。

未来 UI、Core 和 Windows Helper 是多个进程。如果多个进程直接读写同一个状态库，将产生锁、迁移、所有权和崩溃恢复问题。

## 决策

Go Core 使用 **SQLite + WAL** 作为客户端业务状态的唯一持久化数据库，并且是唯一写入者。UI 和 Windows Helper 通过版本化 Core API/IPC 查询或提交意图，不直接打开数据库。

配置项仍保存在现有 `config.json`，但账号状态、元数据、同步游标和任务迁入 SQLite。Token 私密材料不以明文保存在数据库，使用 Windows Credential Manager 或 DPAPI 保护后再引用。

建议最小表域：

```text
schema_migrations
accounts / device_sessions
sync_roots
remote_entries / remote_versions
local_bindings
sync_cursors
transfer_tasks
upload_parts
pending_operations
conflicts
cache_entries
```

统一任务信封：

```text
Task
- id
- type                  cloud_upload | cloud_download | lan_send |
                        lan_receive | hydrate | dehydrate
- source_ref / target_ref
- file_id / version_id / content_hash
- state                 pending | running | paused | retry_wait |
                        succeeded | failed | canceled
- priority
- bytes_total / bytes_transferred
- attempt_count / next_retry_at
- error_code / error_detail_safe
- created_at / updated_at
```

不同任务类型使用独立执行器和类型化 payload；统一的是生命周期、队列、进度和恢复，不强迫所有传输共享一个庞大的协议实现。

Core 启动恢复规则：

1. 执行向前兼容的数据库迁移；
2. 将上次遗留的 `running` 任务恢复为可重新判断的中间状态，而不是假设失败或成功；
3. 重新验证本地临时文件、upload ID、已完成分片和远端状态；
4. 根据网络、用户暂停状态、优先级和重试时间重新调度；
5. 通过 Core 事件 API 向 UI 发布快照和后续变化。

## 不变量

1. Core 是本地业务数据库的唯一写入者和迁移执行者。
2. cursor 前进和本地变更应用在同一 SQLite 事务中完成。
3. 任务成功只在结果已经可验证时写入，不以“请求已发送”作为成功。
4. 进度事件可以丢失，数据库中的任务状态必须可重新查询。
5. 缓存回收不得删除未上传成功的唯一内容、固定保留内容或正在使用的临时对象。
6. 数据库升级失败时停止相关服务并保留原库，不静默重建丢失状态。

## 备选方案

### 继续使用内存任务

拒绝。无法满足断点续传、升级和重启恢复。

### 使用 JSON 文件保存任务数组

拒绝。并发更新、原子事务、索引查询和 schema migration 都不可靠。

### UI、Core、Helper 共同访问 SQLite

拒绝。会扩大数据库协议面，使迁移和锁处理耦合到 Explorer 生命周期。

### 客户端运行 PostgreSQL 或其他服务数据库

拒绝。部署和资源成本不适合桌面客户端，SQLite 已满足单 Core 写入场景。

## 影响

正面影响：

- 任务、游标和缓存状态可以原子更新并在重启后恢复；
- UI 不需要维护另一套事实状态；
- 数据迁移和诊断入口集中在 Core。

成本与风险：

- 需要数据库迁移、备份和损坏处理策略；
- 高频进度不能每个字节都落库，需要节流；
- SQLite 文件包含文件名等隐私元数据，诊断导出必须脱敏。

## 开放问题

- SQLite 驱动选择：纯 Go 或启用 CGO；
- 数据库和缓存的最终目录；
- 成功任务和历史记录保留期限；
- 进度持久化的时间/字节阈值；
- 数据库损坏时自动恢复与人工导出策略；
- 凭据引用和 DPAPI/Credential Manager 的具体封装。

## 验证

至少覆盖：

- 上传和下载过程中强制结束 Core，再启动恢复；
- WAL 模式下机器断电模拟和数据库完整性检查；
- 旧 schema 逐版本升级；
- 重复任务、取消与暂停竞态；
- UI/Helper 无法直接绕过 Core 修改状态；
- 百万级历史任务或元数据下的分页与索引性能；
- 磁盘已满时不破坏已存在数据库和本地唯一内容。
