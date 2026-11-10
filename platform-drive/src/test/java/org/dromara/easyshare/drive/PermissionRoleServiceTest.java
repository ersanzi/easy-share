package org.dromara.easyshare.drive;

import org.dromara.common.core.constant.SystemConstants;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.domain.AdminMemberVo;
import org.dromara.easyshare.drive.domain.EsPermissionRole;
import org.dromara.easyshare.drive.domain.EsUserPermission;
import org.dromara.easyshare.drive.domain.SysDeptRow;
import org.dromara.easyshare.drive.domain.SysUserDept;
import org.dromara.easyshare.drive.domain.UserRoleBinding;
import org.dromara.easyshare.drive.mapper.EsPermissionRoleMapper;
import org.dromara.easyshare.drive.mapper.EsUserPermissionMapper;
import org.dromara.easyshare.drive.mapper.EsUserRoleMapper;
import org.dromara.easyshare.drive.mapper.SysDeptRowMapper;
import org.dromara.easyshare.drive.mapper.SysUserDeptMapper;
import org.dromara.easyshare.drive.service.PermissionRoleService;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;

import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

/**
 * 角色与子管理员范围测试：范围判定（超管/有部门/无部门）、同部门才能管、
 * 删角色落快照、重名拒绝、成员列表按部门裁剪。
 *
 * @author EasyShare
 */
class PermissionRoleServiceTest {

    private EsPermissionRoleMapper roleMapper;
    private EsUserRoleMapper userRoleMapper;
    private EsUserPermissionMapper permissionMapper;
    private SysUserDeptMapper sysUserDeptMapper;
    private SysDeptRowMapper sysDeptRowMapper;
    private PermissionRoleService service;

    @BeforeEach
    void setUp() {
        roleMapper = mock(EsPermissionRoleMapper.class);
        userRoleMapper = mock(EsUserRoleMapper.class);
        permissionMapper = mock(EsUserPermissionMapper.class);
        sysUserDeptMapper = mock(SysUserDeptMapper.class);
        sysDeptRowMapper = mock(SysDeptRowMapper.class);
        service = new PermissionRoleService(roleMapper, userRoleMapper, permissionMapper,
            sysUserDeptMapper, sysDeptRowMapper);
    }

    private static EsPermissionRole role(long roleId, String scope, boolean knowledge, boolean meeting,
                                         boolean drive, boolean shared, boolean plugin) {
        EsPermissionRole role = new EsPermissionRole();
        role.setRoleId(roleId);
        role.setRoleName("角色" + roleId);
        role.setAdminScope(scope);
        role.setKnowledge(knowledge);
        role.setMeeting(meeting);
        role.setDrive(drive);
        role.setShared(shared);
        role.setPlugin(plugin);
        return role;
    }

    private static SysUserDept user(long userId, Long deptId) {
        SysUserDept user = new SysUserDept();
        user.setUserId(userId);
        user.setDeptId(deptId);
        user.setUserName("u" + userId);
        user.setNickName("用户" + userId);
        user.setStatus("0");
        user.setDelFlag("0");
        return user;
    }

    @Test
    void superAdminScopeIsAll() {
        assertEquals(PermissionRoleService.SCOPE_ALL,
            service.adminScopeOf(SystemConstants.SUPER_ADMIN_USER_ID));
    }

    @Test
    void deptRoleNeedsDepartment() {
        when(userRoleMapper.selectRolesOfUser(7L))
            .thenReturn(List.of(role(9L, EsPermissionRole.SCOPE_DEPT, true, true, true, true, true)));

        when(sysUserDeptMapper.selectById(7L)).thenReturn(user(7L, null));
        assertEquals(PermissionRoleService.SCOPE_NONE, service.adminScopeOf(7L));

        when(sysUserDeptMapper.selectById(7L)).thenReturn(user(7L, 100L));
        assertEquals(PermissionRoleService.SCOPE_DEPT, service.adminScopeOf(7L));
    }

