-- 文档级可见性（部门级权限片 2 数据面，2026-09-06）。
--
-- visible_depts 语义（仅共享空间文件生效；个人空间 owner 独占无需此列参与）：
--   NULL/空串  = 共享空间全体成员可见（默认，存量行为不变）
--   "3,7"      = 仅这些部门 ID 的成员可见（上传者本人始终可见）
-- 裁剪点：控制面 /objects 列举（网盘页/挂载盘/快搜文件路全部经此出口）。
-- 知识检索侧（/query）的联动依赖知识服务用户-部门模型，见独立切片。

ALTER TABLE es_file ADD COLUMN IF NOT EXISTS visible_depts VARCHAR(500);

COMMENT ON COLUMN es_file.visible_depts IS '文档可见部门 ID 逗号分隔；空=全体可见（仅共享空间语义）';
