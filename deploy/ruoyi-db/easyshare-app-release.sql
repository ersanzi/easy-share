-- EasyShare 客户端在线升级发布模型（PostgreSQL）。
--
-- 放在 EasyShare 仓内而非 platform/script/sql/：platform/ 是上游 RuoYi 的 clone 且被
-- .gitignore 忽略，产品自己的 DDL 放进去会脱离版本管理（同 easyshare-space.sql）。
--
-- 用法：
--   docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-app-release.sql
--
-- 设计要点：
--   1. 一行一个版本（es_app_release，version 唯一），可下载资产（安装包/DMG/zip）挂在
--      版本下（es_app_release_asset），同版本同平台同类型唯一——重传即覆盖。
--   2. 上传两段式（与云盘上传同哲学，控制面不在数据路径上）：prepareUpload 建 pending
--      资产并签发预签名 PUT → 发布方直传 RustFS → publish 时校验对象存在且大小一致才置
--      已发布。失败/中断的上传永远不会进入 latest 清单。
--   3. 回滚 = 删除版本记录（DELETE /easyshare/app/admin/releases/{id}），对象一并删除，
--      客户端随即查不到该版本。
--   4. status：0 待上传/未验证、1 已发布。

CREATE TABLE IF NOT EXISTS es_app_release
(
    release_id  BIGINT        NOT NULL,
    version     VARCHAR(32)   NOT NULL,
    notes       VARCHAR(2000),
    tenant_id   VARCHAR(20)   DEFAULT '000000',
    create_dept BIGINT,
    create_by   BIGINT,
    create_time TIMESTAMP,
    update_by   BIGINT,
    update_time TIMESTAMP,
    remark      VARCHAR(500),
    CONSTRAINT pk_es_app_release PRIMARY KEY (release_id)
);

COMMENT ON TABLE es_app_release IS 'EasyShare 客户端发布版本';
COMMENT ON COLUMN es_app_release.version IS '版本号，如 0.1.0 / 0.1.0-preview.1';
COMMENT ON COLUMN es_app_release.notes IS '更新说明，多行纯文本';

-- 版本号唯一：重传同版本是「覆盖发布」，不产生第二行，latest 排序保持首次发布时间。
CREATE UNIQUE INDEX IF NOT EXISTS uk_es_app_release_version
    ON es_app_release (version);

CREATE TABLE IF NOT EXISTS es_app_release_asset
(
    id          BIGINT        NOT NULL,
    release_id  BIGINT        NOT NULL,
    platform    VARCHAR(16)   NOT NULL,
    kind        VARCHAR(16)   NOT NULL,
    filename    VARCHAR(128)  NOT NULL,
    size_bytes  BIGINT        NOT NULL,
    sha256      CHAR(64)      NOT NULL,
    object_key  VARCHAR(256)  NOT NULL,
    status      CHAR(1)       NOT NULL DEFAULT '0',
    tenant_id   VARCHAR(20)   DEFAULT '000000',
    create_dept BIGINT,
    create_by   BIGINT,
    create_time TIMESTAMP,
    update_by   BIGINT,
    update_time TIMESTAMP,
    remark      VARCHAR(500),
    CONSTRAINT pk_es_app_release_asset PRIMARY KEY (id)
);

COMMENT ON TABLE es_app_release_asset IS 'EasyShare 发布资产（安装包）';
COMMENT ON COLUMN es_app_release_asset.release_id IS '所属版本 ID';
COMMENT ON COLUMN es_app_release_asset.platform IS '平台：windows / macos';
COMMENT ON COLUMN es_app_release_asset.kind IS '资产类型：installer（NSIS）/ dmg / zip';
COMMENT ON COLUMN es_app_release_asset.filename IS '原始文件名，单段无路径分隔';
COMMENT ON COLUMN es_app_release_asset.size_bytes IS '字节数，publish 时与对象实际大小比对';
COMMENT ON COLUMN es_app_release_asset.sha256 IS '文件 SHA256（发布方本地计算），客户端下载后校验';
COMMENT ON COLUMN es_app_release_asset.object_key IS 'RustFS 对象键 releases/{version}/{filename}';
COMMENT ON COLUMN es_app_release_asset.status IS '状态：0 待上传、1 已发布';

CREATE UNIQUE INDEX IF NOT EXISTS uk_es_app_release_asset
    ON es_app_release_asset (release_id, platform, kind);

CREATE INDEX IF NOT EXISTS idx_es_app_release_asset_platform
    ON es_app_release_asset (platform, status);
