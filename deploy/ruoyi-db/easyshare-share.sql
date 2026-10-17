-- EasyShare 文件分享（长效、可撤销、可审计）。
--
-- 与预签名 URL 的区别：预签名是「一次授权、无记录、撤不回」，只能给 10 分钟；
-- 分享是「一条记录、可撤销、能统计访问」，才能支撑「发给外部同事」这个动作。
-- 现状 CloudShare 返回的正是预签名 URL（见 app.go 注释），本轮补上真正的分享层。
--
-- 设计要点：
--   1. token 是 URL 里那串随机码，base62 22 位 ≈ 128 bit 熵：
--      不依赖自增、不可枚举、泄露单条不影响其他。
--   2. file_name / file_size 是**创建时的快照**。文件后续被删或改名，
--      分享列表仍要能显示「我分享过什么」，不能因为 join 不到就变成一行空白。
--   3. 失效有两个来源：expires_at 到期、revoked 主动撤销。两者独立，
--      查询时都要判断——撤销必须立刻生效，不能等过期。
--   4. 同一文件允许多条分享记录（发给不同的人、不同有效期），不做唯一约束。
--   5. 幂等：整脚本可重复执行。
--
-- 执行：
--   docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-share.sql

CREATE TABLE IF NOT EXISTS es_share
(
    share_id    BIGINT       NOT NULL,
    token       VARCHAR(32)  NOT NULL,
    owner_id    BIGINT       NOT NULL,
    space_type  VARCHAR(16)  NOT NULL,
    file_path   VARCHAR(768) NOT NULL,
    file_name   VARCHAR(255) NOT NULL,
    file_size   BIGINT       NOT NULL DEFAULT 0,
    expires_at  TIMESTAMP    NOT NULL,
    revoked     BOOLEAN      NOT NULL DEFAULT FALSE,
    visit_count INT          NOT NULL DEFAULT 0,
    created_at  TIMESTAMP    NOT NULL,
    CONSTRAINT pk_es_share PRIMARY KEY (share_id)
);

COMMENT ON TABLE es_share IS '文件分享记录：一条记录对应一个可撤销的外链';
COMMENT ON COLUMN es_share.token IS 'URL 中的随机码（base62，22 位）；不可枚举，泄露单条不影响其他分享';
COMMENT ON COLUMN es_share.owner_id IS '创建分享的用户';
COMMENT ON COLUMN es_share.space_type IS '空间类型：personal / shared';
COMMENT ON COLUMN es_share.file_path IS '分享时文件的空间内相对路径';
COMMENT ON COLUMN es_share.file_name IS '创建时的文件名快照：文件被删后列表仍可读';
COMMENT ON COLUMN es_share.file_size IS '创建时的字节数快照';
COMMENT ON COLUMN es_share.expires_at IS '到期时间；到期后 download 一律拒绝';
COMMENT ON COLUMN es_share.revoked IS '是否已主动撤销；撤销立即生效，不等过期';
COMMENT ON COLUMN es_share.visit_count IS '成功下载次数（302 跳转计数，非精确审计）';

-- token 是外部入口，必须唯一且走索引
CREATE UNIQUE INDEX IF NOT EXISTS uk_es_share_token
    ON es_share (token);

-- 「我的分享」按创建时间倒序
CREATE INDEX IF NOT EXISTS idx_es_share_owner
    ON es_share (owner_id, created_at DESC);
