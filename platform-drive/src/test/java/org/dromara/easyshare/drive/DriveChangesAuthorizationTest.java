package org.dromara.easyshare.drive;

import org.dromara.common.core.domain.R;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.domain.EsFileChangeLog;
import org.dromara.easyshare.drive.domain.EsFileMember;
import org.dromara.easyshare.drive.mapper.EsFileMapper;
import org.dromara.easyshare.drive.mapper.EsFileMemberMapper;
import org.dromara.easyshare.drive.mapper.SysDeptRowMapper;
import org.dromara.easyshare.drive.mapper.SysUserDeptMapper;
import org.dromara.easyshare.drive.service.*;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
import org.mockito.MockedStatic;

import java.time.Instant;
import java.time.LocalDateTime;
import java.util.List;
import java.util.Map;
import java.util.function.Function;
import java.util.stream.Collectors;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

/** 增量授权与旧客户端重建协议：权限变化不能依赖文件版本或历史路径。 */
class DriveChangesAuthorizationTest {
    private DriveStorage storage;
    private DriveFileService files;
    private DriveChangeLogService changes;
    private SpaceService spaces;
    private EsFileMemberMapper members;
    private DriveController controller;
    private MockedStatic<LoginHelper> login;

    @BeforeEach
    void setUp() {
        storage = mock(DriveStorage.class);
        files = mock(DriveFileService.class);
        changes = mock(DriveChangeLogService.class);
        spaces = mock(SpaceService.class);
        members = mock(EsFileMemberMapper.class);
        var users = mock(SysUserDeptMapper.class);
        var roles = new FileRoleService(members, mock(EsFileMapper.class), spaces, users,
            mock(SysDeptRowMapper.class), mock(PermissionWriteService.class));
        when(spaces.checkSharedReadable(7L)).thenReturn("shared/");
        when(spaces.sharedPermissionOf(7L)).thenReturn("read");
        when(spaces.deptIdOf(7L)).thenReturn(10L);
        when(files.filterSharedVisible(anyList(), anyMap(), eq(7L))).thenAnswer(call ->
            roles.filterSharedVisible(call.getArgument(0), call.getArgument(1), 7L));
        when(changes.currentVersion(anyString(), anyLong())).thenReturn(10L);
        controller = new DriveController(storage, spaces, mock(SpaceUsageService.class), files,
            roles, mock(DriveUploadService.class), mock(DriveMoveService.class), changes,
            users, mock(FileMarkService.class), mock(ShareService.class),
            mock(DriveRecycleService.class), mock(FileVersionService.class));
        login = mockStatic(LoginHelper.class);
        login.when(LoginHelper::getUserId).thenReturn(7L);
    }

    @AfterEach
    void tearDown() {
        login.close();
    }

    @ParameterizedTest
    @ValueSource(strings = {"created", "updated", "deleted", "restored", "moved", "purged"})
    void sharedHistoryNeverExposesHiddenNamesOrDeletionTombstones(String event) {
        EsFileChangeLog hidden = event(11L, event, "机密-当前.txt", "机密-旧名称.txt");
        when(changes.changesAfter(eq("shared"), eq(0L), anyLong(), anyLong()))
            .thenReturn(new DriveChangeLogService.ChangePage(List.of(hidden), 11L, false, false));
        when(files.selectActiveById("shared", 0L, 71L))
            .thenReturn(file(71L, "机密-当前.txt", "20"));

        assertReset(controller.changes("shared", 10L), 10L);
        // 包括已彻底删除而无法重建 ACL 的日志：不读取历史身份，更不拼接大小或时间。
        verifyNoInteractions(changes, files, storage);
        verify(spaces).checkSharedReadable(7L);
    }

    @ParameterizedTest
    @ValueSource(strings = {"visibility", "member", "department"})
    void revocationWithoutFileChangesForcesOldCacheToBeReplaced(String cause) {
        EsFile secret = file(71L, "已缓存的机密.txt", "10");
        if ("member".equals(cause)) {
            secret.setVisibleDepts("20");
            EsFileMember grant = new EsFileMember();
            grant.setFileId(71L);
            grant.setRole("read");
            when(members.selectUserRowsOf(7L)).thenReturn(List.of(grant));
        }
        listing("shared", List.of(secret));
        var initial = controller.changes("shared", null).getData();
        assertEquals(List.of("已缓存的机密.txt"), initial.files().stream().map(DriveController.FileVo::path).toList());
        assertEquals(10L, initial.newestSpaceVersion());

        switch (cause) {
            case "visibility" -> secret.setVisibleDepts("20");
            case "member" -> when(members.selectUserRowsOf(7L)).thenReturn(List.of());
            case "department" -> when(spaces.deptIdOf(7L)).thenReturn(20L);
            default -> fail("未知的撤权场景");
        }
        // 文件版本完全不变；旧客户端通过失败码退全量，不能误认成成功的空增量。
        assertReset(controller.changes("shared", initial.newestSpaceVersion()), 10L);
        var replacement = controller.changes("shared", null).getData();
        assertTrue(replacement.files().isEmpty());
        assertNull(replacement.changes());
        assertEquals(10L, replacement.newestSpaceVersion());
        assertFalse(replacement.resetRequired());
        verify(changes, never()).changesAfter(anyString(), anyLong(), anyLong(), anyLong());
    }

    @Test
    void fullRebuildStillUsesActualFileRoleFilter() {
        listing("shared", List.of(file(71L, "公开.txt", null), file(72L, "隐藏.txt", "20")));
        var result = controller.changes("shared", null).getData();
        assertEquals(List.of("公开.txt"), result.files().stream().map(DriveController.FileVo::path).toList());
        assertEquals("download", result.files().getFirst().effectiveRole());
        assertNull(result.sinceVersion());
        assertNull(result.changes());
        assertFalse(result.hasMore());
        verify(changes).trim("shared", 0L, 10_000);
    }

