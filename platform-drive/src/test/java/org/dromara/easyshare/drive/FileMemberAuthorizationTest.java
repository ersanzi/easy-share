package org.dromara.easyshare.drive;

import org.dromara.easyshare.drive.domain.*;
import org.dromara.easyshare.drive.mapper.*;
import org.dromara.easyshare.drive.service.*;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import java.util.List;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;
import static org.mockito.ArgumentMatchers.*;

/** 从文件授权边界验证真实权限链；只替换数据库访问。 */
class FileMemberAuthorizationTest {
    private EsFileMapper files;
    private EsFileMemberMapper members;
    private SysUserDeptMapper users;
    private SysDeptRowMapper depts;
    private EsSpaceMemberMapper spaceMembers;
    private FileRoleService roles;

    @BeforeEach
    void setup() {
        files = mock(EsFileMapper.class);
        members = mock(EsFileMemberMapper.class);
        users = mock(SysUserDeptMapper.class);
        depts = mock(SysDeptRowMapper.class);
        spaceMembers = mock(EsSpaceMemberMapper.class);
        EsUserRoleMapper userRoles = mock(EsUserRoleMapper.class);
        when(userRoles.selectRolesOfUser(anyLong())).thenReturn(List.of());
        FeaturePermissionService features = new FeaturePermissionService(mock(EsUserPermissionMapper.class), userRoles);
        SpaceService spaces = new SpaceService(mock(EsSpaceMapper.class), spaceMembers, null, null, users, depts, features);
        roles = new FileRoleService(members, files, spaces, users, depts);
        EsFile file = new EsFile();
        file.setFileId(100L); file.setSpaceType("shared"); file.setState("active"); file.setUploadBy(7L);
        when(files.selectById(100L)).thenReturn(file);
    }

    @Test
    void candidateSearchChecksManageBeforeQueryingPeople() {
        assertThrows(org.dromara.common.core.exception.ServiceException.class,
            () -> roles.candidatesOf(100L, 8L, "张", 20));
        verify(users, never()).searchSharedCandidates(anyString(), anyInt());
    }

    @Test
    void candidatesReturnOnlyEffectiveSharedUsersWithStringIds() {
        SysUserDept user = new SysUserDept();
        user.setUserId(9007199254740993L); user.setUserName("zhang"); user.setNickName("张三");
        user.setStatus("0"); user.setDelFlag("0");
        when(users.searchSharedCandidates("张", 50)).thenReturn(List.of(user));
        when(users.selectById(user.getUserId())).thenReturn(user);
        EsSpaceMember grant = new EsSpaceMember(); grant.setPermission("read");
        when(spaceMembers.selectOne(any(com.baomidou.mybatisplus.core.conditions.Wrapper.class))).thenReturn(grant);
        var result = roles.candidatesOf(100L, 7L, "张", 500);
        assertEquals("9007199254740993", result.getFirst().memberId());
        assertTrue(result.getFirst().grantable());
        assertThrows(org.dromara.common.core.exception.ServiceException.class,
            () -> roles.candidatesOf(100L, 7L, "", 20));
    }

    @Test
    void uploaderCannotGrantAnActiveEmployeeOutsideSharedSpace() {
        SysUserDept outsider = new SysUserDept();
        outsider.setUserId(8L); outsider.setStatus("0"); outsider.setDelFlag("0");
        when(users.selectById(8L)).thenReturn(outsider);
        EsFileMember row = new EsFileMember();
        row.setFileId(100L); row.setMemberType("user"); row.setMemberId(8L); row.setRole("manage");
        assertThrows(org.dromara.common.core.exception.ServiceException.class,
            () -> roles.replaceMembers(100L, List.of(row), 7L));
        verify(members, never()).deleteByFile(anyLong());
    }
}
