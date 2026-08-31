-- EasyShare 插件商城发布模型（PostgreSQL）。
--
-- 放在 EasyShare 仓内而非 platform/script/sql/：platform/ 是上游 RuoYi 的 clone 且被
-- .gitignore 忽略（同 easyshare-app-release.sql）。
--
-- 用法：
--   docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < deploy/ruoyi-db/easyshare-plugin.sql
--
-- 设计要点（平移 es_app_release 模式）：
--   1. es_plugin 一行一个插件（plugin_id 为客户端 manifest 的 id，字符串主键稳定不变）；
--      版本挂 es_plugin_release（plugin_id+version 唯一，重传=覆盖发布），资产挂
--      es_plugin_release_asset（一个版本一个 zip 包）。
--   2. 上传两段式：prepareUpload upsert 插件登记 + 建 pending 资产并签发预签名 PUT →
--      发布方直传 RustFS（plugins/{pluginId}/{version}/ 前缀）→ publish 校验对象存在且
--      大小一致才置已发布。
--   3. 下架 = 删除版本（DELETE /easyshare/plugins/admin/releases/{id}），对象一并删除，
--      客户端商城随即查不到该版本；已安装客户端不受影响（本地自足）。
--   4. status：0 待上传/未验证、1 已发布。
--   5. 插件无 platform 维度——Web 插件包（HTML/JS/CSS）天然跨平台。

CREATE TABLE IF NOT EXISTS es_plugin
(
    plugin_id   VARCHAR(32)   NOT NULL,
    name        VARCHAR(64)   NOT NULL,
    description VARCHAR(500),
    icon        VARCHAR(200),
    author      VARCHAR(64),
    tenant_id   VARCHAR(20)   DEFAULT '000000',
    create_dept BIGINT,
    create_by   BIGINT,
    create_time TIMESTAMP,
    update_by   BIGINT,
    update_time TIMESTAMP,
    remark      VARCHAR(500),
    CONSTRAINT pk_es_plugin PRIMARY KEY (plugin_id)
);

COMMENT ON TABLE es_plugin IS 'EasyShare 插件登记（商城条目）';
COMMENT ON COLUMN es_plugin.plugin_id IS '插件 ID，与客户端 manifest.id 一致（小写字母开头，字母/数字/连字符）';
COMMENT ON COLUMN es_plugin.name IS '插件显示名';
COMMENT ON COLUMN es_plugin.description IS '插件说明';
COMMENT ON COLUMN es_plugin.icon IS 'emoji 或包内图标文件相对路径';

CREATE TABLE IF NOT EXISTS es_plugin_release
(
    release_id  BIGINT        NOT NULL,
    plugin_id   VARCHAR(32)   NOT NULL,
    version     VARCHAR(32)   NOT NULL,
    notes       VARCHAR(2000),
    tenant_id   VARCHAR(20)   DEFAULT '000000',
    create_dept BIGINT,
    create_by   BIGINT,
    create_time TIMESTAMP,
    update_by   BIGINT,
    update_time TIMESTAMP,
    remark      VARCHAR(500),
    CONSTRAINT pk_es_plugin_release PRIMARY KEY (release_id)
);

COMMENT ON TABLE es_plugin_release IS 'EasyShare 插件发布版本';
COMMENT ON COLUMN es_plugin_release.plugin_id IS '所属插件 ID';
COMMENT ON COLUMN es_plugin_release.version IS '版本号，如 1.0.0';
COMMENT ON COLUMN es_plugin_release.notes IS '更新说明，多行纯文本';

-- 同插件同版本唯一：重传同版本是「覆盖发布」。
CREATE UNIQUE INDEX IF NOT EXISTS uk_es_plugin_release
    ON es_plugin_release (plugin_id, version);

CREATE TABLE IF NOT EXISTS es_plugin_release_asset
(
    id          BIGINT        NOT NULL,
    release_id  BIGINT        NOT NULL,
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
    CONSTRAINT pk_es_plugin_release_asset PRIMARY KEY (id)
);

COMMENT ON TABLE es_plugin_release_asset IS 'EasyShare 插件发布资产（zip 包）';
COMMENT ON COLUMN es_plugin_release_asset.release_id IS '所属版本 ID';
COMMENT ON COLUMN es_plugin_release_asset.filename IS 'zip 文件名，单段无路径分隔';
COMMENT ON COLUMN es_plugin_release_asset.size_bytes IS '字节数，publish 时与对象实际大小比对';
COMMENT ON COLUMN es_plugin_release_asset.sha256 IS '文件 SHA256（发布方本地计算），客户端下载后校验';
COMMENT ON COLUMN es_plugin_release_asset.object_key IS 'RustFS 对象键 plugins/{pluginId}/{version}/{filename}';
COMMENT ON COLUMN es_plugin_release_asset.status IS '状态：0 待上传、1 已发布';

-- 一个版本一个 zip 资产：release_id 唯一。
CREATE UNIQUE INDEX IF NOT EXISTS uk_es_plugin_release_asset
    ON es_plugin_release_asset (release_id);
