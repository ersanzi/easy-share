-- PostgreSQL：在 file/recycle/directory 迁移之后执行；只增字段/索引/删除批次账，不改旧正文。
-- 新字段全部可空：旧 recycled 行不能被宣称为已验证独立快照。整脚本可重复执行。
BEGIN;
ALTER TABLE es_file ADD COLUMN IF NOT EXISTS recycle_batch_id VARCHAR(64);
ALTER TABLE es_file ADD COLUMN IF NOT EXISTS recycle_storage_path VARCHAR(256);
ALTER TABLE es_file ADD COLUMN IF NOT EXISTS recycle_root BOOLEAN;
ALTER TABLE es_file ADD COLUMN IF NOT EXISTS recycle_operation_id VARCHAR(64);
ALTER TABLE es_file ADD COLUMN IF NOT EXISTS recycle_snapshot_version BIGINT;
ALTER TABLE es_file ADD COLUMN IF NOT EXISTS recycle_snapshot_size BIGINT;
ALTER TABLE es_file ADD COLUMN IF NOT EXISTS recycle_snapshot_etag VARCHAR(128);
ALTER TABLE es_file ADD COLUMN IF NOT EXISTS recycle_snapshot_sha256 VARCHAR(64);
COMMENT ON COLUMN es_file.recycle_snapshot_sha256 IS '整对象流式SHA-256，64位小写十六进制；NULL=旧数据未核验，不得据此认定ready';
ALTER TABLE es_file ADD COLUMN IF NOT EXISTS recycle_snapshot_state VARCHAR(16);
COMMENT ON COLUMN es_file.recycle_storage_path IS '空间内独立快照相对键：.recycle/{batchId}/{fileId}/v{version}；NULL=旧数据无保证';
COMMENT ON COLUMN es_file.recycle_snapshot_state IS 'NULL=旧数据未核验；preparing=待复制核验；ready=核验成功，不代替对象HEAD';
COMMENT ON COLUMN es_file.recycle_root IS '本次删除根项；同批子项共享recycle_batch_id，directory沿用既有类型';

-- 专用删除操作账：一次根删除/整批还原的幂等结果，不是泛用任务表。
CREATE TABLE IF NOT EXISTS es_recycle_batch (
    batch_id VARCHAR(64) PRIMARY KEY,
    space_type VARCHAR(16) NOT NULL,
    owner_id BIGINT NOT NULL,
    operator_id BIGINT NOT NULL,
    operation_id VARCHAR(64) NOT NULL,
    root_file_id BIGINT,
    root_path VARCHAR(768) NOT NULL,
    directory BOOLEAN NOT NULL DEFAULT FALSE,
    expected_version BIGINT,
    status VARCHAR(16) NOT NULL DEFAULT 'preparing',
    member_count INTEGER NOT NULL DEFAULT 0,
    members_json TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    restored_at TIMESTAMP,
    CONSTRAINT ck_es_recycle_batch_status CHECK
        (status IN ('preparing','recycled','restoring','restored','purging','purged')),
    CONSTRAINT ck_es_recycle_batch_identity CHECK
        (batch_id ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$'
         AND operation_id ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$'
         AND space_type IN ('personal','shared')
         AND ((space_type='personal' AND owner_id>0) OR (space_type='shared' AND owner_id=0))
         AND operator_id>0 AND (root_file_id IS NULL OR root_file_id>0)
         AND (expected_version IS NULL OR expected_version>=0) AND member_count>=0)
);
ALTER TABLE es_recycle_batch ADD COLUMN IF NOT EXISTS members_json TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS uk_es_recycle_batch_operation
    ON es_recycle_batch (space_type, owner_id, operator_id, operation_id);
CREATE INDEX IF NOT EXISTS idx_es_recycle_batch_root
    ON es_recycle_batch (space_type, owner_id, root_file_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_es_file_recycle_batch
    ON es_file (space_type, owner_id, recycle_batch_id) WHERE recycle_batch_id IS NOT NULL;
COMMENT ON TABLE es_recycle_batch IS '回收站专用删除批次/幂等操作账；重试须核对根身份、版本及操作者，禁止按路径清全批历史';
COMMENT ON COLUMN es_recycle_batch.status IS 'preparing→recycled→restoring→restored；purging→purged；由配套服务核验后推进';
COMMIT;
