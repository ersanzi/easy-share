package org.dromara.easyshare.drive.domain;

import lombok.Data;

/**
 * 成员与角色的绑定行（{@code es_user_role} 的只读投影）。
 * <p>
 * 复合主键（user_id + role_id）用 MyBatis-Plus 的实体不方便表达，本表也不承担
 * 单行更新，读写一律走 {@link org.dromara.easyshare.drive.mapper.EsUserRoleMapper}
 * 的显式 SQL。
 *
 * @author EasyShare
 */
@Data
public class UserRoleBinding {

    private Long userId;

    private Long roleId;
}
