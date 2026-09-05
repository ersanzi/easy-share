# Upload Session — Multipart 断点续传（网盘 P0 收官切片）

- 日期：2026-09-06（同日第三片，接续 es_file 目录层 5e8b71a）
- 参考：[`cloudreve-benchmark.md`](../cloudreve-benchmark.md) §3.3「Upload Session 必须与目录记录联动」
- 状态：**已完成**（Java 32/32 全绿 +8 用例；go build/test 全绿 +5 用例；真机大文件断网续传随部署验收）

## 1. 设计决策（全部按对标验收门槛逐条对齐）

- **会话记账在控制面，ETag 清单在客户端**：服务端只存路由与状态所需最小字段
  （sessionId/uploadId/partSize/申报大小/status），分片 ETag 清单持久化在客户端
  本地会话文件——断点续传的"断点"落本机，服务端不背大 JSON。
- **幂等 Complete**：已完成会话重复 Complete 直接返回既有 fileId，不再触 S3
  （uploadId 已被消费，二次提交必 NoSuchUpload）；已放弃会话明确报错让客户端重开。
- **防孤儿分片**：create 对同路径同用户的遗留 uploading 会话先 S3 Abort 再新建，
  不让分片垃圾吃容量与配额。
- **配额同口径**：create 走与单请求上传相同的 `checkXxxWritable` 软上限；
  part size 服务端定（`easyshare.drive.part-size` 默认 8MB，S3 上限 10000 片支持
  到 80GB）——技术参数自动推断，客户端不选。
- **目录层联动**：Complete 成功后 upsert es_file（分片聚合完毕，申报大小即真实
  大小）并作废用量缓存；返回 fileId。
- **客户端自动分流**：`UploadFile` 内部按大小（≥32MB）走会话分片或单请求直传，
  调用方（面板/拖拽/悬浮窗/文件夹遍历）零改动；会话持久化目录在 `driveClient()`
  装配（数据根 `upload-sessions/`）。

## 2. 实现

| 位置 | 内容 |
| --- | --- |
| `deploy/ruoyi-db/easyshare-upload-session.sql`（新） | es_upload_session DDL（状态机 0 进行中/1 完成/2 放弃） |
| `domain/EsUploadSession` / `mapper/*`（新） | 会话实体与 Mapper |
| `DriveStorage`（扩展） | createUpload / presignPartAt / completeUpload（CompletedMultipartUpload）/ abortUpload 四方法 + UploadPart record |
| `service/DriveUploadService.java`（新） | create（配额+防泄漏）/ presignPart（归属强校验 uploadBy）/ complete（幂等+目录层联动）/ abort |
| `DriveController`（扩展） | `/upload-session/create|part|complete|abort` 四端点 |
| `internal/drive/upload_session.go`（新） | SessionStore（指纹=空间+路径+大小+mtime，原子写）/ createSession/presignSessionPart/completeSession/abortSession / 分片循环（跳过已完成分片、逐片落盘快照、分片重试 3 次退避）/ 幂等 Complete |
| `internal/drive/upload.go` | UploadFile 大小分流（≥32MB 走会话） |
| `app.go` | driveClient 装配 SessionStore（`%LOCALAPPDATA%\EasyShare\upload-sessions`） |

**踩坑两条**：① AWS SDK v2 的 Complete 请求体是 `multipartUpload(CompletedMultipartUpload)`
（不是 `completedMultipartUpload`，也非 `MultipartUpload` 类型——javap 查证）；
② 分片重试的 body 必须**每次重建 reader**（io.SectionReader 读过一次即到尾，
复用会让重试发空 body）。

## 3. 验证

- Java：`mvnw -pl ../platform-drive test` **32/32**（+8：配额联动/防孤儿/幂等
  Complete/归属强校验/放弃语义）。
- Go：`go test ./...` 全绿（+5：三片切分与 Complete、快照续传只补剩余分片并清理、
  指纹变化新建会话、分片重试退避、指纹函数性质）。
- 真机大文件断网续传（传一半杀进程→重开续传）随 Linux 部署验收一并做；
  DDL `easyshare-upload-session.sql` 需在服务器应用（已并入 tasks.md 部署提醒）。

## 4. 网盘 P0 至此收官

fileId 目录层（5e8b71a）+ Upload Session（本片）+ 目录导航（1cd583a）三者合龙，
对标 §5-P0 五步全部落地。回收站/版本（es_file 加列）、缩略图缓存、分享记录管理
进入 P1 候选，按用户优先级排期。
