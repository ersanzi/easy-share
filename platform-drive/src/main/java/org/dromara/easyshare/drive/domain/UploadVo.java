package org.dromara.easyshare.drive.domain;

/**
 * 上传准备结果：资产 ID + 预签名 PUT URL。
 * <p>
 * 发布方拿 URL 直传 RustFS 后，需再调 publish 完成发布（控制面届时校验对象大小）。
 *
 * @param assetId   资产 ID（publish 用）
 * @param uploadUrl 预签名 PUT URL（有效期见 easyshare.drive.put-expiry，默认 15 分钟）
 * @author EasyShare
 */
public record UploadVo(String assetId, String uploadUrl) {
}
