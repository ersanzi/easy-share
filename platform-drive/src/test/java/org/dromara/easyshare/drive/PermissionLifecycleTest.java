package org.dromara.easyshare.drive;

import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.domain.*;
import org.dromara.easyshare.drive.mapper.*;
import org.dromara.easyshare.drive.service.*;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import java.util.*;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

/** 同一账号在停用、撤权、换部门前后的裁决必须读取当前状态。 */
class PermissionLifecycleTest {
    private final SysUserDeptMapper users = mock(SysUserDeptMapper.class);
    private final SysDeptRowMapper depts = mock(SysDeptRowMapper.class);
    private final EsUserRoleMapper bindings = mock(EsUserRoleMapper.class);
    private final EsUserPermissionMapper flags = mock(EsUserPermissionMapper.class);
    private final EsPermissionRoleMapper roles = mock(EsPermissionRoleMapper.class);
    private final PermissionSubjectService subjects = new PermissionSubjectService(users, depts);
    private final PermissionWriteService writes = mock(PermissionWriteService.class);
    private final FeaturePermissionService features = new FeaturePermissionService(flags, bindings, subjects, writes);
    private final PermissionRoleService permissions = new PermissionRoleService(roles, bindings, flags, users, depts, subjects, writes);
    private SysUserDept actor;
    private SysUserDept target;
    private SysDeptRow dept;

    @BeforeEach void setup() {
        actor = user(7L, 100L); target = user(8L, 100L);
        dept = new SysDeptRow(); dept.setDeptId(100L); dept.setStatus("0"); dept.setDelFlag("0");
        when(depts.selectById(100L)).thenReturn(dept);
    }

    private SysUserDept user(long id, Long deptId) {
        SysUserDept user = new SysUserDept(); user.setUserId(id); user.setDeptId(deptId);
        user.setStatus("0"); user.setDelFlag("0"); when(users.selectById(id)).thenReturn(user);
        return user;
    }

    private EsPermissionRole adminRole(String scope) {
        EsPermissionRole role = new EsPermissionRole(); role.setRoleId(9L); role.setAdminScope(scope);
        when(bindings.selectRolesOfUser(7L)).thenReturn(List.of(role));
        return role;
    }

    @Test void unknownOrDisabledUsersCannotInheritDefaultFeatures() {
        assertFalse(features.of(999L).drive());
        assertTrue(features.of(7L).drive());
        actor.setStatus("1"); assertFalse(features.of(7L).drive());
        actor.setStatus("0"); actor.setDelFlag("2"); assertFalse(features.of(7L).knowledge());
    }

    @Test void disabledDepartmentImmediatelyClosesFeaturesAndManagement() {
        adminRole("dept"); assertTrue(permissions.canManage(7L, 8L));
        dept.setStatus("1");
        assertFalse(features.sharedFeatureEnabled(7L));
        assertFalse(permissions.canManage(7L, 8L));
        assertThrows(ServiceException.class, () -> permissions.membersOf(7L));
    }

    @Test void movingTargetOutOfDepartmentImmediatelyRevokesManagement() {
        adminRole("dept"); assertTrue(permissions.canManage(7L, 8L));
        SysDeptRow other = new SysDeptRow(); other.setDeptId(200L); other.setStatus("0"); other.setDelFlag("0");
        when(depts.selectById(200L)).thenReturn(other);
        target.setDeptId(200L);
        assertFalse(permissions.canManage(7L, 8L));
    }

    @Test void removingAdminRoleImmediatelyRevokesRange() {
        adminRole("all"); assertTrue(permissions.canManage(7L, 8L));
        when(bindings.selectRolesOfUser(7L)).thenReturn(List.of());
        assertFalse(permissions.canManage(7L, 8L));
    }

