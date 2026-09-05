package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;
import lombok.EqualsAndHashCode;
import org.dromara.common.mybatis.core.domain.BaseEntity;

/**
 * 文件目录层实体：稳定 fileId 与空间内相对路径的绑定（Cloudreve 对标 P0）。
 * <p>
 * 刻意**不作为文件的存在性真相源**：真实内容在 RustFS，控制面不在上传数据路径上，
 * 登记可能先于写入、也可能永远等不到写入（签名过期）。列表以存储列举为准，
 * 本表只补 fileId 与归属元数据——见 {@link org.dromara.easyshare.drive.service.DriveFileService}。
 *
 * @author EasyShare
 */
@Data
@EqualsAndHashCode(callSuper = true)
@TableName("es_file")
public class EsFile extends BaseEntity {

    /** 个人空间 */
    public static final String TYPE_PERSONAL = EsSpace.TYPE_PERSONAL;

    /** 共享空间 */
    public static final String TYPE_SHARED = EsSpace.TYPE_SHARED;

    /** 共享空间行的 owner_id 固定为 0（与 es_space 的共享单例用法对齐） */
    public static final long SHARED_OWNER = 0L;

    /** 惰性补账的存量行无法归因上传者，记 0 */
    public static final long UPLOAD_BY_UNKNOWN = 0L;

    @TableId(value = "file_id")
    private Long fileId;

    /** personal / shared */
    private String spaceType;

    /** 空间归属：personal=用户ID、shared=0 */
    private Long ownerId;

    /** 真实上传者用户ID；存量补账行为 0 */
    private Long uploadBy;

    /** 空间内相对路径（规范化后，不含用户前缀） */
    private String filePath;

    /** 文件名（路径末段，列表展示用，免得每处都切一遍） */
    private String fileName;

    /** 最近一次登记的字节数 */
    private Long fileSize;
}
