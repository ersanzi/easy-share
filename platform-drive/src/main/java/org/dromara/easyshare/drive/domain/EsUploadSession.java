package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;
import lombok.EqualsAndHashCode;
import org.dromara.common.mybatis.core.domain.BaseEntity;

/**
 * Multipart 上传会话：create 落行、complete/abort 推进状态（Upload Session 切片）。
 * <p>
 * 会话是 S3 端 uploadId 与登录用户之间的记账桥——客户端拿到的 sessionId 不透明，
 * 归属以 {@code upload_by} 强校验，他人不可续传/完成/放弃别人的会话。
 *
 * @author EasyShare
 */
@Data
@EqualsAndHashCode(callSuper = true)
@TableName("es_upload_session")
public class EsUploadSession extends BaseEntity {

    /** 个人空间 */
    public static final String TYPE_PERSONAL = EsSpace.TYPE_PERSONAL;

    /** 共享空间 */
    public static final String TYPE_SHARED = EsSpace.TYPE_SHARED;

    /** 进行中 */
    public static final String STATUS_UPLOADING = "0";

    /** 已完成（幂等 Complete 命中此态直接返回成功） */
    public static final String STATUS_COMPLETED = "1";

    /** 已放弃 */
    public static final String STATUS_ABORTED = "2";

    @TableId(value = "session_id")
    private Long sessionId;

    /** personal / shared */
    private String spaceType;

    /** 空间归属：personal=用户ID、shared=0（与 es_file 口径一致） */
    private Long ownerId;

    /** 会话发起者（归属强校验键） */
    private Long uploadBy;

    /** 空间内相对路径（规范化后） */
    private String filePath;

    /** S3 MultipartUpload 的 uploadId */
    private String uploadId;

    /** 分片大小（服务端定，客户端不选） */
    private Long partSize;

    /** 申报总字节数（create 时配额判定依据） */
    private Long declaredSize;

    /** 0 进行中、1 已完成、2 已放弃 */
    private String status;
}
