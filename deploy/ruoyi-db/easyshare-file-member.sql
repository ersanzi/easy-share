-- 文档级成员角色（企业权限第三片，2026-11-05）。
--
-- 一行 = 一个主体（账号或部门）对一份文件的显式角色，四档：
--   manage   可管理：预览/下载/改名/删除/分享/上传新版/授权他人/改可见性
--   edit     可编辑：预览/下载/上传新版/改名/分享（删除是 manage 独占，对齐钉钉）
--   download 可查看下载：预览/下载
--   read     仅可查看：仅预览，不能下载
--
-- 裁决顺序（FileRoleService.effective，一处收口，列表与拦截共用）：
--   上传者/空间所有者 → manage（隐式，不落表）
--   → 本表成员行（user 行与 dept 行取高，覆盖一切默认）
--   → es_file.visible_depts 命中本人部门 → download（沿用既有语义：可见即可下载）
--   → 文件无 visible_depts 限制时按空间权限取默认（write 成员 manage / read 成员 download，
--     与「未配角色的文件行为与今天完全一致」的兼容前提对齐）
--   → 其余不可见。
--
-- 护栏（服务端强制）：不能修改自己的角色行；仅上传者/空间所有者/manage 档可授权。
--
-- 用法：
--   docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-file-member.sql

CREATE TABLE IF NOT EXISTS es_file_member
(
    file_id     BIGINT      NOT NULL,
    member_type VARCHAR(8)  NOT NULL,
    member_id   BIGINT      NOT NULL,
    role        VARCHAR(16) NOT NULL,
    create_by   BIGINT,
    create_time TIMESTAMP,
    update_by   BIGINT,
    update_time TIMESTAMP,
    CONSTRAINT pk_es_file_member PRIMARY KEY (file_id, member_type, member_id)
);

CREATE INDEX IF NOT EXISTS idx_es_file_member_member ON es_file_member (member_type, member_id);

COMMENT ON TABLE es_file_member IS 'EasyShare 文档级成员角色：一个主体对一份文件的显式角色（四档）';
COMMENT ON COLUMN es_file_member.file_id IS '文件ID（es_file.file_id）';
COMMENT ON COLUMN es_file_member.member_type IS '主体类型：user=账号 / dept=部门（沿用 es_space_member 双主体口径）';
COMMENT ON COLUMN es_file_member.member_id IS '主体ID（sys_user.user_id 或 sys_dept.dept_id）';
COMMENT ON COLUMN es_file_member.role IS '角色档位：manage/edit/download/read，档位间取高裁决';
