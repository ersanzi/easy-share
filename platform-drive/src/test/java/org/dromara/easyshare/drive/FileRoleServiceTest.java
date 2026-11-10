package org.dromara.easyshare.drive;

import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.domain.EsFileMember;
import org.dromara.easyshare.drive.domain.EsSpaceMember;
import org.dromara.easyshare.drive.domain.SysDeptRow;
import org.dromara.easyshare.drive.domain.SysUserDept;
import org.dromara.easyshare.drive.mapper.EsFileMapper;
import org.dromara.easyshare.drive.mapper.EsFileMemberMapper;
import org.dromara.easyshare.drive.mapper.SysDeptRowMapper;
import org.dromara.easyshare.drive.mapper.SysUserDeptMapper;
import org.dromara.easyshare.drive.service.FileRoleService;
import org.dromara.easyshare.drive.service.SpaceService;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;

import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyLong;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

/**
 * 文档级成员角色测试：四档梯子（上传者/成员行/visible_depts/空间默认）、
 * 下载与授权门槛、护栏（不能改自己的角色、操作者既有行保留）。
 *
 * @author EasyShare
 */
class FileRoleServiceTest {

    private EsFileMemberMapper memberMapper;
    private EsFileMapper fileMapper;
    private SpaceService spaceService;
    private SysUserDeptMapper sysUserDeptMapper;
    private SysDeptRowMapper sysDeptRowMapper;
    private FileRoleService service;

    /** 共享空间文件：上传者 1，无可见性限制 */
    private EsFile file(long uploadBy, String visibleDepts) {
        EsFile row = new EsFile();
        row.setFileId(100L);
        row.setSpaceType(EsFile.TYPE_SHARED);
        row.setOwnerId(EsFile.SHARED_OWNER);
        row.setUploadBy(uploadBy);
        row.setVisibleDepts(visibleDepts);
        row.setState(EsFile.STATE_ACTIVE);
        return row;
    }

    @BeforeEach
    void setUp() {
        memberMapper = mock(EsFileMemberMapper.class);
        fileMapper = mock(EsFileMapper.class);
        spaceService = mock(SpaceService.class);
        sysUserDeptMapper = mock(SysUserDeptMapper.class);
        sysDeptRowMapper = mock(SysDeptRowMapper.class);
        service = new FileRoleService(memberMapper, fileMapper, spaceService,
            sysUserDeptMapper, sysDeptRowMapper);
    }

    private void viewerContext(Long userId, Long deptId, String spacePermission) {
        when(spaceService.deptIdOf(userId)).thenReturn(deptId);
        when(spaceService.sharedPermissionOf(userId)).thenReturn(spacePermission);
    }

    @Test
    void uploaderIsAlwaysManage() {
        // 上传者即使被显式行降档、即使被 visible_depts 挡在门外，仍是 manage
        when(memberMapper.selectRole(100L, "user", 1L)).thenReturn(FileRoleService.ROLE_READ);
        assertEquals(FileRoleService.ROLE_MANAGE,
            service.effective(file(1L, "9"), 1L));
    }

    @Test
    void legacyRowWithoutUploaderFallsToLadder() {
        // 存量补账行 uploadBy=0：任何人都不算上传者，按空间权限取默认
        assertEquals(FileRoleService.ROLE_MANAGE,
            FileRoleService.ladder(file(0L, null), 7L, 10L, EsSpaceMember.PERM_WRITE, null, null));
    }

    @Test
    void memberRowOverridesDefaults() {
        // 空间 write 成员默认 manage；显式行给 read 档 → 收窄生效
        viewerContext(7L, 10L, EsSpaceMember.PERM_WRITE);
        when(memberMapper.selectRole(100L, "user", 7L)).thenReturn(FileRoleService.ROLE_READ);
        assertEquals(FileRoleService.ROLE_READ, service.effective(file(1L, null), 7L));
    }

    @Test
    void userAndDeptRowsTakeHigher() {
        // 用户行 read + 部门行 edit → 取高 edit
        assertEquals(FileRoleService.ROLE_EDIT,
            FileRoleService.ladder(file(1L, null), 7L, 10L, null,
                FileRoleService.ROLE_READ, FileRoleService.ROLE_EDIT));
    }

    @Test
    void restrictedFileDeptHitIsDownload() {
        // visible_depts 命中 → download（今天语义：可见即可下载）；未命中且无行 → 不可见
        EsFile restricted = file(1L, "10,20");
        assertEquals(FileRoleService.ROLE_DOWNLOAD,
            FileRoleService.ladder(restricted, 7L, 10L, EsSpaceMember.PERM_WRITE, null, null));
        assertNull(FileRoleService.ladder(restricted, 7L, 30L, EsSpaceMember.PERM_WRITE, null, null));
    }

    @Test
    void memberRowGrantsVisibilityOnRestrictedFile() {
        // 部门不在名单，但显式给了 read 档 → 可见（仅预览）
        assertEquals(FileRoleService.ROLE_READ,
            FileRoleService.ladder(file(1L, "20"), 7L, 30L, null, FileRoleService.ROLE_READ, null));
    }