    @Test
    void losingSpaceAccessCannotReadEventsOrFullStorage() {
        when(spaces.checkSharedReadable(7L)).thenThrow(new ServiceException("没有共享空间的访问权限"));
        assertThrows(ServiceException.class, () -> controller.changes("shared", 10L));
        assertThrows(ServiceException.class, () -> controller.changes("shared", null));
        verifyNoInteractions(changes, storage, files);
    }

    @Test
    void personalPageReturnsDeliveredCursorInsteadOfLatestSpaceVersion() {
        when(changes.currentVersion("personal", 7L)).thenReturn(1_000L);
        when(changes.changesAfter("personal", 7L, 10L, 1_000L)).thenReturn(
            new DriveChangeLogService.ChangePage(List.of(event(510L, "updated", "mine.txt", null)),
                510L, true, false));
        var result = controller.changes("personal", 10L).getData();
        assertEquals(510L, result.newestSpaceVersion());
        assertTrue(result.hasMore());
        assertFalse(result.resetRequired());
        assertNull(result.files());
        assertEquals(510L, result.changes().getFirst().spaceVersion());
        verifyNoInteractions(storage);
    }

    @ParameterizedTest
    @ValueSource(strings = {"deleted", "purged"})
    void personalDeletionKeepsSafeOwnerScopedTombstone(String event) {
        String path = "purged".equals(event) ? null : "mine.txt";
        var entry = event(10L, event, path, null);
        entry.setDirectory(true);
        when(changes.changesAfter("personal", 7L, 9L, 10L)).thenReturn(
            new DriveChangeLogService.ChangePage(List.of(entry), 10L, false, false));
        var result = controller.changes("personal", 9L).getData().changes().getFirst();
        assertEquals(event, result.event());
        assertEquals(path, result.path());
        assertNull(result.oldPath());
        assertEquals(71L, result.fileId());
        assertEquals(0L, result.size());
        assertNull(result.lastModified());
        assertTrue(result.directory());
        verify(files).selectActiveById("personal", 7L, 71L);
    }

    @Test
    void missingPersonalHistoryResetsWithoutSendingPartialChanges() {
        when(changes.changesAfter("personal", 7L, 5L, 10L)).thenReturn(
            new DriveChangeLogService.ChangePage(List.of(), 5L, false, true));
        assertReset(controller.changes("personal", 5L), 5L);
        verifyNoInteractions(files, storage);
    }

    @Test
    void fullListingDoesNotSkipAConcurrentChangeOrLegacyRegistration() {
        listing("personal", List.of());
        when(changes.currentVersion("personal", 7L)).thenReturn(10L, 11L);
        var initial = controller.changes("personal", null).getData();
        assertEquals(10L, initial.newestSpaceVersion());
        verify(changes, times(1)).currentVersion("personal", 7L);

        when(changes.changesAfter("personal", 7L, 10L, 11L)).thenReturn(
            new DriveChangeLogService.ChangePage(List.of(event(11L, "created", "concurrent.txt", null)),
                11L, false, false));
        var next = controller.changes("personal", initial.newestSpaceVersion()).getData();
        assertEquals("concurrent.txt", next.changes().getFirst().path());
        assertEquals(11L, next.newestSpaceVersion());
    }

    private static void assertReset(R<DriveController.ChangesVo> response, long since) {
        assertEquals(409, response.getCode());
        assertTrue(R.isError(response));
        assertTrue(response.getMsg().startsWith("DRIVE_SYNC_RESET_REQUIRED"));
        var data = response.getData();
        assertTrue(data.resetRequired());
        assertFalse(data.hasMore());
        assertEquals(since, data.newestSpaceVersion());
        assertEquals(since, data.sinceVersion());
        assertNull(data.files());
        assertNull(data.changes());
    }

    private void listing(String space, List<EsFile> rows) {
        List<DriveObject> objects = rows.stream()
            .map(row -> new DriveObject(row.getFilePath(), row.getFileSize(), Instant.EPOCH)).toList();
        Map<String, EsFile> byPath = rows.stream().collect(Collectors.toMap(EsFile::getFilePath, Function.identity()));
        Map<String, Long> ids = rows.stream().collect(Collectors.toMap(EsFile::getFilePath, EsFile::getFileId));
        when(storage.listWithDirs(anyString())).thenReturn(objects);
        when(files.list(eq(space), eq("shared".equals(space) ? 0L : 7L), eq(7L), anyList()))
            .thenReturn(new DriveFileService.Listing(objects, ids, byPath, Map.of()));
    }

    private static EsFile file(long id, String path, String depts) {
        EsFile row = new EsFile();
        row.setFileId(id);
        row.setSpaceType("shared");
        row.setOwnerId(0L);
        row.setState(EsFile.STATE_ACTIVE);
        row.setFilePath(path);
        row.setFileSize(91L);
        row.setContentMd5("private-content-hash");
        row.setCreateTime(LocalDateTime.of(2026, 11, 25, 12, 0));
        row.setVisibleDepts(depts);
        return row;
    }

    private static EsFileChangeLog event(long version, String event, String path, String oldPath) {
        EsFileChangeLog row = new EsFileChangeLog();
        row.setSpaceVersion(version);
        row.setEventType(event);
        row.setFileId(71L);
        row.setFilePath(path);
        row.setOldPath(oldPath);
        return row;
    }
}
