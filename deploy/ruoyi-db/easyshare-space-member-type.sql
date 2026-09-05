-- 共享空间授权主体泛化：账号 → 账号或部门（2026-09-06 部门级权限片 1）。
--
-- member_type：'user'=账号授权（user_id 列=用户ID，存量数据默认值，零迁移）、
--              'dept'=部门授权（user_id 列=部门 ID，列名不动、语义复用）。
-- 唯一索引随主体维度重建。生效权限 = max(个人行, 所属部门行)，write > read。

ALTER TABLE es_space_member ADD COLUMN IF NOT EXISTS member_type CHAR(8) NOT NULL DEFAULT 'user';

DROP INDEX IF EXISTS uk_es_space_member;
CREATE UNIQUE INDEX IF NOT EXISTS uk_es_space_member
    ON es_space_member (space_id, member_type, user_id);

COMMENT ON COLUMN es_space_member.member_type IS '授权主体类型：user 账号、dept 部门（user_id 列存对应 ID）';
