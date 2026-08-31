package org.dromara.easyshare.drive.domain;

import java.util.List;

/**
 * 管理端的版本视图：一个版本 + 名下全部资产（含待上传的）。
 *
 * @param releaseId   版本 ID
 * @param version     版本号
 * @param notes       更新说明
 * @param createTime  发布时间 yyyy-MM-dd HH:mm:ss
 * @param assets      资产列表
 * @author EasyShare
 */
public record AppReleaseVo(
    String releaseId,
    String version,
    String notes,
    String createTime,
    List<AppManifestVo.AssetVo> assets
) {
}
