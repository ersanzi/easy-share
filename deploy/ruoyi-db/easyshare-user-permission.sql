-- EasyShare 成员功能权限矩阵（2026-10-27 企业化整改第二轮）。
--
-- 一行 = 一个账号的显式权限配置；**没有行 = 五项全开**（存量账号零迁移）。
-- 刻意不写入默认行：新账号、老账号都靠「缺行即全开」得到开箱可用的行为，
-- 只有管理员真正关过某项的账号才会在这里留下痕迹。
--
-- 五个开关对应桌面端五块能力：
--   knowledge 知识问答、meeting 会议听记、drive 网盘、shared 共享空间、plugin 插件中心。
-- drive 关闭时 shared 一并失效（共享空间是网盘的一个空间），管理员不需要连关两项。
--
-- 超管不特殊对待：管理页本身（/easyshare/permission/admin、/system/user/*）不在
-- 这些开关的管辖范围内，管理员即便关掉自己的某项，也能回到管理页改回来，不存在
-- 「把自己锁在门外」的死循环。
--
-- 用法：
--   docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-user-permission.sql

CREATE TABLE IF NOT EXISTS es_user_permission
(
    user_id     BIGINT       NOT NULL,
    knowledge   BOOLEAN      NOT NULL DEFAULT TRUE,
    meeting     BOOLEAN      NOT NULL DEFAULT TRUE,
    drive       BOOLEAN      NOT NULL DEFAULT TRUE,
    shared      BOOLEAN      NOT NULL DEFAULT TRUE,
    plugin      BOOLEAN      NOT NULL DEFAULT TRUE,
    create_dept BIGINT,
    create_by   BIGINT,
    create_time TIMESTAMP,
    update_by   BIGINT,
    update_time TIMESTAMP,
    remark      VARCHAR(500),
    CONSTRAINT pk_es_user_permission PRIMARY KEY (user_id)
);

COMMENT ON TABLE es_user_permission IS 'EasyShare 成员功能权限（缺行=全开）';
COMMENT ON COLUMN es_user_permission.user_id IS '账号ID（sys_user.user_id）';
COMMENT ON COLUMN es_user_permission.knowledge IS '知识问答：知识库查询与入库';
COMMENT ON COLUMN es_user_permission.meeting IS '会议听记：录音转写与纪要';
COMMENT ON COLUMN es_user_permission.drive IS '网盘：个人空间文件读写';
COMMENT ON COLUMN es_user_permission.shared IS '共享空间：需同时开启 drive';
COMMENT ON COLUMN es_user_permission.plugin IS '插件中心：扩展的安装与使用';
