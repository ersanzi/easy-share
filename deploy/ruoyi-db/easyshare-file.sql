-- EasyShare 文件目录层（PostgreSQL）——Cloudreve 对标 P0：稳定 fileId。
--
-- 设计要点（与 easyshare-space.sql 同口径）：
--   1. es_file 是**逻辑文件的元数据索引**：稳定 fileId + 空间内相对路径。真实内容
--      仍在 RustFS（键 = 前缀 + file_path），路径继续可用，但业务身份从路径解耦——
--      这是回收站、版本、分享重命名稳定与知识索引绑定（KI-3 关闭）的前置。
--   2. 控制面不在上传数据路径上（预签名直传），因此登记时机为：
--      a) presignPut 签发时 upsert（申报大小）；b) 列表时惰性补账（存量对象自愈，
--      不丢 fileId）。文件可能登记后未真正写入（签名过期/上传失败）——列表以
--      RustFS 为准回填，幽灵行不影响正确性，只等下次列表对账或删除时清理。
--   3. owner_id 语义：personal = 归属用户；shared = 0（共享空间全局单例，与
--      es_space 的用法对齐）。upload_by 记真实上传者：personal 与 owner 相同；
--      shared 区分上传者；惰性补账的存量行记 0（早于目录层上线，无法归因）。
--   4. 唯一键 (space_type, owner_id, file_path)：个人空间按用户隔离路径，
--      共享空间全库路径唯一。回收站/版本态字段留待后续切片（届时加列，不本切片）。

CREATE TABLE IF NOT EXISTS es_file
(
    file_id     BIGINT       NOT NULL,
    space_type  VARCHAR(16)  NOT NULL,
    owner_id    BIGINT       NOT NULL DEFAULT 0,
    upload_by   BIGINT       NOT NULL DEFAULT 0,
    file_path   VARCHAR(768) NOT NULL,
    file_name   VARCHAR(255) NOT NULL,
    file_size   BIGINT       NOT NULL DEFAULT 0,
    tenant_id   VARCHAR(20)  DEFAULT '000000',
    create_dept BIGINT,
    create_by   BIGINT,
    create_time TIMESTAMP,
    update_by   BIGINT,
    update_time TIMESTAMP,
    remark      VARCHAR(500),
    CONSTRAINT pk_es_file PRIMARY KEY (file_id)
);

COMMENT ON TABLE es_file IS 'EasyShare 文件目录层：稳定 fileId 与空间内路径的元数据索引';
COMMENT ON COLUMN es_file.file_id IS '文件ID（雪花，稳定业务身份）';
COMMENT ON COLUMN es_file.space_type IS '空间类型：personal 个人、shared 共享';
COMMENT ON COLUMN es_file.owner_id IS '空间归属：personal=用户ID、shared=0';
COMMENT ON COLUMN es_file.upload_by IS '上传者用户ID；惰性补账的存量行为 0';
COMMENT ON COLUMN es_file.file_path IS '空间内相对路径（规范化后，不含前缀）';
COMMENT ON COLUMN es_file.file_name IS '文件名（路径末段）';
COMMENT ON COLUMN es_file.file_size IS '最近一次登记的字节数（申报值或补账真实值）';

-- 空间内路径唯一：个人空间同用户不重名，共享空间全库不重名
CREATE UNIQUE INDEX IF NOT EXISTS uk_es_file_path
    ON es_file (space_type, owner_id, file_path);

-- 按空间列目录走这个索引
CREATE INDEX IF NOT EXISTS idx_es_file_space
    ON es_file (space_type, owner_id);
