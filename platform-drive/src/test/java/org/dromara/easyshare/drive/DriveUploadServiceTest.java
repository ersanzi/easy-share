package org.dromara.easyshare.drive;

import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.domain.EsUploadSession;
import org.dromara.easyshare.drive.mapper.EsUploadSessionMapper;
import org.dromara.easyshare.drive.service.DriveFileService;
import org.dromara.easyshare.drive.service.DriveUploadService;
import org.dromara.easyshare.drive.service.SpaceService;
import org.dromara.easyshare.drive.service.SpaceUsageService;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;
import org.springframework.util.unit.DataSize;

import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyLong;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.ArgumentMatchers.contains;
import static org.mockito.ArgumentMatchers.anyInt;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

/**
 * Upload Session 服务测试：配额联动、防孤儿分片、幂等 Complete、归属强校验。
 *
 * @author EasyShare
 */
class DriveUploadServiceTest {

    private EsUploadSessionMapper mapper;
    private DriveStorage storage;
    private DriveFileService fileService;
    private SpaceService spaceService;
    private SpaceUsageService usageService;
    private DriveProperties properties;
    private DriveUploadService service;

    @BeforeEach
    void setUp() {
        mapper = mock(EsUploadSessionMapper.class);
        storage = mock(DriveStorage.class);
        fileService = mock(DriveFileService.class);
        spaceService = mock(SpaceService.class);
        usageService = mock(SpaceUsageService.class);
        properties = new DriveProperties();
        properties.setPartSize(DataSize.ofMegabytes(8));
        service = new DriveUploadService(mapper, storage, fileService, spaceService,
            usageService, properties);
    }

    private static EsUploadSession session(long id, String status) {
        EsUploadSession session = new EsUploadSession();
        session.setSessionId(id);
        session.setSpaceType(EsUploadSession.TYPE_PERSONAL);
        session.setOwnerId(1L);
        session.setUploadBy(1L);
        session.setFilePath("big/backup.iso");
        session.setUploadId("s3-upload-id");
        session.setPartSize(8L * 1024 * 1024);
        session.setDeclaredSize(100L * 1024 * 1024);
        session.setStatus(status);
        return session;
    }

    @Test
    void createAbortsStaleSessionsThenInserts() {
        when(spaceService.checkPersonalWritable(1L, 100L)).thenReturn("users/1/");
        when(mapper.selectList(any())).thenReturn(List.of(session(9L, EsUploadSession.STATUS_UPLOADING)));
        when(storage.createUpload("users/1/", "big/backup.iso")).thenReturn("new-upload-id");

        EsUploadSession created = service.create(EsUploadSession.TYPE_PERSONAL, 1L, 1L,
            "big/backup.iso", 100L);

        // 遗留会话被放弃（S3 Abort + 状态推进），防孤儿分片
        verify(storage).abortUpload("users/1/", "big/backup.iso", "s3-upload-id");
        ArgumentCaptor<EsUploadSession> updated = ArgumentCaptor.forClass(EsUploadSession.class);
        verify(mapper).updateById(updated.capture());
        assertEquals(9L, updated.getValue().getSessionId());
        assertEquals(EsUploadSession.STATUS_ABORTED, updated.getValue().getStatus());
        // 新会话落行：S3 uploadId 与服务端定的分片大小
        ArgumentCaptor<EsUploadSession> inserted = ArgumentCaptor.forClass(EsUploadSession.class);
        verify(mapper).insert(inserted.capture());
        assertEquals("new-upload-id", inserted.getValue().getUploadId());
        assertEquals(8L * 1024 * 1024, created.getPartSize());
        assertEquals(EsUploadSession.STATUS_UPLOADING, created.getStatus());
    }

    @Test
    void presignPartRejectsForeignUser() {
        when(mapper.selectById(9L)).thenReturn(session(9L, EsUploadSession.STATUS_UPLOADING));

        ServiceException ex = assertThrows(ServiceException.class,
            () -> service.presignPart(2L, 9L, 3));
        assertEquals("上传会话不存在或不属于当前用户", ex.getMessage());
        verify(storage, never()).presignPartAt(anyString(), anyString(), anyString(), anyInt());
    }

