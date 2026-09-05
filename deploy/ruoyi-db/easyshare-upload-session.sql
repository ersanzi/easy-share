-- EasyShare Upload Session（Multipart 断点续传，PostgreSQL）。
--
-- 设计要点：
--   1. 控制面是会话的记账方：create 时创建 S3 MultipartUpload 并落一行，
--      complete/abort 时推进状态。S3 端 uploadId 与分片 ETag 清单的真值分别在
--      S3 与客户端本地会话文件里，库里只存路由与状态所需的最小字段。
--   2. 幂等 Complete：status='1'（已完成）的会话重复 Complete 直接返回成功，
--      不再触 S3（S3 端 uploadId 已被消费，二次 Complete 必然 NoSuchUpload）。
--   3. create 对同路径的遗留 uploading 会话先 Abort 再新建，防 S3 端孤儿分片
--      占用存储与配额。
--   4. owner_id 语义与 es_file 一致：personal=用户ID、shared=0；upload_by 记
--      真实发起者，是会话归属的强校验键（共享空间他人不可续传/完成别人的会话）。

CREATE TABLE IF NOT EXISTS es_upload_session
(
    session_id    BIGINT       NOT NULL,
    space_type    VARCHAR(16)  NOT NULL,
    owner_id      BIGINT       NOT NULL DEFAULT 0,
    upload_by     BIGINT       NOT NULL,
    file_path     VARCHAR(768) NOT NULL,
    upload_id     VARCHAR(256) NOT NULL,
    part_size     BIGINT       NOT NULL,
    declared_size BIGINT       NOT NULL DEFAULT 0,
    status        CHAR(1)      NOT NULL DEFAULT '0',
    tenant_id     VARCHAR(20)  DEFAULT '000000',
    create_dept   BIGINT,
    create_by     BIGINT,
    create_time   TIMESTAMP,
    update_by     BIGINT,
    update_time   TIMESTAMP,
    remark        VARCHAR(500),
    CONSTRAINT pk_es_upload_session PRIMARY KEY (session_id)
);

COMMENT ON TABLE es_upload_session IS 'EasyShare Multipart 上传会话';
COMMENT ON COLUMN es_upload_session.session_id IS '会话ID（雪花，对客户端不透明）';
COMMENT ON COLUMN es_upload_session.upload_id IS 'S3 MultipartUpload 的 uploadId';
COMMENT ON COLUMN es_upload_session.part_size IS '分片大小（服务端定，客户端不选）';
COMMENT ON COLUMN es_upload_session.declared_size IS '申报总字节数（create 时配额判定依据）';
COMMENT ON COLUMN es_upload_session.status IS '状态：0 进行中、1 已完成、2 已放弃';

-- 按路径清理遗留会话与按会话查详情
CREATE INDEX IF NOT EXISTS idx_es_upload_session_path
    ON es_upload_session (space_type, owner_id, file_path, status);
