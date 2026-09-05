package org.dromara.easyshare.drive;

import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.domain.EsSpaceMember;
import org.dromara.easyshare.drive.domain.SysUserDept;
import org.dromara.easyshare.drive.service.CapacityService;
import org.dromara.easyshare.drive.mapper.EsSpaceMapper;
import org.dromara.easyshare.drive.mapper.EsSpaceMemberMapper;
import org.dromara.easyshare.drive.mapper.SysDeptRowMapper;
import org.dromara.easyshare.drive.mapper.SysUserDeptMapper;
import org.dromara.easyshare.drive.service.SpaceService;
import org.dromara.easyshare.drive.service.SpaceUsageService;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

/**
 * 部门级权限（片 1）测试：生效权限合并（个人行 ∨ 部门行取宽）与授权主体泛化。
 *
 * @author EasyShare
 */
class SpaceServiceDeptTest {

    private EsSpaceMemberMapper memberMapper;
    private SysUserDeptMapper userMapper;
    private SpaceService service;

    @BeforeEach
    void setUp() {
        EsSpaceMapper spaceMapper = mock(EsSpaceMapper.class);
        memberMapper = mock(EsSpaceMemberMapper.class);
        SpaceUsageService usageService = mock(SpaceUsageService.class);
        CapacityService capacityService = mock(CapacityService.class);
        userMapper = mock(SysUserDeptMapper.class);
        SysDeptRowMapper deptMapper = mock(SysDeptRowMapper.class);
        service = new SpaceService(spaceMapper, memberMapper, usageService, capacityService,
            userMapper, deptMapper);
    }

    private static EsSpaceMember member(String type, long memberId, String permission) {
        EsSpaceMember member = new EsSpaceMember();
        member.setSpaceId(1L);
        member.setMemberType(type);
        member.setUserId(memberId);
        member.setPermission(permission);
        return member;
    }

    private void userDept(long userId, long deptId) {
        SysUserDept user = new SysUserDept();
        user.setUserId(userId);
        user.setDeptId(deptId);
        when(userMapper.selectById(userId)).thenReturn(user);
    }

    @Test
    void effectivePermissionTakesWidestOfUserAndDept() {
        // 个人行 read + 部门行 write → write（取宽）
        when(memberMapper.selectOne(any())).thenReturn(
            member(EsSpaceMember.TYPE_USER, 1L, EsSpaceMember.PERM_READ),
            member(EsSpaceMember.TYPE_DEPT, 5L, EsSpaceMember.PERM_WRITE));
        userDept(1L, 5L);
        assertEquals("write", service.sharedPermissionOf(1L));

        // 个人行 write + 部门行 read → write（个人授权不被部门压窄）
        when(memberMapper.selectOne(any())).thenReturn(
            member(EsSpaceMember.TYPE_USER, 1L, EsSpaceMember.PERM_WRITE),
            member(EsSpaceMember.TYPE_DEPT, 5L, EsSpaceMember.PERM_READ));
        assertEquals("write", service.sharedPermissionOf(1L));

        // 个人行 read + 无部门行 → read
        when(memberMapper.selectOne(any())).thenReturn(
            member(EsSpaceMember.TYPE_USER, 1L, EsSpaceMember.PERM_READ), null);
        userDept(1L, 0L);
        assertEquals("read", service.sharedPermissionOf(1L));
    }

    @Test
    void deptAuthorizationAloneGrantsAccess() {
        // 无个人行、部门行 read → 生效 read（部门授权独立可用）
        when(memberMapper.selectOne(any())).thenReturn(
            null, member(EsSpaceMember.TYPE_DEPT, 5L, EsSpaceMember.PERM_READ));
        userDept(1L, 5L);
        assertEquals("read", service.sharedPermissionOf(1L));
    }

    @Test
    void noRowsAndNoDeptMeansUnauthorized() {
        when(memberMapper.selectOne(any())).thenReturn(null, null);
        when(userMapper.selectById(1L)).thenReturn(null);
        assertNull(service.sharedPermissionOf(1L));
    }

    @Test
    void grantSharedToInsertsTypedRow() {
        service.grantSharedTo(EsSpaceMember.TYPE_DEPT, 5L, EsSpaceMember.PERM_WRITE);

        ArgumentCaptor<EsSpaceMember> captor = ArgumentCaptor.forClass(EsSpaceMember.class);
        verify(memberMapper).insert(captor.capture());
        assertEquals(EsSpaceMember.TYPE_DEPT, captor.getValue().getMemberType());
        assertEquals(5L, captor.getValue().getUserId());
        assertEquals("write", captor.getValue().getPermission());
    }

    @Test
    void grantSharedToRejectsUnknownType() {
        ServiceException ex = assertThrows(ServiceException.class,
            () -> service.grantSharedTo("group", 5L, "read"));
        assertEquals("授权主体类型非法", ex.getMessage());
    }
}