    @Test
    void presignPartSignsWithSessionUploadId() {
        when(mapper.selectById(9L)).thenReturn(session(9L, EsUploadSession.STATUS_UPLOADING));
        when(storage.presignPartAt("users/1/", "big/backup.iso", "s3-upload-id", 3))
            .thenReturn("https://signed");

        assertEquals("https://signed", service.presignPart(1L, 9L, 3));
    }

    @Test
    void completeSubmitsPartsAndRegistersFile() {
        EsUploadSession uploading = session(9L, EsUploadSession.STATUS_UPLOADING);
        when(mapper.selectById(9L)).thenReturn(uploading);
        when(fileService.fileIdOfPath(EsUploadSession.TYPE_PERSONAL, 1L, "big/backup.iso"))
            .thenReturn(777L);

        Long fileId = service.complete(1L, 9L, List.of(new DriveStorage.UploadPart(1, "etag-a")));

        assertEquals(777L, fileId);
        @SuppressWarnings("unchecked")
        ArgumentCaptor<List<DriveStorage.UploadPart>> captor =
            ArgumentCaptor.forClass((Class) List.class);
        verify(storage).completeUpload(eq("users/1/"), eq("big/backup.iso"),
            eq("s3-upload-id"), captor.capture());
        assertEquals(1, captor.getValue().size());
        assertEquals(EsUploadSession.STATUS_COMPLETED, uploading.getStatus());
        verify(fileService).registerOnPresign(EsUploadSession.TYPE_PERSONAL, 1L, 1L,
            "big/backup.iso", 100L * 1024 * 1024);
        verify(usageService).invalidate("users/1/");
    }

    @Test
    void completeIsIdempotentForFinishedSessions() {
        when(mapper.selectById(9L)).thenReturn(session(9L, EsUploadSession.STATUS_COMPLETED));
        when(fileService.fileIdOfPath(anyString(), anyLong(), anyString())).thenReturn(777L);

        assertEquals(777L, service.complete(1L, 9L, List.of(new DriveStorage.UploadPart(1, "etag-x"))));

        // 幂等：不触 S3（uploadId 已消费），也不再动目录层
        verify(storage, never()).completeUpload(anyString(), anyString(), anyString(), any());
        verify(fileService, never()).registerOnPresign(anyString(), anyLong(), anyLong(), anyString(), anyLong());
    }

    @Test
    void completeRejectsAbortedSession() {
        when(mapper.selectById(9L)).thenReturn(session(9L, EsUploadSession.STATUS_ABORTED));

        ServiceException ex = assertThrows(ServiceException.class,
            () -> service.complete(1L, 9L, List.of(new DriveStorage.UploadPart(1, "etag"))));
        assertEquals("上传会话已放弃，请重新创建会话", ex.getMessage());
    }

    @Test
    void abortUploadsThenMarksAborted() {
        when(mapper.selectById(9L)).thenReturn(session(9L, EsUploadSession.STATUS_UPLOADING));

        service.abort(1L, 9L);

        verify(storage).abortUpload("users/1/", "big/backup.iso", "s3-upload-id");
        ArgumentCaptor<EsUploadSession> captor = ArgumentCaptor.forClass(EsUploadSession.class);
        verify(mapper).updateById(captor.capture());
        assertEquals(EsUploadSession.STATUS_ABORTED, captor.getValue().getStatus());
    }

    @Test
    void abortRejectsCompletedSession() {
        when(mapper.selectById(9L)).thenReturn(session(9L, EsUploadSession.STATUS_COMPLETED));

        assertThrows(ServiceException.class, () -> service.abort(1L, 9L));
        verify(storage, never()).abortUpload(anyString(), contains("backup"), anyString());
    }
}
