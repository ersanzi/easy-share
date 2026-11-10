package org.dromara.easyshare.drive;

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
    private DriveStorage storage;
    private DriveFileService files;
    private DriveChangeLogService changes;
    private DriveController controller;
    private MockedStatic<LoginHelper> login;

    @BeforeEach
    void setUp() {
        storage = mock(DriveStorage.class);
        files = mock(DriveFileService.class);
        changes = mock(DriveChangeLogService.class);
        SpaceService spaces = mock(SpaceService.class);
        when(spaces.checkPersonalWritable(1L, 0L)).thenReturn("users/1/");
        controller = new DriveController(storage, spaces, mock(SpaceUsageService.class), files,
            mock(FileRoleService.class), mock(DriveUploadService.class), mock(DriveMoveService.class), changes,
            mock(SysUserDeptMapper.class), mock(FileMarkService.class), mock(ShareService.class),
            mock(FileVersionService.class));
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
        row.setDirectory(true);
        row.setVersionId(1L);
        return row;
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
        DriveObject object = new DriveObject("docs", 0L, Instant.EPOCH);
        when(files.list(eq("personal"), eq(1L), eq(1L), anyList())).thenReturn(
            new DriveFileService.Listing(List.of(object), Map.of("docs", 71L),
                Map.of("docs", directory()), Map.of("docs", 1L)));
        var normal = controller.objects("personal", false).getData();
        var legacy = controller.objects("personal", true).getData();
        assertEquals("docs", normal.get(0).path());
        assertEquals("docs/", legacy.get(0).path());
        assertTrue(normal.get(0).directory());
        assertEquals(71L, normal.get(0).fileId());
        assertEquals(normal.get(0).fileId(), legacy.get(0).fileId());
        verify(storage, times(2)).listWithDirs("users/1/");
    }

    @Test
    void recursiveDeleteAcceptsPersistentDirectoryIdentity() {
        when(files.resolveDeletePath("personal", 1L, 71L, "docs")).thenReturn("docs");
        when(files.activeAt("personal", 1L, "docs")).thenReturn(directory());
        controller.delete(new DriveController.PathBo("docs/", "personal", 71L, true));
        var order = inOrder(files, storage);
        order.verify(files).recycleSubtree("personal", 1L, "docs");
        order.verify(storage).deleteDirectoryTree("users/1/", "docs");
        verify(files, never()).markRecycled(anyString(), anyLong(), anyString());
        verify(storage, never()).deleteAt(anyString(), anyString());
    }

    @Test
    void recursiveDeleteStillAcceptsLegacyMarkerWithoutIdentity() {
        when(files.resolveDeletePath("personal", 1L, null, "docs")).thenReturn("docs");
        controller.delete(new DriveController.PathBo("docs/", "personal", null, true));
        verify(files).recycleSubtree("personal", 1L, "docs");
        verify(storage).deleteDirectoryTree("users/1/", "docs");
    }

    @Test
    void nonRecursivePersistentDirectoryWithChildIsRejected() {
        when(files.resolveDeletePath("personal", 1L, 71L, "docs")).thenReturn("docs");
        when(files.activeAt("personal", 1L, "docs")).thenReturn(directory());
        when(storage.listAt("users/1/")).thenReturn(
            List.of(new DriveObject("docs/file.txt", 3L, Instant.EPOCH)));
        assertThrows(org.dromara.common.core.exception.ServiceException.class,
            () -> controller.delete(new DriveController.PathBo("docs", "personal", 71L, false)));
        verify(files, never()).markRecycled(anyString(), anyLong(), anyString());
        verify(storage, never()).deleteDirectory(anyString(), anyString());
    }

    @Test
    void trailingSlashCannotMakeFileIdentityARecursiveDirectory() {
        EsFile file = new EsFile();
        file.setDirectory(false);
        when(files.resolveDeletePath("personal", 1L, 72L, "docs")).thenReturn("actual.txt");
        when(files.activeAt("personal", 1L, "actual.txt")).thenReturn(file);
        when(files.markRecycled("personal", 1L, "actual.txt")).thenReturn(file);
        controller.delete(new DriveController.PathBo("docs/", "personal", 72L, true));
        verify(files).markRecycled("personal", 1L, "actual.txt");
        verify(files, never()).recycleSubtree(anyString(), anyLong(), anyString());
        verify(storage, never()).deleteDirectoryTree(anyString(), anyString());
    }
}
