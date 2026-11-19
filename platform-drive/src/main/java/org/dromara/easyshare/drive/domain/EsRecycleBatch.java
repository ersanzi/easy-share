package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.IdType;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;

import java.time.LocalDateTime;

/** 回收站专用删除批次与幂等操作账；不承担泛用任务职责。 */
@Data
@TableName("es_recycle_batch")
public class EsRecycleBatch {

    /** 服务端生成的唯一批次标识，不能由 MyBatis 自动生成数字 ID。 */
    @TableId(value = "batch_id", type = IdType.INPUT)
    private String batchId;

    /** personal / shared */
    private String spaceType;
    /** personal=用户 ID，shared=0。 */
    private Long ownerId;
    /** 操作者取自登录态。 */
    private Long operatorId;
    /** 在空间与操作者范围内唯一的删除操作标识。 */
    private String operationId;
    /** 本批删除根项的稳定身份。 */
    private Long rootFileId;
    /** 删除时根路径快照，不能用作跨批历史清理前缀。 */
    private String rootPath;
    private Boolean directory;
    private Long expectedVersion;
    /** preparing/recycled/restoring/restored/purging/purged。 */
    private String status;
    /** 本批确切成员数（含根项）。 */
    private Integer memberCount;
    /** 准确成员身份和快照元数据；preparing失败时也保留以便同批次重试。 */
    private String membersJson;
    private LocalDateTime createdAt;
    private LocalDateTime updatedAt;
    private LocalDateTime restoredAt;
}
