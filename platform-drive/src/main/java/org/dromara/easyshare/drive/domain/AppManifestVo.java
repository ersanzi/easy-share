package org.dromara.easyshare.drive.domain;

import java.util.List;

/**
 * 客户端升级清单：latest 接口对某平台的返回体。
 * <p>
 * 下载 URL 不在清单里——预签名 GET 有效期短（默认 10 分钟），客户端每次下载前
 * 调 /assets/{id}/url 现取，避免拿到过期的长缓存 URL。
 *
 * @param version     版本号
 * @param notes       更新说明
 * @param publishedAt 发布时间 yyyy-MM-dd HH:mm:ss
 * @param assets      该平台已发布的资产（一般 1 个）
 * @author EasyShare
 */
public record AppManifestVo(
    String version,
    String notes,
    String publishedAt,
    List<AssetVo> assets
) {

    /**
     * 单个可下载资产。id 为字符串——雪花 ID 超出 JS 安全整数范围（同 SpaceVo）。
     *
     * @param id        资产 ID（下载前用它换取预签名 URL）
     * @param platform  windows / macos
     * @param kind      installer / dmg / zip
     * @param filename  原始文件名
     * @param sizeBytes 字节数（进度条总量）
     * @param sha256    下载后校验
     * @param status    0 待上传、1 已发布
     */
    public record AssetVo(
        String id,
        String platform,
        String kind,
        String filename,
        Long sizeBytes,
        String sha256,
        String status
    ) {
    }
}
