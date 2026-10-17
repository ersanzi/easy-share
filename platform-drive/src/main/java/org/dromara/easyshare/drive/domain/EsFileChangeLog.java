package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;
import lombok.EqualsAndHashCode;
import org.dromara.common.mybatis.core.domain.BaseEntity;

/**
 * 增量列举的变更日志（路线图切片 2）。
 *
 * <p>这张表不是文件状态机的真相源（那是 {@code es_file}），
 * 而是“给客户端看的差异流”：客户端记住上次看到的 {@code spaceVersion}，
 * 下一次只拉它之后的变更。日志不足时退全量重建。</p>
 *
 * @author EasyShare
 */
@Data
@EqualsAndHashCode(callSuper = true)
@TableName("es_file_change_log")
public class EsFileChangeLog extends BaseEntity {

    /** 新建（含首次登记） */
    public static final String EVENT_CREATED = "created";

    /** 覆盖更新（同名上传，版本递增） */
    public static final String EVENT_UPDATED = "updated";

    /** 软删进回收站 */
    public static final String EVENT_DELETED = "deleted";

    /** 从回收站还原 */
    public static final String EVENT_RESTORED = "restored";

    /** 改名/移动 */
    public static final String EVENT_MOVED = "moved";

    /** 彻底删除（对象已释放，filePath 为空） */
    public static final String EVENT_PURGED = "purged";

    @TableId(value = "log_id")
    private Long logId;

    /** personal / shared */
    private String spaceType;

    /** 空间归属：personal=用户ID、shared=0 */
    private Long ownerId;

    /** 该变更对应的空间版本（每空间单调递增） */
    private Long spaceVersion;

    /** created / updated / deleted / restored / moved / purged */
    private String eventType;

    /** 事件对应的稳定文件 ID */
    private Long fileId;

    /** 事件后的当前路径；purged 时为空 */
    private String filePath;

    /** moved 的源路径；其他事件为空 */
    private String oldPath;
}
