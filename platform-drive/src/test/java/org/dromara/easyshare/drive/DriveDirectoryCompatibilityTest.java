package org.dromara.easyshare.drive;

import org.dromara.common.core.exception.ServiceException;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.mapper.SysUserDeptMapper;
import org.dromara.easyshare.drive.service.*;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.MockedStatic;

import java.time.Instant;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

/** 保留上游旧目录接口的响应与递归语义，同时接入持久目录身份。 */
class DriveDirectoryCompatibilityTest {
    private static final String PREFIX = "users/1/";
    private DriveStorage storage;
    private DriveFileService files;
    private DriveRecycleService recycle;
    private DriveChangeLogService changes;
    private DriveController controller;
    private MockedStatic<LoginHelper> login;

    @BeforeEach
    void setUp() {
        storage = mock(DriveStorage.class);
        files = mock(DriveFileService.class);
        recycle = mock(DriveRecycleService.class);
        changes = mock(DriveChangeLogService.class);
        SpaceService spaces = mock(SpaceService.class);
        when(spaces.checkPersonalWritable(1L, 0L)).thenReturn(PREFIX);
        when(storage.listWithDirs(PREFIX)).thenReturn(List.of());
        when(files.list(eq("personal"), eq(1L), eq(1L), anyList())).thenAnswer(invocation ->
            new DriveFileService.Listing(invocation.getArgument(3), Map.of(), Map.of(), Map.of()));
        controller = new DriveController(storage, spaces, mock(SpaceUsageService.class), files,
            mock(FileRoleService.class), mock(DriveUploadService.class), mock(DriveMoveService.class), changes,
            mock(SysUserDeptMapper.class), mock(FileMarkService.class), mock(ShareService.class),
            recycle, mock(FileVersionService.class));
        login = mockStatic(LoginHelper.class);
        login.when(LoginHelper::getUserId).thenReturn(1L);
    }

    @AfterEach
    void tearDown() {
        login.close();
    }

    private EsFile directory() {
        EsFile row = new EsFile();
        row.setFileId(71L);
        row.setFilePath("docs");
        row.setSpaceType("personal");
        row.setOwnerId(1L);
        row.setFileSize(0L);
        row.setDirectory(true);
        row.setVersionId(1L);
        row.setState(EsFile.STATE_ACTIVE);
        return row;
    }

    /** 替身只补目录身份；仍检查控制器把实际旧 marker 传给安全迁移入口。 */
    private void listLegacyDirectory(boolean withChild) {
        DriveObject marker = new DriveObject("docs/", 0L, Instant.EPOCH);
        List<DriveObject> raw = withChild
            ? List.of(marker, new DriveObject("docs/file.txt", 3L, Instant.EPOCH))
            : List.of(marker);
        when(storage.listWithDirs(PREFIX)).thenReturn(raw);
        when(files.list(eq("personal"), eq(1L), eq(1L), anyList())).thenAnswer(invocation -> {
            assertEquals(raw, invocation.getArgument(3), "不能丢掉旧 marker 再委托迁移");
            List<DriveObject> typed = withChild
                ? List.of(new DriveObject("docs", 0L, Instant.EPOCH), raw.get(1))
                : List.of(new DriveObject("docs", 0L, Instant.EPOCH));
            return new DriveFileService.Listing(typed, Map.of("docs", 71L),
                Map.of("docs", directory()), Map.of("docs", 1L));
        });
    }

    private DriveRecycleService.Result recycled(boolean directory, String fileId, String path, int count) {
        return new DriveRecycleService.Result("batch-compat", "op-compat", fileId, path,
            directory, "recycled", count);
    }

    private void assertNoUnsafeDeletion() {
        verify(storage, never()).deleteAt(anyString(), anyString());
        verify(storage, never()).deleteDirectory(anyString(), anyString());
        verify(storage, never()).deleteDirectoryTree(anyString(), anyString());
        verify(storage, never()).copyAt(anyString(), anyString(), anyString());
        verify(files, never()).markRecycled(anyString(), anyLong(), anyString());
        verify(files, never()).recycleSubtree(anyString(), anyLong(), anyString());
        verifyNoInteractions(changes);
    }

    @Test
    void oldMkdirReturnsTrailingSlashButCreatesPersistentEntity() {
        when(files.createDirectory("personal", 1L, 1L, "docs")).thenReturn(directory());
        assertEquals("docs/", controller.mkdir(new DriveController.MkdirBo("docs", "personal"))
            .getData().path());
        verify(files).createDirectory("personal", 1L, 1L, "docs");
        verify(storage, never()).createDirectory(anyString(), anyString());
    }

