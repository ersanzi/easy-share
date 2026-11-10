package org.dromara.easyshare.drive;

import org.dromara.common.core.exception.ServiceException;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.service.*;
import org.dromara.easyshare.drive.mapper.SysUserDeptMapper;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.MockedStatic;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

/** 空文件、缺失秒传源与已登记未落地的重试；不接触真实数据库或对象。 */
class DrivePresignSafetyTest {
    private static final String MD5 = "d41d8cd98f00b204e9800998ecf8427e";
    private static final String PREFIX = "users/1/";
    private DriveStorage storage;
    private DriveFileService files;
    private FileVersionService versions;
    private DriveController controller;
    private MockedStatic<LoginHelper> login;

    @BeforeEach
    void setUp() {
        storage = mock(DriveStorage.class);
        files = mock(DriveFileService.class);
        versions = mock(FileVersionService.class);
        SpaceService spaces = mock(SpaceService.class);
        when(spaces.checkPersonalWritable(1L, 0L)).thenReturn(PREFIX);
        when(storage.presignPutAt(PREFIX, "empty.txt")).thenReturn("https://store.invalid/empty");
        controller = new DriveController(storage, spaces, mock(SpaceUsageService.class), files,
            mock(FileRoleService.class), mock(DriveUploadService.class), mock(DriveMoveService.class), mock(DriveChangeLogService.class),
            mock(SysUserDeptMapper.class), mock(FileMarkService.class), mock(ShareService.class), versions);
        login = mockStatic(LoginHelper.class);
        login.when(LoginHelper::getUserId).thenReturn(1L);
    }

    @AfterEach
    void tearDown() {
        login.close();
    }

    private DriveController.PutBo request() {
        return new DriveController.PutBo("empty.txt", "personal", 0L, null, 0L, MD5);
    }

    private EsFile row(String path) {
        EsFile row = new EsFile();
        row.setFileId(71L);
        row.setFilePath(path);
        row.setSpaceType("personal");
        row.setOwnerId(1L);
        row.setUploadBy(1L);
        row.setFileSize(0L);
        row.setVersionId(1L);
        row.setState(EsFile.STATE_ACTIVE);
        row.setContentMd5(MD5);
        row.setMd5Reliable(DriveFileService.MD5_RELIABLE_CLIENT);
        row.setDirectory(false);
        return row;
    }

    @Test
    void missingDedupSourceFallsBackToUploadWithoutCopy() {
        when(files.findByContentHash("personal", 1L, MD5)).thenReturn(row("ghost.txt"));
        when(storage.metadataAt(PREFIX, "ghost.txt")).thenReturn(null);
        var result = controller.presignPut(request()).getData();
        assertFalse(result.deduped());
        assertEquals("https://store.invalid/empty", result.url());
        verify(files).registerOnPresign("personal", 1L, 1L, "empty.txt", 0L, 0L, MD5);
        verify(storage, never()).copyAt(anyString(), anyString(), anyString());
    }

    @Test
    void verifiedEmptyObjectCanDedup() {
        when(files.findByContentHash("personal", 1L, MD5)).thenReturn(row("real-empty.txt"));
        when(storage.metadataAt(PREFIX, "real-empty.txt"))
            .thenReturn(new DriveStorage.ObjectMetadata(0, MD5));
        var result = controller.presignPut(request()).getData();
        assertTrue(result.deduped());
        assertNull(result.url());
        var order = inOrder(files, storage);
        order.verify(files).assertVersion("personal", 1L, "empty.txt", 0L);
        order.verify(storage).metadataAt(PREFIX, "real-empty.txt");
        order.verify(files).registerOnPresign("personal", 1L, 1L, "empty.txt", 0L, 0L, MD5);
        order.verify(storage).copyAt(PREFIX, "real-empty.txt", "empty.txt");
    }

    @Test
    void staleHashOrSizeCannotDedup() {
        when(files.findByContentHash("personal", 1L, MD5)).thenReturn(row("changed.txt"));
        for (var metadata : new DriveStorage.ObjectMetadata[]{
            new DriveStorage.ObjectMetadata(1, MD5),
            new DriveStorage.ObjectMetadata(0, "0123456789abcdef0123456789abcdef"),
            new DriveStorage.ObjectMetadata(0, MD5 + "-2")}) {
            when(storage.metadataAt(PREFIX, "changed.txt")).thenReturn(metadata);
            assertFalse(controller.presignPut(request()).getData().deduped());
        }
        verify(storage, never()).copyAt(anyString(), anyString(), anyString());
    }

