-- EasyShare 权限角色（权限组）与子管理员范围（2026-10-27 企业化整改第三轮；2026-11-05 扩范围档位）。
--
-- 一张 es_permission_role = 一个权限组：五项功能开关放在组上，成员继承组的并集。
-- admin_scope != 'none' 的角色同时是「子管理员」：该成员可以打开管理页的权限矩阵，
-- 管理范围按档位裁剪：
--   dept           本部门（sys_user.dept_id 精确匹配）
--   dept_children  本部门及下级部门（按 sys_dept.parent_id 内存 BFS）
--   specified      指定部门列表（scope_dept_ids，逗号分隔 sys_dept.dept_id）
--   all            全部成员
-- 多个管理角色取并集。admin_scope = none 是普通权限组，只影响功能开关。
--
-- 裁决顺序（FeaturePermissionService）：
--   有角色 → 角色五项取并集；无角色 → 沿用 es_user_permission 逐账号行；再缺行 → 全开。
-- 逐账号行在用户有角色时不参与裁决，但**不删除**——移除角色后回到原有单独配置。
--
-- 删除角色时，只挂这一个角色的成员会落一行 es_user_permission 快照，
-- 避免「删个角色把成员权限放大成全开」。
--
-- 用法：
--   docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-permission-role.sql

CREATE TABLE IF NOT EXISTS es_permission_role
(
    role_id        BIGINT       NOT NULL,
    role_name      VARCHAR(64)  NOT NULL,
    admin_scope    VARCHAR(16)  NOT NULL DEFAULT 'none',
    scope_dept_ids TEXT,
    knowledge      BOOLEAN      NOT NULL DEFAULT TRUE,
    meeting        BOOLEAN      NOT NULL DEFAULT TRUE,
    drive          BOOLEAN      NOT NULL DEFAULT TRUE,
    shared         BOOLEAN      NOT NULL DEFAULT TRUE,
    plugin         BOOLEAN      NOT NULL DEFAULT TRUE,
    create_dept    BIGINT,
    create_by      BIGINT,
    create_time    TIMESTAMP,
    update_by      BIGINT,
    update_time    TIMESTAMP,
    CONSTRAINT pk_es_permission_role PRIMARY KEY (role_id)
);

-- 存量库补列（2026-11-05 数据权限范围片）：幂等，可重复执行
ALTER TABLE es_permission_role ADD COLUMN IF NOT EXISTS scope_dept_ids TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS uk_es_permission_role_name ON es_permission_role (role_name);

COMMENT ON TABLE es_permission_role IS 'EasyShare 权限角色（权限组）：五项功能开关 + 子管理员范围';
COMMENT ON COLUMN es_permission_role.role_name IS '角色名（全局唯一，管理页按此展示）';
COMMENT ON COLUMN es_permission_role.admin_scope IS '管理范围：none=普通权限组；dept=本部门；dept_children=本部门及下级；specified=指定部门；all=全部成员';
COMMENT ON COLUMN es_permission_role.scope_dept_ids IS '指定部门范围：逗号分隔的 sys_dept.dept_id，仅 admin_scope=specified 时生效';
COMMENT ON COLUMN es_permission_role.knowledge IS '知识问答：知识库查询与入库';
COMMENT ON COLUMN es_permission_role.meeting IS '会议听记：录音转写与纪要';
COMMENT ON COLUMN es_permission_role.drive IS '网盘：个人空间文件读写';
COMMENT ON COLUMN es_permission_role.shared IS '共享空间：需同时开启 drive';
COMMENT ON COLUMN es_permission_role.plugin IS '插件中心：扩展的安装与使用';

CREATE TABLE IF NOT EXISTS es_user_role
(
    user_id BIGINT NOT NULL,
    role_id BIGINT NOT NULL,
    CONSTRAINT pk_es_user_role PRIMARY KEY (user_id, role_id)
);

CREATE INDEX IF NOT EXISTS idx_es_user_role_role ON es_user_role (role_id);

COMMENT ON TABLE es_user_role IS 'EasyShare 成员与权限角色的绑定（多对多，权限取并集）';
COMMENT ON COLUMN es_user_role.user_id IS '账号ID（sys_user.user_id）';
COMMENT ON COLUMN es_user_role.role_id IS '角色ID（es_permission_role.role_id）';
