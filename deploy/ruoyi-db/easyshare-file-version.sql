-- EasyShare 文件版本历史。
--
-- 背景：同名覆盖上传目前只把 es_file.version_id +1，**旧内容被直接冲掉**——
-- 因为客户端是直传对象存储，控制面不经过数据路径，覆盖发生在两次 API 调用之间。
-- 所以保留旧版只能在 presign-put 那一刻做一次服务端复制（copyObject，不耗客户端带宽）。
--
-- 设计要点：
--   1. 版本对象放在 `{prefix}.versions/{path}/v{n}`，与正常文件**不同前缀**；
--      listAt 必须过滤 `.versions/`，否则快照会被当成文件列出来。
--   2. 版本行保存 fileSize/contentMd5 快照：查历史时不必再 HEAD 对象。
--   3. 版本号与被覆盖时的 es_file.version_id 对齐，回滚后主版本号继续 +1，
--      保证 version_id 单调（乐观并发校验依赖它）。
--   4. 同一 (file_id, version_id) 只留一条：presign 可能被重试，
--      重复快照没有意义，唯一索引让第二次写入直接失败而不是产生重复历史。
--   5. 幂等：整脚本可重复执行。
--
-- 执行：
--   docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-file-version.sql

CREATE TABLE IF NOT EXISTS es_file_version
(
    version_pk   BIGINT        NOT NULL,
    file_id      BIGINT        NOT NULL,
    version_id   BIGINT        NOT NULL,
    space_type   VARCHAR(16)   NOT NULL,
    owner_id     BIGINT        NOT NULL,
    file_path    VARCHAR(768)  NOT NULL,
    file_size    BIGINT        NOT NULL DEFAULT 0,
    content_md5  VARCHAR(64),
    storage_path VARCHAR(1024) NOT NULL,
    created_at   TIMESTAMP     NOT NULL,
    created_by   BIGINT,
    CONSTRAINT pk_es_file_version PRIMARY KEY (version_pk)
);

COMMENT ON TABLE es_file_version IS '文件历史版本：每次覆盖上传前把旧对象复制一份留档';
COMMENT ON COLUMN es_file_version.file_id IS '归属文件（es_file.file_id），路径改名后历史仍跟着文件走';
COMMENT ON COLUMN es_file_version.version_id IS '被覆盖时的版本号（与 es_file.version_id 的历史值对齐）';
COMMENT ON COLUMN es_file_version.storage_path IS '版本对象的相对路径：.versions/{path}/v{n}';
COMMENT ON COLUMN es_file_version.content_md5 IS '该版本的哈希快照；老数据可能为空';
COMMENT ON COLUMN es_file_version.created_by IS '触发覆盖上传的用户';

-- 重试 presign 不产生重复历史
CREATE UNIQUE INDEX IF NOT EXISTS uk_es_file_version
    ON es_file_version (file_id, version_id);

-- 「看某个文件的历史」按版本号倒序
CREATE INDEX IF NOT EXISTS idx_es_file_version_file
    ON es_file_version (file_id, version_id DESC);
