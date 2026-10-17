-- EasyShare 星标与最近访问（对标 WPS 的星标标记 + 钉钉的「最近」入口）。
--
-- 设计要点：
--   1. 标记挂在「用户 × 路径」上，不挂 fileId。同名文件被删后重建会拿到新 fileId，
--      但用户说的「我星标的是这个位置的文件」在路径不变时应继续成立；
--      WPS 的 colorMark 是挂在文件行上的单值标记，我们这里是个人视图，按人+路径更稳。
--   2. kind 用同一张表两种用途（star / recent）。两者结构完全一致，
--      差异只在写入时机与保留策略——拆两张表会让「查某人所有标记」变成 union。
--   3. 最近访问由清理任务裁剪条数，星标不裁剪。
--   4. 幂等：整脚本可重复执行。
--
-- 执行：
--   docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-file-mark.sql

CREATE TABLE IF NOT EXISTS es_file_mark
(
    mark_id    BIGINT       NOT NULL,
    user_id    BIGINT       NOT NULL,
    space_type VARCHAR(16)  NOT NULL,
    file_path  VARCHAR(768) NOT NULL,
    kind       VARCHAR(8)   NOT NULL,
    marked_at  TIMESTAMP    NOT NULL,
    CONSTRAINT pk_es_file_mark PRIMARY KEY (mark_id)
);

COMMENT ON TABLE es_file_mark IS '用户级文件标记：星标 / 最近访问';
COMMENT ON COLUMN es_file_mark.user_id IS '标记归属用户';
COMMENT ON COLUMN es_file_mark.space_type IS '空间类型：personal 个人、shared 共享';
COMMENT ON COLUMN es_file_mark.file_path IS '空间内相对路径（与 es_file.file_path 同口径）';
COMMENT ON COLUMN es_file_mark.kind IS 'star 星标、recent 最近访问';
COMMENT ON COLUMN es_file_mark.marked_at IS '标记时间：星标为点击时刻，最近为最后一次访问';

-- 同一用户对同一路径的同类标记只有一条：重复星标只是刷新时间，不是新增
CREATE UNIQUE INDEX IF NOT EXISTS uk_es_file_mark
    ON es_file_mark (user_id, space_type, file_path, kind);

-- 「最近」按时间倒序取前 N 条，这条索引直接支撑该查询
CREATE INDEX IF NOT EXISTS idx_es_file_mark_recent
    ON es_file_mark (user_id, kind, marked_at DESC);
