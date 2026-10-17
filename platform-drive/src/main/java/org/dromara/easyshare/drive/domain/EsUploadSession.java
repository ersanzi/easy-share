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

    /**
     * 创建会话时调用方声明的「我看到的是哪一版」（路线图切片 1 的 CAS）。
     *
     * <p>为什么**寄存在会话上**而不是 create 时立即校验：Multipart 的真实写入发生在
     * complete（大文件可能几分钟），create 时校验只能证明「开始时版本一致」，
     * 对「写入时是否已被别人改动」毫无保证。校验点必须与写入点重合。
     *
     * <p>null = 旧调用方，不做校验（向后兼容）。
     */
    private Long expectedVersion;

    /**
     * 客户端整文件读算的内容 MD5（切片 6 秒传判定）。
     *
     * <p>大文件走分片路径，字节在 complete 才聚合——秒传判定同样只能在
     * complete 做（聚合前对象还不存在）。null = 客户端未上报，不判秒传。
     */
    private String contentMd5;
}
