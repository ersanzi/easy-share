package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;
import lombok.EqualsAndHashCode;
import org.dromara.common.mybatis.core.domain.BaseEntity;

/**
 * 插件发布资产实体：一个版本的一个 zip 包。
 * <p>
 * 上传两段式（同 es_app_release_asset）：prepareUpload 建行（pending）+ 预签名 PUT 直传
 * RustFS；publish 校验对象存在且大小一致后才置 published。
 *
 * @author EasyShare
 */
@Data
@EqualsAndHashCode(callSuper = true)
@TableName("es_plugin_release_asset")
public class EsPluginReleaseAsset extends BaseEntity {

    /** 待上传/未验证 */
    public static final String STATUS_PENDING = "0";

    /** 已发布，可进入商城清单 */
    public static final String STATUS_PUBLISHED = "1";

    @TableId(value = "id")
    private Long id;

    /** 所属版本 ID */
    private Long releaseId;

    /** zip 文件名，单段无路径分隔 */
    private String filename;

    /** 字节数，publish 时与对象实际大小比对 */
    private Long sizeBytes;

    /** 文件 SHA256（发布方本地计算），客户端下载后校验 */
    private String sha256;

    /** RustFS 对象键 plugins/{pluginId}/{version}/{filename} */
    private String objectKey;

    /** 0 待上传、1 已发布 */
    private String status;
}
