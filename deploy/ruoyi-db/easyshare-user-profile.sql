-- EasyShare 成员个人资料扩展（2026-10-27 企业化整改第二轮）。
--
-- 为什么头像存在这里、而不是用 RuoYi 的 sys_user.avatar + sys_oss：
--   1. RuoYi 的头像上传接口 /resource/oss/upload 需要 system:oss:upload 权限，
--      普通成员没有，等于「只有管理员能换头像」；
--   2. 我们的控制面只配了 EasyShare 自己的 RustFS（easyshare.drive.*），
--      RuoYi 的 sys_oss_config 未必可用。
-- 因此头像字节直接随控制面落库：一张 2MB 以内的图片、一个人一行，规模上完全不是问题，
-- 部署也只需这条 DDL，不再多一个存储组件。
--
-- 昵称与密码不在这里：它们走 RuoYi 标准的 /system/user/profile 接口，
-- 任何登录用户都有权改自己的，无需扩展表。
--
-- 用法：
--   docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-user-profile.sql

CREATE TABLE IF NOT EXISTS es_user_profile
(
    user_id     BIGINT      NOT NULL,
    avatar_mime VARCHAR(64),
    avatar_data BYTEA,
    create_dept BIGINT,
    create_by   BIGINT,
    create_time TIMESTAMP,
    update_by   BIGINT,
    update_time TIMESTAMP,
    remark      VARCHAR(500),
    CONSTRAINT pk_es_user_profile PRIMARY KEY (user_id)
);

COMMENT ON TABLE es_user_profile IS 'EasyShare 成员个人资料扩展（目前只有头像）';
COMMENT ON COLUMN es_user_profile.user_id IS '账号ID（sys_user.user_id）';
COMMENT ON COLUMN es_user_profile.avatar_mime IS '头像 MIME（按文件头嗅探，不信客户端申报）';
COMMENT ON COLUMN es_user_profile.avatar_data IS '头像原始字节，≤2MB';