    @Test void disabledSpecifiedDepartmentAndOverflowCsvGrantNothing() {
        EsPermissionRole role = adminRole("specified");
        role.setScopeDeptIds("0,-1,999999999999999999999999,200");
        assertEquals("none", permissions.adminScopeOf(7L));
        assertFalse(permissions.canManage(7L, 8L));
    }

    @Test void orphanRoleBindingCannotFallBackToOpenDefaults() {
        when(bindings.selectRoleIdsOfUser(7L)).thenReturn(List.of(99L));
        assertFalse(features.of(7L).drive());
        verify(flags, never()).selectById(7L);
    }

    @Test void invalidAndMissingRoleListNeverClearsExistingRoles() {
        assertThrows(ServiceException.class, () -> permissions.assignRoles(8L, null));
        assertThrows(ServiceException.class, () -> permissions.assignRoles(8L, Arrays.asList(9L, null)));
        assertThrows(ServiceException.class, () -> permissions.assignRoles(8L, List.of(-1L)));
        verify(bindings, never()).deleteByUser(8L);
    }

    @Test void deletedRoleAndDisabledTargetAreRejectedBeforeReplacingBindings() {
        assertThrows(ServiceException.class, () -> permissions.assignRoles(8L, List.of(9L)));
        target.setStatus("1");
        assertThrows(ServiceException.class, () -> permissions.assignRoles(8L, List.of()));
        verify(bindings, never()).deleteByUser(8L);
    }

    @Test void invalidTargetCannotReceiveIndividualOverrides() {
        assertThrows(ServiceException.class, () -> features.save(999L, true, true, true, true, true));
        target.setStatus("1");
        assertThrows(ServiceException.class, () -> features.save(8L, true, true, true, true, true));
        verifyNoInteractions(flags);
    }

    @Test void staleOpenOverrideIsReplacedWhenLastRestrictedRoleIsRemoved() {
        EsPermissionRole role = adminRole("none"); role.setDrive(false);
        when(bindings.selectRoleIdsOfUser(7L)).thenReturn(List.of(9L));
        EsUserPermission old = new EsUserPermission(); old.setUserId(7L); old.setDrive(true);
        when(flags.selectById(7L)).thenReturn(old);
        permissions.assignRoles(7L, List.of());
        verify(flags).updateById(old); assertFalse(old.getDrive());
        var order = inOrder(writes, flags, bindings);
        order.verify(writes).begin(7L);
        order.verify(flags).updateById(old);
        order.verify(bindings).deleteByUser(7L);
        order.verify(writes).audit(eq("role.assign"), eq(7L), any(), any());
    }

    @Test void unknownFileRoleDoesNotTurnIntoDefaultManage() {
        EsFile file = new EsFile(); file.setSpaceType("shared"); file.setState("active"); file.setUploadBy(1L);
        assertNull(FileRoleService.ladder(file, 7L, 100L, "write", "broken", null));
    }

    @Test void disabledSpaceAndInvalidPermissionCannotYieldFileAccess() {
        EsSpaceMapper spaces = mock(EsSpaceMapper.class);
        EsSpaceMemberMapper members = mock(EsSpaceMemberMapper.class);
        SpaceService service = new SpaceService(spaces, members, null, null, users, depts, features, subjects, writes);
        EsSpace shared = new EsSpace(); shared.setStatus("1"); when(spaces.selectById(1L)).thenReturn(shared);
        assertNull(service.sharedPermissionOf(7L));
        verifyNoInteractions(members);
        shared.setStatus("0"); EsSpaceMember invalid = new EsSpaceMember(); invalid.setPermission("owner");
        when(members.selectOne(any())).thenReturn(invalid);
        assertNull(service.sharedPermissionOf(7L));
    }

