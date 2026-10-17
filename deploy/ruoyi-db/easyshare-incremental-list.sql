-- EasyShare 增量列举（切片 2）——空间版本 + 变更日志。
--
-- 用法：
--   Get-Content deploy/ruoyi-db/easyshare-incremental-list.sql | docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue
--
-- 设计要点：
--   1. es_space.space_version 是空间级单调递增版本号：任何文件生命周期变更
--      （新建/覆盖/软删/还原/彻底删除/移动）都先取一个新版本号，再写入变更日志。
--      客户端记住上次看到的版本，下一次带 sinceVersion 只拉之后的变更。
--   2. 变更日志不替代 es_file 状态机，只是给客户端看的差异流。
--   3. 事件粒度按路径（客户端视图是路径视角，与存储实现解耦）。
--   4. 幂等：ADD COLUMN IF NOT EXISTS / CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS。
--   5. 变更日志是缓存而非真相源，可按版本裁剪；日志不足时客户端退全量重建。

ALTER TABLE es_space ADD COLUMN IF NOT EXISTS space_version BIGINT NOT NULL DEFAULT 0;
COMMENT ON COLUMN es_space.space_version IS '空间级单调递增版本；文件生命周期每次变更 +1';

CREATE TABLE IF NOT EXISTS es_file_change_log
(
    log_id        BIGINT       NOT NULL,
    space_type    VARCHAR(16)  NOT NULL,
    owner_id      BIGINT       NOT NULL,
    space_version BIGINT       NOT NULL,
    event_type    VARCHAR(16)  NOT NULL,
    file_id       BIGINT,
    file_path     VARCHAR(768),
    old_path      VARCHAR(768),
    tenant_id     VARCHAR(20)  DEFAULT '000000',
    create_dept   BIGINT,
    create_by     BIGINT,
    create_time   TIMESTAMP,
    update_by     BIGINT,
    update_time   TIMESTAMP,
    CONSTRAINT pk_es_file_change_log PRIMARY KEY (log_id),
    CONSTRAINT uk_es_file_change_version UNIQUE (space_type, owner_id, space_version)
);

CREATE INDEX IF NOT EXISTS idx_es_file_change_since
    ON es_file_change_log (space_type, owner_id, space_version);

COMMENT ON TABLE es_file_change_log IS 'EasyShare 增量列举变更日志（客户端差异流，非真相源）';
COMMENT ON COLUMN es_file_change_log.space_version IS '该变更对应的空间版本（全局单调）';
COMMENT ON COLUMN es_file_change_log.event_type IS 'created / updated / deleted / restored / moved / purged';
COMMENT ON COLUMN es_file_change_log.file_path IS '事件后的当前路径；purged 时为空';
COMMENT ON COLUMN es_file_change_log.old_path IS 'moved 事件的源路径；其他事件为空';
