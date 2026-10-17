-- EasyShare 内容哈希与秒传（切片 6）——es_file 哈希列。
--
-- 用法：
--   Get-Content deploy/ruoyi-db/easyshare-content-hash.sql | docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue
--
-- 设计要点（对标钉盘取证 §9「内容哈希/秒传」：md5 + md5_reliable + 服务端 genMd5）：
--   1. content_md5 是十六进制小写 MD5：客户端上传前算好随请求上报，
--      服务端按它做秒传判定（同空间同哈希 → 不再重复占对象存储空间）。
--   2. md5_reliable 标记哈希的可靠性来源（对标钉盘 md5_reliable 语义）：
--      'client' = 客户端整文件读算（可靠）；
--      NULL     = 未上报（存量行），秒传判定不生效。
--   3. 幂等：ADD COLUMN IF NOT EXISTS / CREATE INDEX IF NOT EXISTS。
--   4. 哈希查询是点查热路径（每次上传签发都会查一次），建索引。

ALTER TABLE es_file ADD COLUMN IF NOT EXISTS content_md5    VARCHAR(32);
ALTER TABLE es_file ADD COLUMN IF NOT EXISTS md5_reliable   VARCHAR(16);

COMMENT ON COLUMN es_file.content_md5 IS '内容 MD5（十六进制小写）；秒传判定键，未上报为 NULL';
COMMENT ON COLUMN es_file.md5_reliable IS '哈希可靠性来源：client=客户端整文件读算；NULL=未上报（秒传不生效）';

-- 秒传点查：同空间内按哈希找一条在用行（active 才能作为秒传源）
CREATE INDEX IF NOT EXISTS idx_es_file_content_md5
    ON es_file (space_type, owner_id, content_md5)
    WHERE state = 'active' AND content_md5 IS NOT NULL;