    @Test void invalidSpaceGrantFailsBeforeWritingAndRevocationRemainsPossible() {
        EsSpaceMemberMapper members = mock(EsSpaceMemberMapper.class);
        SpaceService service = new SpaceService(mock(EsSpaceMapper.class), members, null, null, users, depts, features, subjects, writes);
        target.setStatus("1");
        assertThrows(ServiceException.class, () -> service.grantSharedTo("user", 8L, "write"));
        assertThrows(ServiceException.class, () -> service.grantSharedTo("dept", 999L, "write"));
        verify(members, never()).insert(any(EsSpaceMember.class));
        service.grantSharedTo("user", 8L, null);
        verify(writes).audit("space.revoke", "user:8", null, null);
    }
    @Test void visibilityChangeLocksRechecksAndAudits() {
        EsFileMapper files = mock(EsFileMapper.class);
        EsFileMemberMapper members = mock(EsFileMemberMapper.class);
        SpaceService spaces = mock(SpaceService.class);
        FileRoleService service = new FileRoleService(members, files, spaces, users, depts, writes);
        EsFile file = new EsFile(); file.setFileId(42L); file.setSpaceType("shared");
        file.setState("active"); file.setUploadBy(7L);
        when(files.lockForPermissionUpdate(42L)).thenReturn(42L);
        when(files.selectById(42L)).thenReturn(file);
        when(spaces.sharedPermissionOf(7L)).thenReturn("write");
        assertEquals("100", service.replaceVisibleDepts(42L, 7L, "100"));
        var order = inOrder(writes, files);
        order.verify(writes).begin(7L); order.verify(files).lockForPermissionUpdate(42L);
        order.verify(files).selectById(42L); order.verify(files).updateById(file);
        order.verify(writes).audit("file.visibility", 42L, null, "100");
        when(spaces.sharedPermissionOf(7L)).thenReturn(null);
        assertThrows(ServiceException.class, () -> service.replaceVisibleDepts(42L, 7L, ""));
        verify(files, times(1)).updateById(file);
    }

    @Test void visibilityRejectsNewInactiveDepartmentWithoutWriting() {
        EsFileMapper files = mock(EsFileMapper.class);
        SpaceService spaces = mock(SpaceService.class);
        FileRoleService service = new FileRoleService(mock(EsFileMemberMapper.class), files, spaces, users, depts, writes);
        EsFile file = new EsFile(); file.setFileId(42L); file.setSpaceType("shared");
        file.setState("active"); file.setUploadBy(7L);
        when(files.lockForPermissionUpdate(42L)).thenReturn(42L);
        when(files.selectById(42L)).thenReturn(file);
        when(spaces.sharedPermissionOf(7L)).thenReturn("write");
        assertThrows(ServiceException.class, () -> service.replaceVisibleDepts(42L, 7L, "999"));
        verify(files, never()).updateById(any(EsFile.class));
    }

    @Test void personalVisibilityRequiresActualOperatorOwnership() {
        EsFileMapper files = mock(EsFileMapper.class);
        EsFile file = new EsFile(); file.setFileId(42L); file.setSpaceType("personal"); file.setOwnerId(8L);
        when(files.selectOne(any())).thenReturn(file);
        DriveFileService service = new DriveFileService(files, mock(DriveChangeLogService.class), mock(FileRoleService.class));
        assertThrows(ServiceException.class, () -> service.setRegisteredVisibleDepts("personal", 8L, 7L, "file.txt", List.of()));
        verify(files, never()).updateById(any(EsFile.class));
    }

    @Test void malformedRoleScopeDoesNotBreakRoleListOrShowEnabledFlags() {
        EsPermissionRole role = new EsPermissionRole(); role.setScopeDeptIds("0,-1,100,100,999999999999999999999999");
        RoleVo view = RoleVo.of(role, 0L);
        assertEquals(List.of(100L), view.scopeDeptIds()); assertFalse(view.drive());
    }

    @Test void partiallyOrphanedRoleBindingsAlsoFailClosed() {
        EsPermissionRole role = adminRole("none"); role.setDrive(true);
        when(bindings.selectRoleIdsOfUser(7L)).thenReturn(List.of(9L, 99L));
        assertFalse(features.of(7L).drive());
    }

}
