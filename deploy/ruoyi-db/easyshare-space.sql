-- EasyShare 空间授权与配额模型（PostgreSQL）。
--
-- 放在 EasyShare 仓内而非 platform/script/sql/：platform/ 是上游 RuoYi 的 clone 且被
-- .gitignore 忽略，产品自己的 DDL 放进去会脱离版本管理。
--
-- 用法：
--   docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-space.sql
--
-- 设计要点：
--   1. 一行一个空间。个人空间 owner 独占，共享空间靠 es_space_member 授权。
--   2. 只存**配额上限**，不存已用量。已用量的真值在 RustFS，控制面签完预签名 URL 后
--      客户端直传，控制面不在数据路径上——落库的用量必然与真实脱节。用量一律实时
--      list 聚合（见 SpaceUsageService），库里不留会腐烂的镜像字段。
--   3. quota_bytes：0 表示未分配（客户端显示「待开空间」），-1 表示不限。

CREATE TABLE IF NOT EXISTS es_space
(
    space_id    BIGINT       NOT NULL,
    space_type  VARCHAR(16)  NOT NULL,
    owner_id    BIGINT       NOT NULL DEFAULT 0,
    space_name  VARCHAR(64)  NOT NULL,
    quota_bytes BIGINT       NOT NULL DEFAULT 0,
    status      CHAR(1)      NOT NULL DEFAULT '0',
    tenant_id   VARCHAR(20)  DEFAULT '000000',
    create_dept BIGINT,
    create_by   BIGINT,
    create_time TIMESTAMP,
    update_by   BIGINT,
    update_time TIMESTAMP,
    remark      VARCHAR(500),
    CONSTRAINT pk_es_space PRIMARY KEY (space_id)
);

COMMENT ON TABLE es_space IS 'EasyShare 空间';
COMMENT ON COLUMN es_space.space_id IS '空间ID';
COMMENT ON COLUMN es_space.space_type IS '空间类型：personal 个人、shared 共享';
COMMENT ON COLUMN es_space.owner_id IS '归属用户ID，共享空间为 0';
COMMENT ON COLUMN es_space.space_name IS '空间名称';
COMMENT ON COLUMN es_space.quota_bytes IS '配额字节数：0 未分配、-1 不限';
COMMENT ON COLUMN es_space.status IS '状态：0 正常、1 停用';

-- 个人空间一人一个：唯一索引挡住重复开通，也让「按用户查空间」走索引。
CREATE UNIQUE INDEX IF NOT EXISTS uk_es_space_owner
    ON es_space (space_type, owner_id);

CREATE TABLE IF NOT EXISTS es_space_member
(
    id          BIGINT      NOT NULL,
    space_id    BIGINT      NOT NULL,
    user_id     BIGINT      NOT NULL,
    permission  VARCHAR(8)  NOT NULL DEFAULT 'read',
    tenant_id   VARCHAR(20) DEFAULT '000000',
    create_dept BIGINT,
    create_by   BIGINT,
    create_time TIMESTAMP,
    update_by   BIGINT,
    update_time TIMESTAMP,
    CONSTRAINT pk_es_space_member PRIMARY KEY (id)
);

COMMENT ON TABLE es_space_member IS 'EasyShare 共享空间成员授权';
COMMENT ON COLUMN es_space_member.space_id IS '空间ID';
COMMENT ON COLUMN es_space_member.user_id IS '用户ID';
COMMENT ON COLUMN es_space_member.permission IS '权限：read 只读、write 读写';

CREATE UNIQUE INDEX IF NOT EXISTS uk_es_space_member
    ON es_space_member (space_id, user_id);

-- 共享空间是全局单例，用固定 ID 1 预置，避免「管理员没建就没有共享盘」。
-- 初始配额 0（待开空间），由管理员在客户端管理页设定。
INSERT INTO es_space (space_id, space_type, owner_id, space_name, quota_bytes, status, create_time, remark)
VALUES (1, 'shared', 0, '共享空间', 0, '0', NOW(), '全员可见的公共空间，读写权限逐账号授予')
ON CONFLICT (space_id) DO NOTHING;
