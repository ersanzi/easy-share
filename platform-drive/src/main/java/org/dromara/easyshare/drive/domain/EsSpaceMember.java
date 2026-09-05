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

    /** 授权主体：账号（user_id 列 = 用户 ID，默认值，存量行即此型） */
    public static final String TYPE_USER = "user";

    /** 授权主体：部门（user_id 列 = 部门 ID，列名不动、语义复用） */
    public static final String TYPE_DEPT = "dept";

    @TableId(value = "id")
    private Long id;

    private Long spaceId;

    /** 主体 ID：user 型=用户 ID、dept 型=部门 ID（列名保持兼容） */
    private Long userId;

    /** 授权主体类型：user / dept */
    private String memberType;

    /** read / write */
    private String permission;
}
