package org.dromara.easyshare.drive;

import cn.dev33.satoken.annotation.SaCheckLogin;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.service.DriveFileService;
import org.dromara.easyshare.drive.service.SpaceService;
import org.dromara.easyshare.drive.service.SpaceUsageService;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.MockedStatic;
import org.springframework.transaction.annotation.Transactional;
import org.springframework.web.bind.annotation.PostMapping;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

/** 隔离的 HEAD/身份核验，不访问真实对象、数据库或生产接口。 */
class DriveUploadCommitControllerTest {
    private static final String MD5 = "900150983cd24fb0d6963f7d28e17f72";
    private static final String PREFIX = "users/1/";
    private DriveStorage storage;
    private DriveFileService files;
    private SpaceService spaces;
    private SpaceUsageService usage;
    private DriveUploadCommitController controller;
    private MockedStatic<LoginHelper> login;
    private EsFile row;

    @BeforeEach
    void setUp() {
        storage = mock(DriveStorage.class);
        files = mock(DriveFileService.class);
        spaces = mock(SpaceService.class);
        usage = mock(SpaceUsageService.class);
        controller = new DriveUploadCommitController(storage, spaces, usage, files);
        login = mockStatic(LoginHelper.class);
        login.when(LoginHelper::getUserId).thenReturn(1L);
        when(spaces.checkPersonalWritable(1L, 0L)).thenReturn(PREFIX);
        row = new EsFile();
        row.setFileId(2110952103974817794L);
        row.setVersionId(2L);
        row.setSpaceType("personal");
        row.setOwnerId(1L);
        row.setUploadBy(1L);
        row.setFilePath("a.txt");
        row.setState(EsFile.STATE_ACTIVE);
        row.setContentMd5(MD5);
        when(files.lockActiveForPresign("personal", 1L, "a.txt")).thenReturn(row);
        when(storage.metadataAt(PREFIX, "a.txt")).thenReturn(new DriveStorage.ObjectMetadata(3, MD5));
    }

    @AfterEach
    void tearDown() { login.close(); }

    private DriveUploadCommitController.CommitBo request() {
        return new DriveUploadCommitController.CommitBo("a.txt", null, 3L, MD5, row.getFileId(), 2L);
    }

    @Test
    void verifiesHeadAndInvalidatesWithoutRecountingOrAdvancingVersion() {
        var result = controller.commit(request()).getData();
        assertTrue(result.verified());
        assertEquals("2110952103974817794", result.fileId());
        assertEquals(2L, result.version());
        assertEquals(3L, result.size());
        var order = inOrder(spaces, files, storage, usage);
        order.verify(spaces).checkPersonalWritable(1L, 0L);
        order.verify(files).lockActiveForPresign("personal", 1L, "a.txt");
        order.verify(storage).metadataAt(PREFIX, "a.txt");
        order.verify(usage).invalidate(PREFIX);
        verifyNoMoreInteractions(spaces, files, storage, usage);
    }

    @Test
    void missingWrongSizedAndWrongHashedObjectsCannotReportSuccess() {
        for (var metadata : new DriveStorage.ObjectMetadata[]{null,
            new DriveStorage.ObjectMetadata(4, MD5),
            new DriveStorage.ObjectMetadata(3, "d41d8cd98f00b204e9800998ecf8427e"),
            new DriveStorage.ObjectMetadata(3, null), new DriveStorage.ObjectMetadata(3, MD5 + "-0")}) {
            when(storage.metadataAt(PREFIX, "a.txt")).thenReturn(metadata);
            assertThrows(ServiceException.class, () -> controller.commit(request()));
        }
        verifyNoInteractions(usage);
    }

    @Test
    void multipartEtagIsNotWholeFileMd5ButCurrentRowHashMustMatch() {
        when(storage.metadataAt(PREFIX, "a.txt"))
            .thenReturn(new DriveStorage.ObjectMetadata(3, "d41d8cd98f00b204e9800998ecf8427e-2"));
        assertTrue(controller.commit(request()).getData().verified());
        row.setContentMd5("d41d8cd98f00b204e9800998ecf8427e");
        assertThrows(ServiceException.class, () -> controller.commit(request()));
        verify(usage, times(1)).invalidate(PREFIX);
    }