    @Test
    void metadataPermissionFailureDoesNotRegisterOrSign() {
        when(files.findByContentHash("personal", 1L, MD5)).thenReturn(row("forbidden.txt"));
        when(storage.metadataAt(PREFIX, "forbidden.txt")).thenThrow(new ServiceException("denied"));
        assertThrows(ServiceException.class, () -> controller.presignPut(request()));
        verify(files, never()).registerOnPresign(anyString(), anyLong(), anyLong(), anyString(), anyLong(), any(), any());
        verify(storage, never()).presignPutAt(anyString(), anyString());
    }

    @Test
    void sameUnfinishedUploadReusesVersionWithoutMissingSnapshot() {
        when(files.lockActiveForPresign("personal", 1L, "empty.txt")).thenReturn(row("empty.txt"));
        when(storage.metadataAt(PREFIX, "empty.txt")).thenReturn(null);
        assertFalse(controller.presignPut(request()).getData().deduped());
        verify(files).assertVersion("personal", 1L, "empty.txt", 1L);
        verify(files, never()).registerOnPresign(anyString(), anyLong(), anyLong(), anyString(), anyLong(), any(), any());
        verifyNoInteractions(versions);
        verify(storage).presignPutAt(PREFIX, "empty.txt");
    }

    @Test
    void existingBytesAreNeverTreatedAsUnfinishedRetry() {
        when(files.lockActiveForPresign("personal", 1L, "empty.txt")).thenReturn(row("empty.txt"));
        when(storage.metadataAt(PREFIX, "empty.txt")).thenReturn(new DriveStorage.ObjectMetadata(0, MD5));
        doThrow(new ServiceException("version conflict")).when(files).assertVersion("personal", 1L, "empty.txt", 0L);
        assertThrows(ServiceException.class, () -> controller.presignPut(request()));
        verify(storage, never()).presignPutAt(anyString(), anyString());
        verifyNoInteractions(versions);
    }

    @Test
    void unfinishedRetryRequiresEveryIdentityField() {
        assertTrue(DriveController.sameUnfinishedUpload(row("empty.txt"), 1L, 0L, 0L, MD5));
        assertFalse(DriveController.sameUnfinishedUpload(row("empty.txt"), 2L, 0L, 0L, MD5));
        assertFalse(DriveController.sameUnfinishedUpload(row("empty.txt"), 1L, 1L, 0L, MD5));
        assertFalse(DriveController.sameUnfinishedUpload(row("empty.txt"), 1L, 0L, 1L, MD5));
        assertFalse(DriveController.sameUnfinishedUpload(row("empty.txt"), 1L, 0L, null, MD5));
        assertFalse(DriveController.sameUnfinishedUpload(row("empty.txt"), 1L, 0L, -1L, MD5));
        assertFalse(DriveController.sameUnfinishedUpload(row("empty.txt"), 1L, 0L, Long.MAX_VALUE, MD5));
        assertFalse(DriveController.sameUnfinishedUpload(row("empty.txt"), 1L, 0L, 0L, ""));
        assertFalse(DriveController.sameUnfinishedUpload(row("empty.txt"), 1L, 0L, 0L, "0123456789abcdef0123456789abcdef"));
        EsFile row = row("empty.txt");
        row.setDirectory(true);
        assertFalse(DriveController.sameUnfinishedUpload(row, 1L, 0L, 0L, MD5));
        row.setDirectory(false);
        row.setState(EsFile.STATE_RECYCLED);
        assertFalse(DriveController.sameUnfinishedUpload(row, 1L, 0L, 0L, MD5));
        row.setState(EsFile.STATE_ACTIVE);
        row.setMd5Reliable(null);
        assertFalse(DriveController.sameUnfinishedUpload(row, 1L, 0L, 0L, MD5));
    }

    @Test
    void copyFailurePropagatesAndEndpointUsesTransactionRollback() throws Exception {
        when(files.findByContentHash("personal", 1L, MD5)).thenReturn(row("real-empty.txt"));
        when(storage.metadataAt(PREFIX, "real-empty.txt")).thenReturn(new DriveStorage.ObjectMetadata(0, MD5));
        doThrow(new ServiceException("copy failed")).when(storage).copyAt(PREFIX, "real-empty.txt", "empty.txt");
        assertThrows(ServiceException.class, () -> controller.presignPut(request()));
        var transaction = DriveController.class.getMethod("presignPut", DriveController.PutBo.class)
            .getAnnotation(org.springframework.transaction.annotation.Transactional.class);
        assertNotNull(transaction);
        assertArrayEquals(new Class<?>[]{Exception.class}, transaction.rollbackFor());
        verify(storage, never()).presignPutAt(anyString(), anyString());
    }
}
