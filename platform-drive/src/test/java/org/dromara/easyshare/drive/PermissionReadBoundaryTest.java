package org.dromara.easyshare.drive;

import com.baomidou.mybatisplus.core.MybatisConfiguration;
import com.baomidou.mybatisplus.core.metadata.TableInfoHelper;
import org.apache.ibatis.builder.MapperBuilderAssistant;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.domain.*;
import org.dromara.easyshare.drive.mapper.*;
import org.dromara.easyshare.drive.service.*;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.function.Executable;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
import org.mockito.MockedStatic;
import org.springframework.util.unit.DataSize;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

/** 真实主体服务配合旧会话：管理员读取必须先拒绝失效账号，再触碰业务数据与存储用量。 */
class PermissionReadBoundaryTest {
    private final SysUserDeptMapper users = mock(SysUserDeptMapper.class);
    private final SysDeptRowMapper depts = mock(SysDeptRowMapper.class);
    private final EsUserPermissionMapper flags = mock(EsUserPermissionMapper.class);
    private final EsUserRoleMapper bindings = mock(EsUserRoleMapper.class);
    private final EsPermissionRoleMapper roleRows = mock(EsPermissionRoleMapper.class);
    private final SpaceService spaces = mock(SpaceService.class);
    private final SpaceUsageService usage = mock(SpaceUsageService.class);
    private final CapacityService capacity = mock(CapacityService.class);
    private final DriveProperties properties = mock(DriveProperties.class);
    private final PermissionWriteService writes = mock(PermissionWriteService.class);
    private final PermissionSubjectService subjects = new PermissionSubjectService(users, depts);
    private final FeaturePermissionService features = new FeaturePermissionService(flags, bindings, subjects, writes);
    private final PermissionRoleService roles = new PermissionRoleService(roleRows, bindings, flags, users, depts, subjects, writes);
    private final UserPermissionController permissions = new UserPermissionController(features, roles, writes);
    private final SpaceController spaceController = new SpaceController(spaces, usage, capacity,
        properties, users, depts, features, subjects);
    private SysUserDept actor;
    private SysDeptRow department;
    private MockedStatic<LoginHelper> login;

    @BeforeAll
    static void initMetadata() {
        var assistant = new MapperBuilderAssistant(new MybatisConfiguration(), "");
        TableInfoHelper.initTableInfo(assistant, EsPermissionRole.class);
        TableInfoHelper.initTableInfo(assistant, EsUserPermission.class);
        TableInfoHelper.initTableInfo(assistant, SysUserDept.class);
        TableInfoHelper.initTableInfo(assistant, SysDeptRow.class);
    }

    @BeforeEach
    void setUp() {
        actor = new SysUserDept();
        actor.setUserId(1L);
        actor.setDeptId(100L);
        actor.setStatus("0");
        actor.setDelFlag("0");
        department = new SysDeptRow();
        department.setDeptId(100L);
        department.setDeptName("管理部门");
        department.setStatus("0");
        department.setDelFlag("0");
        when(users.selectById(1L)).thenReturn(actor);
        when(depts.selectById(100L)).thenReturn(department);
        login = mockStatic(LoginHelper.class);
        login.when(LoginHelper::getUserId).thenReturn(1L);
        login.when(() -> LoginHelper.isSuperAdmin(1L)).thenReturn(true);
    }

    @AfterEach
    void tearDown() {
        login.close();
    }

    @ParameterizedTest
    @ValueSource(strings = {"disabled", "deleted", "missing", "department-disabled", "department-deleted", "department-missing"})
    void oldSuperAdminSessionCannotReadAnyAdminEndpointAfterSubjectInvalidation(String state) {
        // 先以同一会话成功读取；随后只改数据库主体，证明读取不会信任缓存的有效状态。
        assertDoesNotThrow(permissions::adminList);
        assertDoesNotThrow(permissions::roles);
        clearInvocations(flags, bindings, roleRows);
        invalidate(state);

        Map<String, Executable> reads = adminReads();
        reads.put("permission/members", permissions::members);
        reads.forEach((route, read) -> assertThrows(ServiceException.class, read, route + ": " + state));
        verifyNoInteractions(flags, bindings, roleRows, spaces, usage, capacity, properties, writes);
        verify(users, never()).selectList(any());
        verify(depts, never()).selectList(any());
    }

    @ParameterizedTest
    @ValueSource(strings = {"disabled", "deleted", "missing", "department-disabled", "department-deleted", "department-missing"})
    void mineDropsOldRoleNamesAndAllCapabilitiesWhenSubjectBecomesInvalid(String state) {
        EsPermissionRole role = role();
        when(bindings.selectRolesOfUser(1L)).thenReturn(List.of(role));
        when(bindings.selectRoleIdsOfUser(1L)).thenReturn(List.of(role.getRoleId()));
        assertEquals(List.of("内部管理角色"), permissions.mine().getData().roles());
        clearInvocations(flags, bindings, roleRows);
        invalidate(state);

        var mine = permissions.mine().getData();
        assertTrue(mine.roles().isEmpty());
        assertEquals("none", mine.adminScope());
        assertFalse(mine.knowledge() || mine.meeting() || mine.drive() || mine.shared() || mine.plugin());
        verifyNoInteractions(flags, bindings, roleRows, writes);
    }

