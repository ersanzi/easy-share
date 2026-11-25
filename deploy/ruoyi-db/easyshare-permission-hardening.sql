-- 权限写保护与事务审计，PostgreSQL；在部署新控制面之前执行，可重复运行。
-- 缺失该迁移时权限变更失败关闭；读取既有权限不受影响。
CREATE TABLE IF NOT EXISTS es_permission_write_guard (
    guard_id INTEGER PRIMARY KEY CHECK (guard_id = 1)
);
INSERT INTO es_permission_write_guard(guard_id) VALUES (1) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS es_permission_audit (
    audit_id BIGSERIAL PRIMARY KEY,
    actor_id BIGINT NOT NULL,
    action VARCHAR(64) NOT NULL,
    target_id VARCHAR(128) NOT NULL,
    before_state TEXT NOT NULL,
    after_state TEXT NOT NULL,
    create_time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_es_permission_audit_target_time
    ON es_permission_audit(target_id, create_time);
COMMENT ON TABLE es_permission_write_guard IS 'EasyShare 低频授权变更的事务互斥行';
COMMENT ON TABLE es_permission_audit IS 'EasyShare 权限变更审计，与业务授权同事务提交';
