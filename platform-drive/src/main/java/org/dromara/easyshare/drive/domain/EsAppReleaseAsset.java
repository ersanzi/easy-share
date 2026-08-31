package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;
import lombok.EqualsAndHashCode;
import org.dromara.common.mybatis.core.domain.BaseEntity;

/**
 * 发布资产实体：一个版本下的一个可下载文件（安装包/DMG/zip）。
 * <p>
 * 上传两段式：prepareUpload 建行（status=pending）+ 签发预签名 PUT，发布方直传 RustFS；
 * publish 校验对象存在且大小一致后才置 published——失败/中断的上传进不了 latest 清单。
 *
 * @author EasyShare
 */
@Data
@EqualsAndHashCode(callSuper = true)
@TableName("es_app_release_asset")
public class EsAppReleaseAsset extends BaseEntity {

    /** 平台：Windows 客户端 */
    public static final String PLATFORM_WINDOWS = "windows";

    /** 平台：macOS 客户端 */
    public static final String PLATFORM_MACOS = "macos";

    /** 资产类型：NSIS 安装包 */
    public static final String KIND_INSTALLER = "installer";

    /** 资产类型：macOS DMG 镜像 */
    public static final String KIND_DMG = "dmg";

    /** 资产类型：macOS 应用 zip */
    public static final String KIND_ZIP = "zip";

    /** 待上传/未验证 */
    public static final String STATUS_PENDING = "0";

    /** 已发布，可进入 latest 清单 */
    public static final String STATUS_PUBLISHED = "1";

    @TableId(value = "id")
    private Long id;

    private Long releaseId;

    /** windows / macos */
    private String platform;

    /** installer / dmg / zip */
    private String kind;

    /** 原始文件名，单段无路径分隔 */
    private String filename;

    /** 字节数，publish 时与对象实际大小比对 */
    private Long sizeBytes;

    /** 文件 SHA256（发布方本地计算），客户端下载后校验 */
    private String sha256;

    /** RustFS 对象键 releases/{version}/{filename} */
    private String objectKey;

    /** 0 待上传、1 已发布 */
    private String status;
}