    @Test
    void activeSuperAdminKeepsManagementReadsEvenWithProductFeaturesDisabled() {
        EsUserPermission row = new EsUserPermission();
        row.setUserId(1L);
        row.setKnowledge(false); row.setMeeting(false); row.setDrive(false);
        row.setShared(false); row.setPlugin(false);
        when(flags.selectById(1L)).thenReturn(row);
        when(flags.selectList(any())).thenReturn(List.of(row));
        when(roleRows.selectList(any())).thenReturn(List.of(role()));
        EsSpace shared = new EsSpace();
        shared.setSpaceId(1L); shared.setSpaceType("shared"); shared.setOwnerId(0L);
        shared.setQuotaBytes(1_000L); shared.setStatus("0");
        when(spaces.findShared()).thenReturn(shared);
        when(spaces.listAll()).thenReturn(List.of(shared));
        when(usage.usedBytesGrouped()).thenReturn(Map.of("shared/", 50L));
        when(properties.getReservedBytes()).thenReturn(DataSize.ofBytes(100L));
        EsSpaceMember grant = new EsSpaceMember();
        grant.setMemberType("dept"); grant.setUserId(100L); grant.setPermission("read");
        when(spaces.listSharedMembers()).thenReturn(List.of(grant));
        when(spaces.listDepts()).thenReturn(List.of(department));

        assertFalse(features.of(1L).drive());
        assertEquals(1, permissions.adminList().getData().size());
        assertEquals("内部管理角色", permissions.roles().getData().getFirst().roleName());
        assertEquals("shared", spaceController.adminList().getData().getFirst().spaceType());
        assertEquals(1_000L, spaceController.capacity().getData().committedBytes());
        assertEquals("管理部门", spaceController.sharedMembers().getData().getFirst().name());
        assertEquals("管理部门", spaceController.depts().getData().getFirst().deptName());
        verifyNoInteractions(writes);
    }

    @Test
    void nonSuperAdminCannotReadGlobalDataButKeepsItsScopedMemberRead() {
        actor.setUserId(7L);
        when(users.selectById(7L)).thenReturn(actor);
        login.when(LoginHelper::getUserId).thenReturn(7L);
        adminReads().forEach((route, read) -> assertThrows(ServiceException.class, read, route));
        verifyNoInteractions(flags, bindings, roleRows, spaces, usage, capacity, properties, writes);

        EsPermissionRole role = role();
        role.setAdminScope("dept");
        when(bindings.selectRolesOfUser(7L)).thenReturn(List.of(role));
        assertDoesNotThrow(permissions::members);
    }

    @Test
    void activeOrdinaryAccountStillReadsItsOwnRoles() {
        actor.setUserId(7L);
        when(users.selectById(7L)).thenReturn(actor);
        login.when(LoginHelper::getUserId).thenReturn(7L);
        EsPermissionRole role = role();
        role.setDrive(true);
        role.setAdminScope("none");
        when(bindings.selectRolesOfUser(7L)).thenReturn(List.of(role));
        when(bindings.selectRoleIdsOfUser(7L)).thenReturn(List.of(role.getRoleId()));
        var mine = permissions.mine().getData();
        assertEquals(List.of("内部管理角色"), mine.roles());
        assertTrue(mine.drive());
        assertEquals("none", mine.adminScope());
    }

    @Test
    void invalidRoleLookupIdsCannotReachTheBindingMapper() {
        assertTrue(roles.rolesOf(null).isEmpty());
        assertTrue(roles.rolesOf(0L).isEmpty());
        assertTrue(roles.rolesOf(-1L).isEmpty());
        verifyNoInteractions(bindings);
    }

    private Map<String, Executable> adminReads() {
        Map<String, Executable> reads = new LinkedHashMap<>();
        reads.put("permission/list", permissions::adminList);
        reads.put("permission/roles", permissions::roles);
        reads.put("space/list", spaceController::adminList);
        reads.put("space/capacity", spaceController::capacity);
        reads.put("space/shared-members", spaceController::sharedMembers);
        reads.put("space/depts", spaceController::depts);
        return reads;
    }

    private void invalidate(String state) {
        switch (state) {
            case "disabled" -> actor.setStatus("1");
            case "deleted" -> actor.setDelFlag("2");
            case "missing" -> when(users.selectById(1L)).thenReturn(null);
            case "department-disabled" -> department.setStatus("1");
            case "department-deleted" -> department.setDelFlag("2");
            case "department-missing" -> when(depts.selectById(100L)).thenReturn(null);
            default -> fail("未知的主体失效状态");
        }
    }

    private static EsPermissionRole role() {
        EsPermissionRole role = new EsPermissionRole();
        role.setRoleId(9L);
        role.setRoleName("内部管理角色");
        role.setAdminScope("all");
        return role;
    }
}
