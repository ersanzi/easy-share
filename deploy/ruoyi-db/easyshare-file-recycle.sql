-- EasyShare 回收站 + 版本（P1，Cloudreve/WPS 双对标第 1 项）——es_file 生命周期列。
--
-- 设计要点（沿用 easyshare-file.sql 的口径）：
--   1. 软删：删除不删行、不动 RustFS 对象，置 state='recycled' + deleted_at +
--      origin_path=删除时的 file_path。还原按 origin_path 复位（WPS to_delete_files
--      的"路径快照"同构——见 wps-cloud-drive-benchmark.md §4.2）。
--   2. 唯一索引从全量降为部分索引 WHERE state='active'：回收站里同路径的文件
--      不再卡住同路径新上传；active 层仍保证空间内路径唯一。
--   3. version_id 单调递增：同名覆盖上传（presignPut 登记）时旧行 +1。本片只保
--      证递增与留字段，版本历史/回滚为后续切片。
--   4. 幂等：整脚本可重复执行——加列用 IF NOT EXISTS，索引重建用 DO 块判存在性。

-- ── 生命周期列 ─────────────────────────────────────────────
ALTER TABLE es_file ADD COLUMN IF NOT EXISTS state            VARCHAR(16) NOT NULL DEFAULT 'active';
ALTER TABLE es_file ADD COLUMN IF NOT EXISTS deleted_at       TIMESTAMP;
ALTER TABLE es_file ADD COLUMN IF NOT EXISTS origin_path      VARCHAR(768);
ALTER TABLE es_file ADD COLUMN IF NOT EXISTS origin_parent_id BIGINT;
ALTER TABLE es_file ADD COLUMN IF NOT EXISTS version_id       BIGINT      NOT NULL DEFAULT 0;

COMMENT ON COLUMN es_file.state IS '生命周期：active 在用、recycled 回收站、purged 已彻底清除（逻辑态预留）';
COMMENT ON COLUMN es_file.deleted_at IS '进入回收站的时间；active 行为 NULL';
COMMENT ON COLUMN es_file.origin_path IS '删除前的 file_path 快照（还原落点）';
COMMENT ON COLUMN es_file.origin_parent_id IS '删除前的父目录 fileId（目录建模后启用，本片恒为 NULL）';
COMMENT ON COLUMN es_file.version_id IS '同路径覆盖上传递增的版本号（0=首次登记）';

-- ── 唯一索引改为部分索引（只对 active 行唯一）────────────────
-- PG 的部分索引必须新建：先删旧全量唯一索引，再建同名的部分唯一索引。
-- DO 块按 pg_indexes 判存在性，重复执行安全。
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE schemaname = current_schema()
          AND indexname = 'uk_es_file_path'
          AND indexdef NOT ILIKE '%WHERE%'
    ) THEN
        DROP INDEX uk_es_file_path;
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS uk_es_file_path
    ON es_file (space_type, owner_id, file_path)
    WHERE state = 'active';

-- 回收站列表按空间 + 删除时间倒序走这个索引
CREATE INDEX IF NOT EXISTS idx_es_file_recycled
    ON es_file (space_type, owner_id, deleted_at DESC)
    WHERE state = 'recycled';
