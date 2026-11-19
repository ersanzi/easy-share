package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;
import lombok.EqualsAndHashCode;
import org.dromara.common.mybatis.core.domain.BaseEntity;

import java.time.LocalDateTime;

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

    /** 生命周期：在用 */
    public static final String STATE_ACTIVE = "active";

    /** 生命周期：回收站（软删，对象仍保留在 RustFS） */
    public static final String STATE_RECYCLED = "recycled";

    /** 生命周期：已彻底清除（逻辑态预留；彻底删除目前直接删行） */
    public static final String STATE_PURGED = "purged";

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

    /** 目录是数据库持久化实体，不对应对象存储 marker；历史行默认为文件。 */
    private Boolean directory;

    // 非 JavaBean helper：Boolean getDirectory() 是唯一属性 getter，避免 MyBatis 与 primitive is getter 冲突。
    public boolean directoryEntry() {
        return Boolean.TRUE.equals(directory);
    }

    /** 最近一次登记的字节数 */
    private Long fileSize;

    /** 文档可见部门 ID 逗号分隔（仅共享空间语义）；空=全体可见 */
    private String visibleDepts;

    /** 生命周期状态（active/recycled/purged），默认 active */
    private String state;

    /** 进入回收站的时间；active 行为 NULL */
    private LocalDateTime deletedAt;

    /** 删除前的 file_path 快照（还原落点；回收站行的 file_path 保持原值，本列是冗余快照） */
    private String originPath;

    /** 删除前的父目录 fileId（目录建模后启用，本片恒为 NULL） */
    private Long originParentId;

    /** 同路径覆盖上传递增的版本号（0=首次登记） */
    private Long versionId;

    /** 唯一删除批次；NULL 为旧回收站数据，不代表存在独立正文快照。 */
    private String recycleBatchId;
    /** 本空间内独立快照的相对键，只能由 DriveRecycleKeys 按身份生成。 */
    private String recycleStoragePath;
    /** 是否为本次删除的根项；目录子项共享 batchId。 */
    private Boolean recycleRoot;
    /** 调用方删除操作标识；在空间/操作者内幂等，不能用路径代替。 */
    private String recycleOperationId;
    /** 被删除的确切版本，允许历史版本 0。 */
    private Long recycleSnapshotVersion;
    /** HEAD 存储字节数及 ETag 元数据；ETag 不能作为快照正文一致性证明。 */
    private Long recycleSnapshotSize;
    private String recycleSnapshotEtag;
    /** 整对象流式 SHA-256（64 位小写十六进制）；NULL 为旧数据未核验，不代表 ready。 */
    private String recycleSnapshotSha256;
    /** NULL=旧数据未核验，preparing=待复制核验，ready=已核验；不是对象存在性真值。 */
    private String recycleSnapshotState;

    /** 内容 MD5（十六进制小写）；秒传判定键，未上报为 NULL（切片 6） */
    private String contentMd5;

    /** 哈希可靠性来源：client=客户端整文件读算；NULL=未上报（秒传不生效） */
    private String md5Reliable;
}
