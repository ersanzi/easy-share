package org.dromara.easyshare.drive.domain;

/**
 * 商城可见的插件清单项：商城列表与单插件 latest 共用。
 * <p>
 * 下载 URL 不在这里——预签名 GET 有效期短（默认 10 分钟），客户端安装前现取
 * /assets/{assetId}/url。asset 为 null 表示该插件从未发布过（理论上商城列表不会出现）。
 *
 * @param id          插件 ID（即客户端 manifest.id）
 * @param name        插件名
 * @param description 插件说明
 * @param icon        emoji 或包内图标路径
 * @param author      作者
 * @param version     最新已发布版本
 * @param notes       该版本更新说明
 * @param publishedAt 发布时间 yyyy-MM-dd HH:mm:ss
 * @param asset       可下载资产（zip）
 * @author EasyShare
 */
public record PluginManifestVo(
    String id,
    String name,
    String description,
    String icon,
    String author,
    String version,
    String notes,
    String publishedAt,
    AssetVo asset
) {

    /**
     * 插件包资产。id 为字符串——雪花 ID 超出 JS 安全整数范围（同 SpaceVo）。
     *
     * @param id        资产 ID（下载前用它换取预签名 URL）
     * @param filename  zip 文件名
     * @param sizeBytes 字节数
     * @param sha256    下载后校验
     */
    public record AssetVo(
        String id,
        String filename,
        Long sizeBytes,
        String sha256
    ) {
    }
}