    @Test
    void plainRoleDoesNotGrantAdmin() {
        when(userRoleMapper.selectRolesOfUser(7L))
            .thenReturn(List.of(role(9L, EsPermissionRole.SCOPE_NONE, true, true, true, true, true)));
        when(sysUserDeptMapper.selectById(7L)).thenReturn(user(7L, 100L));

        assertEquals(PermissionRoleService.SCOPE_NONE, service.adminScopeOf(7L));
    }

    @Test
    void canManageOnlySameDept() {
        when(userRoleMapper.selectRolesOfUser(7L))
            .thenReturn(List.of(role(9L, EsPermissionRole.SCOPE_DEPT, true, true, true, true, true)));
        when(sysUserDeptMapper.selectById(7L)).thenReturn(user(7L, 100L));
        when(sysUserDeptMapper.selectById(8L)).thenReturn(user(8L, 100L));
        when(sysUserDeptMapper.selectById(9L)).thenReturn(user(9L, 200L));

        assertTrue(service.canManage(7L, 8L));
        assertFalse(service.canManage(7L, 9L));
        // 超管不受部门限制
        assertTrue(service.canManage(SystemConstants.SUPER_ADMIN_USER_ID, 9L));
    }

    @Test
    void deleteMaterializesSnapshotForSingleRoleMember() {
        EsPermissionRole role = role(9L, EsPermissionRole.SCOPE_NONE, true, false, true, false, true);
        when(roleMapper.selectById(9L)).thenReturn(role);
        when(userRoleMapper.selectUserIdsOfRole(9L)).thenReturn(List.of(5L));
        when(userRoleMapper.selectRoleIdsOfUser(5L)).thenReturn(List.of());
        when(permissionMapper.selectById(5L)).thenReturn(null);

        service.delete(9L);

        ArgumentCaptor<EsUserPermission> captor = ArgumentCaptor.forClass(EsUserPermission.class);
        verify(permissionMapper).insert(captor.capture());
        EsUserPermission snapshot = captor.getValue();
        assertEquals(5L, snapshot.getUserId());
        assertTrue(snapshot.getKnowledge());
        assertFalse(snapshot.getMeeting());
        assertTrue(snapshot.getDrive());
        assertFalse(snapshot.getShared());
        assertTrue(snapshot.getPlugin());
        verify(userRoleMapper).deleteByRole(9L);
        verify(roleMapper).deleteById(9L);
    }

    @Test
    void deleteSkipsSnapshotWhenMemberHasOtherRoles() {
        when(roleMapper.selectById(9L)).thenReturn(role(9L, EsPermissionRole.SCOPE_NONE,
            true, true, true, true, true));
        when(userRoleMapper.selectUserIdsOfRole(9L)).thenReturn(List.of(5L));
        when(userRoleMapper.selectRoleIdsOfUser(5L)).thenReturn(List.of(10L));

        service.delete(9L);

        verify(permissionMapper, never()).insert(any(EsUserPermission.class));
        verify(permissionMapper, never()).updateById(any(EsUserPermission.class));
        verify(roleMapper).deleteById(9L);
    }

    @Test
    void saveRejectsDuplicateName() {
        when(roleMapper.selectOne(any())).thenReturn(role(9L, EsPermissionRole.SCOPE_NONE,
            true, true, true, true, true));

        assertThrows(ServiceException.class, () -> service.save(null, "角色9", "none", null,
            true, true, true, true, true));
        verify(roleMapper, never()).insert(any(EsPermissionRole.class));
    }

    @Test
    void saveRejectsSpecifiedWithoutDeptIds() {
        // specified 档必须有至少一个部门；其余档位忽略传入的列表
        assertThrows(ServiceException.class, () -> service.save(null, "质检管理", "specified", null,
            true, true, true, true, true));
        assertThrows(ServiceException.class, () -> service.save(null, "质检管理", "specified", List.of(0L),
            true, true, true, true, true));
        // 非 specified 档带部门列表不报错，列表被清空
        when(roleMapper.selectOne(any())).thenReturn(null);
        service.save(null, "华东管理", "dept_children", List.of(100L), true, true, true, true, true);
        ArgumentCaptor<EsPermissionRole> captor = ArgumentCaptor.forClass(EsPermissionRole.class);
        verify(roleMapper).insert(captor.capture());
        assertEquals("dept_children", captor.getValue().getAdminScope());
        assertEquals(null, captor.getValue().getScopeDeptIds());
    }

