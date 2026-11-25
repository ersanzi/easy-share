package org.dromara.easyshare.drive;

import org.apache.ibatis.annotations.Select;
import org.dromara.easyshare.drive.domain.*;
import org.dromara.easyshare.drive.mapper.*;
import org.dromara.easyshare.drive.service.*;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;
import java.util.List;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

/** 撤权、停用和损坏数据必须沿真实裁决路径失败关闭。 */
class PermissionFailureBranchesTest {
    private final EsPermissionRoleMapper roles = mock(EsPermissionRoleMapper.class);
    private final EsUserRoleMapper bindings = mock(EsUserRoleMapper.class);
    private final EsUserPermissionMapper flags = mock(EsUserPermissionMapper.class);
    private final SysUserDeptMapper users = mock(SysUserDeptMapper.class);
    private final SysDeptRowMapper depts = mock(SysDeptRowMapper.class);
    private final PermissionRoleService service = new PermissionRoleService(roles, bindings, flags, users, depts,
        new PermissionSubjectService(users, depts), mock(PermissionWriteService.class));

    private SysUserDept user(long id, String status) {
        SysUserDept user = new SysUserDept();
        user.setUserId(id); user.setDeptId(null); user.setStatus(status); user.setDelFlag("0");
        when(users.selectById(id)).thenReturn(user);
        return user;
    }

    private EsPermissionRole restrictedRole() {
        EsPermissionRole role = new EsPermissionRole();
        role.setRoleId(9L); role.setAdminScope("all");
        role.setKnowledge(false); role.setMeeting(false); role.setDrive(false);
        role.setShared(false); role.setPlugin(false);
        when(bindings.selectRolesOfUser(7L)).thenReturn(List.of(role));
        return role;
    }

    @Test void disabledAdministratorCannotManageMembers() {
        restrictedRole(); user(7L, "1"); user(8L, "0");
        assertFalse(service.canManage(7L, 8L));
        assertEquals("none", service.adminScopeOf(7L));
    }

    @Test void subordinateCannotManageSuperAdministrator() {
        restrictedRole(); user(7L, "0");
        long superAdmin = org.dromara.common.core.constant.SystemConstants.SUPER_ADMIN_USER_ID;
        user(superAdmin, "0");
        assertFalse(service.canManage(7L, superAdmin));
    }

    @Test void missingOrDisabledTargetCannotReceivePermission() {
        restrictedRole(); user(7L, "0");
        assertFalse(service.canManage(7L, 8L));
        user(8L, "1");
        assertFalse(service.canManage(7L, 8L));
    }

    @Test void removingLastRolePreservesRestrictions() {
        user(7L, "0"); restrictedRole();
        when(bindings.selectRoleIdsOfUser(7L)).thenReturn(List.of(9L));
        service.assignRoles(7L, List.of());
        ArgumentCaptor<EsUserPermission> saved = ArgumentCaptor.forClass(EsUserPermission.class);
        verify(flags).insert(saved.capture());
        assertFalse(saved.getValue().getDrive());
    }

    @Test void incompletePermissionRowsNeverGrantMissingFlags() {
        EsUserPermission row = new EsUserPermission(); row.setUserId(7L);
        assertFalse(PermissionVo.of(row).drive());
        assertFalse(PermissionVo.union(7L, List.of(new EsPermissionRole())).knowledge());
    }

    @Test void roleProjectionRetainsSpecifiedDepartmentScope() throws Exception {
        String sql = String.join(" ", EsUserRoleMapper.class.getMethod("selectRolesOfUser", Long.class)
            .getAnnotation(Select.class).value());
        assertTrue(sql.contains("r.scope_dept_ids"));
    }

    @Test void revokedSpaceMembershipDeniesUploaderAndExplicitFileManager() {
        EsFile file = new EsFile(); file.setFileId(100L); file.setSpaceType("shared");
        file.setState("active"); file.setUploadBy(7L);
        assertNull(FileRoleService.ladder(file, 7L, 100L, null, null, null));
        assertNull(FileRoleService.ladder(file, 8L, 100L, null, "manage", null));
    }
}
