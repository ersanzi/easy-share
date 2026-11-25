package org.dromara.easyshare.drive.domain;

import java.util.List;

/**
 * 角色及其成员数（管理页角色列表的一行）。
 *
 * @param roleId       角色 ID
 * @param roleName     角色名
 * @param adminScope   none / dept / dept_children / specified / all
 * @param scopeDeptIds 指定部门 ID 列表（adminScope=specified 时非空，其余档位为空列表）
 * @param knowledge    知识问答
 * @param meeting      会议听记
 * @param drive        网盘
 * @param shared       共享空间
 * @param plugin       插件中心
 * @param memberCount  已分配成员数
 * @author EasyShare
 */
public record RoleVo(
    Long roleId,
    String roleName,
    String adminScope,
    List<Long> scopeDeptIds,
    boolean knowledge,
    boolean meeting,
    boolean drive,
    boolean shared,
    boolean plugin,
    long memberCount
) {

    public static RoleVo of(EsPermissionRole role, long memberCount) {
        return new RoleVo(
            role.getRoleId(),
            role.getRoleName(),
            role.getAdminScope(),
            parseDeptIds(role.getScopeDeptIds()),
            Boolean.TRUE.equals(role.getKnowledge()),
            Boolean.TRUE.equals(role.getMeeting()),
            Boolean.TRUE.equals(role.getDrive()),
            Boolean.TRUE.equals(role.getShared()),
            Boolean.TRUE.equals(role.getPlugin()),
            memberCount);
    }

    /** 逗号分隔的部门 ID 解析成列表；空串/非法片段忽略，不抛错（列表展示要宽容） */
    private static List<Long> parseDeptIds(String csv) {
        if (csv == null || csv.isBlank()) {
            return List.of();
        }
        java.util.Set<Long> ids = new java.util.LinkedHashSet<>();
        for (String part : csv.split(",")) {
            try {
                long id = Long.parseLong(part.trim());
                if (id > 0) ids.add(id);
            } catch (NumberFormatException ignored) {
                // 损坏的历史范围不影响管理页列举，也不能扩大授权。
            }
        }
        return List.copyOf(ids);
    }
}
