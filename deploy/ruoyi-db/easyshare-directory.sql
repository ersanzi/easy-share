-- PostgreSQL：在已有 es_file / recycle / incremental-list 迁移之后执行。
-- directory=true 的存在性由 es_file 本身保证，不创建 S3 marker。
BEGIN;
ALTER TABLE es_file ADD COLUMN IF NOT EXISTS directory BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE es_file_change_log ADD COLUMN IF NOT EXISTS directory BOOLEAN NOT NULL DEFAULT FALSE;
COMMENT ON COLUMN es_file.directory IS '持久化目录；false 为普通文件；与文件共用 active 路径唯一索引';

-- 文件与目录使用同一个唯一路径索引。空间级事务锁保护祖先/子项约束，
-- 包含旧客户端上传和列表惰性补账，不能只在新目录接口加锁。
CREATE OR REPLACE FUNCTION es_file_directory_guard() RETURNS TRIGGER AS $$
DECLARE
    st VARCHAR(16);
    oid BIGINT;
BEGIN
    IF TG_OP = 'DELETE' THEN st := OLD.space_type; oid := OLD.owner_id;
    ELSE st := NEW.space_type; oid := NEW.owner_id; END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('es_file:' || st || ':' || oid::text, 0));
    IF TG_OP <> 'INSERT' AND OLD.directory AND OLD.state = 'active' THEN
        IF TG_OP = 'DELETE' THEN
            IF EXISTS (SELECT 1 FROM es_file f WHERE f.space_type=st AND f.owner_id=oid
                AND f.state='active' AND left(f.file_path,length(OLD.file_path)+1)=OLD.file_path || '/') THEN
                RAISE EXCEPTION '目录非空，请先处理子项';
            END IF;
        ELSIF NEW.state <> 'active' OR NEW.file_path <> OLD.file_path THEN
            IF EXISTS (SELECT 1 FROM es_file f WHERE f.space_type=st AND f.owner_id=oid
                AND f.state='active' AND left(f.file_path,length(OLD.file_path)+1)=OLD.file_path || '/') THEN
                RAISE EXCEPTION '目录非空，请先处理子项';
            END IF;
        END IF;
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    IF NEW.state = 'active' THEN
        IF EXISTS (SELECT 1 FROM es_file f WHERE f.space_type=st AND f.owner_id=oid
            AND f.state='active' AND NOT f.directory AND f.file_id <> NEW.file_id
            AND left(NEW.file_path,length(f.file_path)+1)=f.file_path || '/') THEN
            RAISE EXCEPTION '父路径是文件，不能创建子项';
        END IF;
        IF NOT NEW.directory AND EXISTS (SELECT 1 FROM es_file f WHERE f.space_type=st AND f.owner_id=oid
            AND f.state='active' AND f.file_id <> NEW.file_id
            AND left(f.file_path,length(NEW.file_path)+1)=NEW.file_path || '/') THEN
            RAISE EXCEPTION '该位置已有子项，不能写入文件';
        END IF;
        IF TG_OP = 'UPDATE' AND OLD.directory <> NEW.directory THEN
            RAISE EXCEPTION '不允许将文件与目录互相覆盖';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS es_file_directory_guard_trigger ON es_file;
CREATE TRIGGER es_file_directory_guard_trigger BEFORE INSERT OR UPDATE OR DELETE ON es_file
    FOR EACH ROW EXECUTE FUNCTION es_file_directory_guard();
COMMIT;
