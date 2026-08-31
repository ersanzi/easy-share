package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;
import lombok.EqualsAndHashCode;
import org.dromara.common.mybatis.core.domain.BaseEntity;

/**
 * 共享空间成员授权。
 * <p>
 * 只有共享空间需要成员行——个人空间由 owner 独占，归属靠对象键前缀 {@code users/{userId}/}
 * 强制，不需要额外授权表。
 *
 * @author EasyShare
 */
@Data
@EqualsAndHashCode(callSuper = true)
@TableName("es_space_member")
public class EsSpaceMember extends BaseEntity {

    /** 只读 */
    public static final String PERM_READ = "read";

    /** 读写 */
    public static final String PERM_WRITE = "write";

    @TableId(value = "id")
    private Long id;

    private Long spaceId;

    private Long userId;

    /** read / write */
    private String permission;
}
