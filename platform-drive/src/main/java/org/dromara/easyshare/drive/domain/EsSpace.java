package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;
import lombok.EqualsAndHashCode;
import org.dromara.common.mybatis.core.domain.BaseEntity;

/**
 * 空间实体：一行一个空间。
 * <p>
 * 刻意**不存已用量**：签发预签名 URL 后客户端直传 RustFS，控制面不在数据路径上，
 * 库里的用量字段必然与真实脱节。用量一律由 {@link SpaceUsageService} 实时聚合。
 *
 * @author EasyShare
 */
@Data
@EqualsAndHashCode(callSuper = true)
@TableName("es_space")
public class EsSpace extends BaseEntity {

    /** 个人空间 */
    public static final String TYPE_PERSONAL = "personal";

    /** 共享空间 */
    public static final String TYPE_SHARED = "shared";

    /** 共享空间是全局单例，DDL 预置的固定 ID */
    public static final Long SHARED_SPACE_ID = 1L;

    /** 配额未分配：客户端显示「待开空间」 */
    public static final long QUOTA_UNSET = 0L;

    /** 配额不限 */
    public static final long QUOTA_UNLIMITED = -1L;

    @TableId(value = "space_id")
    private Long spaceId;

    /** personal / shared */
    private String spaceType;

    /** 归属用户 ID，共享空间为 0 */
    private Long ownerId;

    private String spaceName;

    /** 配额字节数：0 未分配、-1 不限 */
    private Long quotaBytes;

    /** 0 正常、1 停用 */
    private String status;
}