    @Test
    void optionalIdentityDoesNotSkipRegisteredRowChecks() {
        assertTrue(controller.commit(new DriveUploadCommitController.CommitBo("a.txt", "personal", 3L,
            MD5, null, null)).getData().verified());
        row.setUploadBy(2L);
        assertThrows(ServiceException.class, () -> controller.commit(request()));
        row.setUploadBy(1L);
        row.setVersionId(3L);
        assertThrows(ServiceException.class, () -> controller.commit(request()));
        row.setVersionId(2L);
        row.setFileId(42L);
        assertThrows(ServiceException.class, () -> controller.commit(new DriveUploadCommitController.CommitBo(
            "a.txt", "personal", 3L, MD5, 41L, 2L)));
        verify(storage, times(1)).metadataAt(PREFIX, "a.txt");
    }

    @Test
    void permissionAndMetadataFailuresPropagateBeforeInvalidation() {
        when(storage.metadataAt(PREFIX, "a.txt")).thenThrow(new ServiceException("HEAD denied"));
        assertThrows(ServiceException.class, () -> controller.commit(request()));
        doThrow(new ServiceException("not writable")).when(spaces).checkPersonalWritable(1L, 0L);
        assertThrows(ServiceException.class, () -> controller.commit(request()));
        verifyNoInteractions(usage);
        verify(files, times(1)).lockActiveForPresign("personal", 1L, "a.txt");
    }

    @Test
    void sharedUsesLoggedInWriterAndServerOwnedPrefixWithZeroDeclaredSize() {
        row.setSpaceType("shared");
        row.setOwnerId(0L);
        when(spaces.checkSharedWritable(1L, 0L)).thenReturn("shared/");
        when(files.lockActiveForPresign("shared", 0L, "a.txt")).thenReturn(row);
        when(storage.metadataAt("shared/", "a.txt")).thenReturn(new DriveStorage.ObjectMetadata(3, MD5));
        assertTrue(controller.commit(new DriveUploadCommitController.CommitBo("a.txt", "shared", 3L,
            MD5, null, null)).getData().verified());
        verify(spaces).checkSharedWritable(1L, 0L);
        verify(usage).invalidate("shared/");
    }

    @Test
    void malformedRequestsCannotTouchStorage() {
        for (var body : new DriveUploadCommitController.CommitBo[]{
            new DriveUploadCommitController.CommitBo("../a.txt", "personal", 3L, MD5, null, null),
            new DriveUploadCommitController.CommitBo("a.txt", "foreign", 3L, MD5, null, null),
            new DriveUploadCommitController.CommitBo("a.txt", "personal", -1L, MD5, null, null),
            new DriveUploadCommitController.CommitBo("a.txt", "personal", 3L, "", null, null)}) {
            assertThrows(ServiceException.class, () -> controller.commit(body));
        }
        verifyNoInteractions(storage, files, usage);
    }

    @Test
    void emptyObjectStillNeedsHeadAndAnActiveRegisteredRow() {
        String emptyMd5 = "d41d8cd98f00b204e9800998ecf8427e";
        row.setContentMd5(emptyMd5);
        var body = new DriveUploadCommitController.CommitBo("a.txt", null, 0L, emptyMd5, null, null);
        when(storage.metadataAt(PREFIX, "a.txt")).thenReturn(new DriveStorage.ObjectMetadata(0, emptyMd5));
        assertEquals(0L, controller.commit(body).getData().size());
        when(storage.metadataAt(PREFIX, "a.txt")).thenReturn(null);
        assertThrows(ServiceException.class, () -> controller.commit(body));
        when(files.lockActiveForPresign("personal", 1L, "a.txt")).thenReturn(null);
        assertThrows(ServiceException.class, () -> controller.commit(body));
        verify(usage, times(1)).invalidate(PREFIX);
    }

    @Test
    void endpointIsLoginProtectedAndTransactionKeepsRowLockThroughHead() throws Exception {
        assertNotNull(DriveUploadCommitController.class.getAnnotation(SaCheckLogin.class));
        var method = DriveUploadCommitController.class.getMethod("commit", DriveUploadCommitController.CommitBo.class);
        assertArrayEquals(new String[]{"/upload-commit"}, method.getAnnotation(PostMapping.class).value());
        assertArrayEquals(new Class<?>[]{Exception.class}, method.getAnnotation(Transactional.class).rollbackFor());
    }
}