    @Test
    void includeDirsKeepsLegacyPathShapeAndDefaultKeepsTypedIdentity() {
        listLegacyDirectory(false);
        var normal = controller.objects("personal", false).getData();
        var legacy = controller.objects("personal", true).getData();
        assertEquals("docs", normal.get(0).path());
        assertEquals("docs/", legacy.get(0).path());
        assertTrue(normal.get(0).directory());
        assertTrue(legacy.get(0).directory());
        assertEquals(71L, normal.get(0).fileId());
        assertEquals(1L, normal.get(0).versionId());
        assertEquals(normal.get(0).fileId(), legacy.get(0).fileId());
        assertEquals(normal.get(0).versionId(), legacy.get(0).versionId());
        assertEquals(normal.get(0).size(), legacy.get(0).size());
        verify(storage, times(2)).listWithDirs(PREFIX);
        verify(files, times(2)).list("personal", 1L, 1L,
            List.of(new DriveObject("docs/", 0L, Instant.EPOCH)));
        verifyNoInteractions(recycle);
    }

    @Test
    void recursiveDeleteAcceptsPersistentDirectoryIdentity() {
        listLegacyDirectory(true);
        var expected = recycled(true, "71", "docs", 2);
        when(recycle.recycle("personal", 1L, 1L, 71L, "docs/", null, null, true)).thenReturn(expected);
        var result = controller.delete(new DriveController.PathBo("docs/", "personal", 71L, true));
        assertEquals(200, result.getCode());
        assertSame(expected, result.getData());
        var order = inOrder(files, recycle);
        order.verify(files).list(eq("personal"), eq(1L), eq(1L), anyList());
        order.verify(recycle).recycle("personal", 1L, 1L, 71L, "docs/", null, null, true);
        assertNoUnsafeDeletion();
    }

    @Test
    void recursiveDeleteStillAcceptsLegacyMarkerWithoutIdentity() {
        listLegacyDirectory(true);
        var expected = recycled(true, "71", "docs", 2);
        when(recycle.recycle("personal", 1L, 1L, null, "docs/", null, null, true)).thenReturn(expected);
        var result = controller.delete(new DriveController.PathBo("docs/", "personal", null, true));
        assertSame(expected, result.getData());
        verify(files).list(eq("personal"), eq(1L), eq(1L), anyList());
        verify(recycle).recycle("personal", 1L, 1L, null, "docs/", null, null, true);
        assertNoUnsafeDeletion();
    }

    @Test
    void nonRecursivePersistentDirectoryWithChildIsRejected() {
        listLegacyDirectory(true);
        ServiceException nonEmpty = new ServiceException("目录非空，请使用明确递归删除");
        when(recycle.recycle("personal", 1L, 1L, 71L, "docs", null, null, false)).thenThrow(nonEmpty);
        var thrown = assertThrows(ServiceException.class,
            () -> controller.delete(new DriveController.PathBo("docs", "personal", 71L, false)));
        assertSame(nonEmpty, thrown);
        assertEquals("目录非空，请使用明确递归删除", thrown.getMessage());
        verify(recycle).recycle("personal", 1L, 1L, 71L, "docs", null, null, false);
        assertNoUnsafeDeletion();
    }

    @Test
    void trailingSlashCannotMakeFileIdentityARecursiveDirectory() {
        EsFile file = new EsFile();
        file.setFileId(72L);
        file.setFilePath("actual.txt");
        file.setDirectory(false);
        var actual = new DriveObject("actual.txt", 3L, Instant.EPOCH);
        when(storage.listWithDirs(PREFIX)).thenReturn(List.of(actual));
        when(files.list("personal", 1L, 1L, List.of(actual))).thenReturn(
            new DriveFileService.Listing(List.of(actual), Map.of("actual.txt", 72L),
                Map.of("actual.txt", file), Map.of("actual.txt", 1L)));
        var expected = recycled(false, "72", "actual.txt", 1);
        when(recycle.recycle("personal", 1L, 1L, 72L, "docs/", null, null, true)).thenReturn(expected);
        var result = controller.delete(new DriveController.PathBo("docs/", "personal", 72L, true)).getData();
        assertSame(expected, result);
        assertFalse(result.directory());
        assertEquals("actual.txt", result.path());
        assertEquals("72", result.fileId());
        assertEquals(1, result.memberCount());
        verify(recycle).recycle("personal", 1L, 1L, 72L, "docs/", null, null, true);
        assertNoUnsafeDeletion();
    }
}
