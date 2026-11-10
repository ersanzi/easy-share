package org.dromara.easyshare.drive.mapper;

import org.apache.ibatis.annotations.Delete;
import org.apache.ibatis.annotations.Insert;
import org.apache.ibatis.annotations.Param;
import org.apache.ibatis.annotations.Select;
import org.dromara.easyshare.drive.domain.EsPermissionRole;
import org.dromara.easyshare.drive.domain.UserRoleBinding;

import java.util.List;

/**
 * 成员与角色的绑定 Mapper（{@code es_user_role}，复合主键）。
 * <p>
 * 全部走显式 SQL：复合主键在 MyBatis-Plus 实体里没有自然写法，
 * 而这张表只有「按人/按角色整组替换」两种操作，不需要单行更新。
 *
 * @author EasyShare
 */
public interface EsUserRoleMapper {

    /** 某账号拥有的角色（含角色自身的五项开关与范围） */
    @Select("""
        SELECT r.role_id, r.role_name, r.admin_scope,
               r.knowledge, r.meeting, r.drive, r.shared, r.plugin
        FROM es_user_role ur
        JOIN es_permission_role r ON r.role_id = ur.role_id
        WHERE ur.user_id = #{userId}
        ORDER BY r.role_id
        """)
    List<EsPermissionRole> selectRolesOfUser(@Param("userId") Long userId);

    /** 某账号拥有的角色 ID */
    @Select("SELECT role_id FROM es_user_role WHERE user_id = #{userId} ORDER BY role_id")
    List<Long> selectRoleIdsOfUser(@Param("userId") Long userId);

    /** 全部绑定行，供管理页一次拉齐后按人分组 */
    @Select("SELECT user_id, role_id FROM es_user_role")
    List<UserRoleBinding> selectAll();

    /** 某角色的成员 */
    @Select("SELECT user_id FROM es_user_role WHERE role_id = #{roleId}")
    List<Long> selectUserIdsOfRole(@Param("roleId") Long roleId);

    @Insert("INSERT INTO es_user_role(user_id, role_id) VALUES(#{userId}, #{roleId})")
    int insertRow(@Param("userId") Long userId, @Param("roleId") Long roleId);

    @Delete("DELETE FROM es_user_role WHERE user_id = #{userId}")
    int deleteByUser(@Param("userId") Long userId);

    @Delete("DELETE FROM es_user_role WHERE role_id = #{roleId}")
    int deleteByRole(@Param("roleId") Long roleId);
}
