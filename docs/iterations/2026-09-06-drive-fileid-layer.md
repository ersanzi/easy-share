# 云盘目录层 es_file — 稳定 fileId（Cloudreve 对标 P0 正主）

- 日期：2026-09-06（用户定调：**后续逻辑优先放 Java 控制面**，Go 仅在确需时动——本切片即首个按此方向执行的 P0）
- 参考：[`cloudreve-benchmark.md`](../cloudreve-benchmark.md) §3.1/3.2/§5-P0；ADR-0007
- 状态：**已完成**（模块单测 24 全绿 +8 用例；go/vitest/vue-tsc 全绿；客户端向后兼容零强制改动）

## 1. 边界与设计决策

- **es_file 是元数据索引，不是存在性真相源**：真实内容在 RustFS，控制面不在上传数据
  路径上（预签名直传）。列表永远以存储列举为准，es_file 只补 fileId 与归属——
  避免重蹈"库里用量字段必然腐烂"的覆辙（es_space 同一设计哲学）。
- **登记时机双轨**：① presignPut 签发时幂等 upsert（申报大小，多数文件自此有身份）；
  ② 列表时惰性补账——目录层上线前的存量对象第一次被列举即自愈归账，fileId 全量覆盖。
- **幽灵行无害**：登记后未真正写入（签名过期/上传失败）的行不影响列表正确性，
  留给删除清理与后续对账切片。
- **归属键**：`(space_type, owner_id, file_path)` 唯一。personal owner=用户、shared
  owner=0（与 es_space 共享单例口径一致）；`upload_by` 记真实上传者（共享空间区分
  上传者；惰性补账的存量行记 0=无法归因）。shared 的写权限由 es_space_member 把守，
  与 es_file 无关。
- **过渡期双轨**：删除支持 fileId（新链路，含归属校验防伪造他人 fileId）或路径
  （旧客户端零改动）；列表响应加 `fileId` 字段（JSON 加字段向后兼容）。

## 2. 实现

| 位置 | 内容 |
| --- | --- |
| `deploy/ruoyi-db/easyshare-file.sql`（新） | DDL：es_file 表 + 唯一索引 + 注释（设计要点四条写进 SQL 头） |
| `domain/EsFile.java` / `mapper/EsFileMapper.java`（新） | MyBatis-Plus 实体与 Mapper（BaseEntity 惯例同 EsSpace） |
| `service/DriveFileService.java`（新） | `registerOnPresign`（幂等 upsert，并发撞唯一键回退已存在行）/ `reconcileAndMap`（列表补账+回填）/ `resolveDeletePath`（fileId 归属校验）/ `deleteRegistered` |
| `DriveController.java` | `/objects` 回填 fileId（新 FileVo）；`/presign-put` 登记；`/object` 删除支持 `fileId`（PathBo 加可选字段，路径校验注解随之放开） |
| 客户端透传 | Go `drive.Object` + `cloud.File` 加 `FileId`；前端 `CloudFile` 加 `fileId`；Wails 绑定重生成。旧控制面响应无此字段时客户端为 0，行为不变 |
| `DriveFileServiceTest.java`（新） | 8 用例：规范化登记 / 申报大小更新 / 未知大小不覆盖 / 撞键回退 / 补账映射 / fileId 越权拒绝 / 路径兜底 / 删除清理 |

**踩坑记录（省的下次还有问题）**：① RuoYi 父 POM 默认 `maven.test.skip=true`，跑模块
测试必须 `-Dmaven.test.skip=false`；② 本机 `JAVA_HOME` 指向 java17，编译需
`JAVA_HOME=D:/Develop/java21`（GraalVM 21 也在 PATH，但 Maven 认 JAVA_HOME）；
③ Mockito 桩要贴着**产品代码的真实调用序**写——insertSafely 是"先插、撞键后补查"，
假设"先查后插"的桩会莫名失败（调试 20 分钟的教训：先打印调用序再改桩）。

## 3. 验证

- `platform-drive`：`mvnw -pl ../platform-drive test` **24/24 全绿**（+8）。
- 客户端：`go build ./... && go test ./...` 全绿；前端 `vue-tsc` 干净、vitest 40 全绿。
- 真机/服务器验收依赖：控制面库应用 `easyshare-file.sql` + 重新 ship jar——归入
  Linux 服务器端到端验收一并做（tasks.md 用户动作区）。

## 4. 下一步（网盘 P0 剩余，按对标顺序）

1. **Upload Session + Multipart 断点续传**：控制面加 Create/PresignPart/Complete
   端点（幂等 Complete、重启恢复、part 参数 Core 自动推断）；
2. **回收站/版本**：es_file 加 state/version 列（本切片 DDL 已预留演进空间）；
3. 分享记录管理、缩略图缓存（fileId 键控）。