    @Test
    void deptChildrenScopeIncludesDescendants() {
        // 层级：100（本部门）→ 110 → 111；200 在别的子树
        when(userRoleMapper.selectRolesOfUser(7L))
            .thenReturn(List.of(role(9L, EsPermissionRole.SCOPE_DEPT_CHILDREN, true, true, true, true, true)));
        when(sysUserDeptMapper.selectById(7L)).thenReturn(user(7L, 100L));
        when(sysUserDeptMapper.selectById(8L)).thenReturn(user(8L, 111L));
        when(sysUserDeptMapper.selectById(9L)).thenReturn(user(9L, 200L));
        when(sysDeptRowMapper.selectList(any())).thenReturn(List.of(
            deptRow(100L, 0L), deptRow(110L, 100L), deptRow(111L, 110L), deptRow(200L, 0L)));

        assertTrue(service.canManage(7L, 8L), "下级部门的成员应可管");
        assertFalse(service.canManage(7L, 9L), "别的子树不可管");
        assertEquals(PermissionRoleService.SCOPE_DEPT_CHILDREN, service.adminScopeOf(7L));
    }

    @Test
    void specifiedScopeUsesRoleDeptList() {
        EsPermissionRole role = role(9L, EsPermissionRole.SCOPE_SPECIFIED, true, true, true, true, true);
        role.setScopeDeptIds("200, 300");
        when(userRoleMapper.selectRolesOfUser(7L)).thenReturn(List.of(role));
        // 操作者本人属于研发部（100），范围与本人部门无关
        when(sysUserDeptMapper.selectById(7L)).thenReturn(user(7L, 100L));
        when(sysUserDeptMapper.selectById(8L)).thenReturn(user(8L, 200L));
        when(sysUserDeptMapper.selectById(9L)).thenReturn(user(9L, 400L));

        assertTrue(service.canManage(7L, 8L), "指定部门内的成员应可管");
        assertFalse(service.canManage(7L, 9L), "指定范围外的部门不可管");
        assertEquals(PermissionRoleService.SCOPE_SPECIFIED, service.adminScopeOf(7L));
    }

    @Test
    void specifiedScopeWithoutDeptListGrantsNothing() {
        EsPermissionRole role = role(9L, EsPermissionRole.SCOPE_SPECIFIED, true, true, true, true, true);
        role.setScopeDeptIds("  ");
        when(userRoleMapper.selectRolesOfUser(7L)).thenReturn(List.of(role));
        when(sysUserDeptMapper.selectById(7L)).thenReturn(user(7L, 100L));
        when(sysUserDeptMapper.selectById(8L)).thenReturn(user(8L, 100L));

        assertFalse(service.canManage(7L, 8L), "空部门列表的 specified 角色不生效");
        assertEquals(PermissionRoleService.SCOPE_NONE, service.adminScopeOf(7L));
        assertThrows(ServiceException.class, () -> service.membersOf(7L));
    }

    @Test
    void allScopeGrantsWholeCompany() {
        when(userRoleMapper.selectRolesOfUser(7L))
            .thenReturn(List.of(role(9L, EsPermissionRole.SCOPE_ALL, true, true, true, true, true)));
        when(sysUserDeptMapper.selectById(9L)).thenReturn(user(9L, 200L));

        assertTrue(service.canManage(7L, 9L), "全部成员档位不限部门");
        assertEquals(PermissionRoleService.SCOPE_ALL, service.adminScopeOf(7L));
    }