    @Test
    void unrestrictedFileDefaultsPreserveTodayBehavior() {
        // 未配角色：write 成员 manage（今天可删可改名）、read 成员 download、非成员不可见
        EsFile row = file(1L, null);
        assertEquals(FileRoleService.ROLE_MANAGE,
            FileRoleService.ladder(row, 7L, 10L, EsSpaceMember.PERM_WRITE, null, null));
        assertEquals(FileRoleService.ROLE_DOWNLOAD,
            FileRoleService.ladder(row, 7L, 10L, EsSpaceMember.PERM_READ, null, null));
        assertNull(FileRoleService.ladder(row, 7L, 10L, null, null, null));
    }

    @Test
    void readRoleCannotDownload() {
        viewerContext(7L, null, EsSpaceMember.PERM_READ);
        when(memberMapper.selectRole(100L, "user", 7L)).thenReturn(FileRoleService.ROLE_READ);
        ServiceException e = assertThrows(ServiceException.class,
            () -> service.assertDownloadable(file(1L, null), 7L));
        assertTrue(e.getMessage().contains("仅可在线预览"));
    }

    @Test
    void invisibleViewerCannotDownload() {
        viewerContext(7L, null, null);
        ServiceException e = assertThrows(ServiceException.class,
            () -> service.assertDownloadable(file(1L, null), 7L));
        assertTrue(e.getMessage().contains("无权访问"));
    }

    @Test
    void replaceMembersRejectsSelfRow() {
        when(fileMapper.selectById(100L)).thenReturn(file(1L, null));
        viewerContext(7L, null, EsSpaceMember.PERM_WRITE);
        EsFileMember self = member("user", 7L, FileRoleService.ROLE_READ);
        ServiceException e = assertThrows(ServiceException.class,
            () -> service.replaceMembers(100L, List.of(self), 7L));
        assertTrue(e.getMessage().contains("不能修改自己的角色"));
        verify(memberMapper, never()).deleteByFile(anyLong());
    }

    @Test
    void replaceMembersKeepsOperatorRow() {
        // 操作者自己有显式 manage 行（被授权的管理员）：整组替换后自己的行原样保留
        when(fileMapper.selectById(100L)).thenReturn(file(1L, null));
        viewerContext(7L, null, EsSpaceMember.PERM_WRITE);
        when(memberMapper.selectRole(100L, "user", 7L)).thenReturn(FileRoleService.ROLE_MANAGE);
        SysUserDept user = new SysUserDept();
        user.setUserId(8L);
        user.setDelFlag("0");
        when(sysUserDeptMapper.selectById(8L)).thenReturn(user);

        service.replaceMembers(100L, List.of(member("user", 8L, FileRoleService.ROLE_READ)), 7L);

        verify(memberMapper).deleteByFile(100L);
        verify(memberMapper).insertRow(100L, "user", 8L, FileRoleService.ROLE_READ);
        // 操作者既有行回插
        verify(memberMapper).insertRow(100L, "user", 7L, FileRoleService.ROLE_MANAGE);
    }

    @Test
    void replaceMembersValidatesRoleAndSubject() {
        when(fileMapper.selectById(100L)).thenReturn(file(1L, null));
        viewerContext(7L, null, EsSpaceMember.PERM_WRITE);

        assertThrows(ServiceException.class,
            () -> service.replaceMembers(100L, List.of(member("user", 8L, "owner")), 7L));
        assertThrows(ServiceException.class,
            () -> service.replaceMembers(100L, List.of(member("group", 8L, "read")), 7L));

        // 账号已删除 → 拒绝
        SysUserDept gone = new SysUserDept();
        gone.setUserId(8L);
        gone.setDelFlag("2");
        when(sysUserDeptMapper.selectById(8L)).thenReturn(gone);
        assertThrows(ServiceException.class,
            () -> service.replaceMembers(100L, List.of(member("user", 8L, "read")), 7L));
    }

    @Test
    void memberEndpointsRequireManage() {
        // 非 manage（read 档成员）调授权端点 → 拒绝
        when(fileMapper.selectById(100L)).thenReturn(file(1L, null));
        viewerContext(7L, null, EsSpaceMember.PERM_READ);
        assertThrows(ServiceException.class, () -> service.membersOf(100L, 7L));
        assertThrows(ServiceException.class,
            () -> service.removeMember(100L, "user", 8L, 7L));
    }

    @Test
    void nonManageCannotDeleteOrRename() {
        viewerContext(7L, null, EsSpaceMember.PERM_WRITE);
        when(memberMapper.selectRole(100L, "user", 7L)).thenReturn(FileRoleService.ROLE_EDIT);
        EsFile row = file(1L, null);
        // edit 可改名
        service.assertEditable(row, 7L);
        // 不可删除
        assertThrows(ServiceException.class, () -> service.assertManage(row, 7L));
    }

    @Test
    void purgeCleansMemberRows() {
        service.purgeWithFile(100L);
        verify(memberMapper).deleteByFile(100L);
    }

    private static EsFileMember member(String type, long id, String role) {
        EsFileMember member = new EsFileMember();
        member.setFileId(100L);
        member.setMemberType(type);
        member.setMemberId(id);
        member.setRole(role);
        return member;
    }
}
