# 迭代：网盘回收站 + 版本（P1 首片，双对标落地）

- 开工：2026-10-14（用户直配轮：「现在的云盘功能太糙了，学习完善下」）
- 依据：[`wps-cloud-drive-benchmark.md`](../wps-cloud-drive-benchmark.md) §6-P1 第 1 项 + [`cloudreve-benchmark.md`](../cloudreve-benchmark.md) §5-P1——两份独立对标共同指向「回收站 + 版本」为当前最大缺口
- 口径变更：用户明确解除 2026-10-13 起的「网盘数据面两周观察期」，允许动 es_file 表结构
- 验收口径：代码 + 三层单测全绿 → 本地 dev 库 DDL 幂等重放验证 → **连真实服务器（192.168.1.239）端到端联调**（用户拍板）

## 范围

做：
1. es_file 加列：`state`（active/recycled/purged）、`deleted_at`、`origin_path`、`origin_parent_id`、`version_id`；DDL 幂等（`deploy/ruoyi-db/easyshare-file-recycle.sql`）
2. `uk_es_file_path` 唯一索引改为部分索引（`WHERE state='active'`），回收站内同路径不冲突
3. 删除改软删（保留 RustFS 对象）；列表只出 active
4. 回收站 API：列表 / 还原（origin_path 复位，原路径被占则报可读冲突）/ 彻底删除（连对象一起删）
5. 版本号：同名覆盖上传时 `version_id` 单调递增（历史列表/回滚不做，留字段）
6. Go Core 封装 + 版本化路由；前端网盘页「回收站」视图（列表/还原/彻底删除，破坏性操作有确认）

不做：重命名/移动、错误三件套、多选批量、版本历史 UI、星标/标签（P2 排期见对标文档 §6）

## 验收门槛（对标 §9）

1. 挂 fileId，不新增路径绑定
2. 删除可还原、还原到原位置
3. 破坏性操作前有确认，失败原因对人可读
4. 不新增普通用户需要理解的技术设置
5. 与阶段 5 共用同一文件模型，不产生第二套真相源

## 结果

（完成后回填）