    @Test
    void multipleScopeRolesUnion() {
        // dept（本部门 100）+ specified（200）：并集 = {100, 200}
        EsPermissionRole specified = role(10L, EsPermissionRole.SCOPE_SPECIFIED, true, true, true, true, true);
        specified.setScopeDeptIds("200");
        when(userRoleMapper.selectRolesOfUser(7L)).thenReturn(List.of(
            role(9L, EsPermissionRole.SCOPE_DEPT, true, true, true, true, true), specified));
        when(sysUserDeptMapper.selectById(7L)).thenReturn(user(7L, 100L));
        when(sysUserDeptMapper.selectById(8L)).thenReturn(user(8L, 100L));
        when(sysUserDeptMapper.selectById(9L)).thenReturn(user(9L, 200L));
        when(sysUserDeptMapper.selectById(11L)).thenReturn(user(11L, 300L));

        assertTrue(service.canManage(7L, 8L), "并集含本部门");
        assertTrue(service.canManage(7L, 9L), "并集含指定部门");
        assertFalse(service.canManage(7L, 11L), "并集之外的部门不可管");
    }

    @Test
    void deptChildrenWithoutOwnDeptDoesNotGrantAdmin() {
        when(userRoleMapper.selectRolesOfUser(7L))
            .thenReturn(List.of(role(9L, EsPermissionRole.SCOPE_DEPT_CHILDREN, true, true, true, true, true)));
        when(sysUserDeptMapper.selectById(7L)).thenReturn(user(7L, null));

        assertEquals(PermissionRoleService.SCOPE_NONE, service.adminScopeOf(7L));
    }

    private static SysDeptRow deptRow(long deptId, long parentId) {
        SysDeptRow row = new SysDeptRow();
        row.setDeptId(deptId);
        row.setParentId(parentId);
        row.setDeptName("部门" + deptId);
        row.setDelFlag("0");
        return row;
    }

    @Test
    void membersAreScopedToOwnDepartment() {
        when(userRoleMapper.selectRolesOfUser(7L))
            .thenReturn(List.of(role(9L, EsPermissionRole.SCOPE_DEPT, true, false, true, true, true)));
        when(sysUserDeptMapper.selectById(7L)).thenReturn(user(7L, 100L));
        when(sysUserDeptMapper.selectList(any()))
            .thenReturn(List.of(user(7L, 100L), user(8L, 100L), user(9L, 200L)));
        when(roleMapper.selectList(any())).thenReturn(List.of(role(9L, EsPermissionRole.SCOPE_DEPT,
            true, false, true, true, true)));
        when(userRoleMapper.selectAll()).thenReturn(List.of(
            binding(7L, 9L), binding(9L, 9L)));
        when(permissionMapper.selectList(any())).thenReturn(List.of());
        SysDeptRow dept = new SysDeptRow();
        dept.setDeptId(100L);
        dept.setDeptName("车间");
        when(sysDeptRowMapper.selectList(any())).thenReturn(List.of(dept));

        List<AdminMemberVo> members = service.membersOf(7L);

        // 7 与 8 同部门；9 在别的部门被裁掉
        assertEquals(2, members.size());
        AdminMemberVo first = members.get(0);
        assertEquals(7L, first.userId());
        assertEquals(PermissionRoleService.SCOPE_DEPT, first.adminScope());
        assertTrue(first.roleIds().contains(9L));
        assertFalse(first.meeting(), "角色 meeting=false 应生效");
        AdminMemberVo second = members.get(1);
        assertEquals(8L, second.userId());
        // 无角色、无逐账号行 → 全开
        assertTrue(second.knowledge() && second.meeting() && second.drive()
            && second.shared() && second.plugin());
        assertEquals("车间", second.deptName());
    }

    @Test
    void membersRejectedWithoutScope() {
        when(userRoleMapper.selectRolesOfUser(7L)).thenReturn(List.of());
        when(sysUserDeptMapper.selectById(7L)).thenReturn(user(7L, 100L));

        assertThrows(ServiceException.class, () -> service.membersOf(7L));
    }

    private static UserRoleBinding binding(long userId, long roleId) {
        UserRoleBinding binding = new UserRoleBinding();
        binding.setUserId(userId);
        binding.setRoleId(roleId);
        return binding;
    }
}
