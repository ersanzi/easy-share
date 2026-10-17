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
import org.mockito.InOrder;
import org.springframework.util.unit.DataSize;

import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyLong;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.ArgumentMatchers.contains;
import static org.mockito.ArgumentMatchers.anyInt;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.doThrow;
import static org.mockito.Mockito.inOrder;
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
    void createStoresExpectedVersionInsteadOfJudgingIt() {
        // create **只寄存不判定**：真正的写入（S3 聚合）发生在 complete，
        // 那才是字节落地与 es_file 登记的时刻。在 create 就拒绝会给出虚假的安全感。
        when(spaceService.checkPersonalWritable(1L, 100L)).thenReturn("users/1/");
        when(mapper.selectList(any())).thenReturn(List.of());
        when(storage.createUpload("users/1/", "big/backup.iso")).thenReturn("u1");

        EsUploadSession created = service.create(EsUploadSession.TYPE_PERSONAL, 1L, 1L,
            "big/backup.iso", 100L, 3L, null);

        assertEquals(3L, created.getExpectedVersion());
        // 只寄存，所以不该在 create 阶段对文件层做任何校验
        verify(fileService, never()).assertVersion(anyString(), anyLong(), anyString(), anyLong());
    }

    @Test
    void createWithoutVersionStoresNullForBackwardCompatibility() {
        // 旧调用方不传版本 → 不做校验（升级不必同时上线）
        when(spaceService.checkPersonalWritable(1L, 100L)).thenReturn("users/1/");
        when(mapper.selectList(any())).thenReturn(List.of());
        when(storage.createUpload("users/1/", "big/backup.iso")).thenReturn("u1");

        EsUploadSession created = service.create(EsUploadSession.TYPE_PERSONAL, 1L, 1L,
            "big/backup.iso", 100L);

        assertNull(created.getExpectedVersion());
        verify(fileService, never()).assertVersion(anyString(), anyLong(), anyString(), anyLong());
    }

    @Test
    void completeAbortsSessionWhenAssertVersionRejects() {
        // ⚠️ 这条钉的是一个**只在真机上暴露过**的缺口（KI-26）：
        // assertVersion 原先写在 try **之外**，它抛异常时不会走 abortQuietly，
        // 会话于是永久停在 uploading（S3 分片白占容量、库里留一条永不结束的会话）。
        // 实测：跑一次「过期版本被拒」的分片上传，库里就多一条 uploading。
        //
        // 真机验证代价高（要造 ≥32 MiB 分片），所以这里用单测钉顺序：
        // 只要 assertVersion 拒绝，就必须 abort 会话、且**不碰对象存储**。
        EsUploadSession uploading = session(9L, EsUploadSession.STATUS_UPLOADING);
        uploading.setExpectedVersion(3L);
        when(mapper.selectById(9L)).thenReturn(uploading);
        doThrow(new ServiceException("文件已被删除（你看到的版本 3），请刷新后再试"))
            .when(fileService).assertVersion(EsUploadSession.TYPE_PERSONAL, 1L, "big/backup.iso", 3L);

        assertThrows(ServiceException.class,
            () -> service.complete(1L, 9L, List.of(new DriveStorage.UploadPart(1, "etag-a"))));

        // 关键断言：会话被中止（不再是 uploading），对象存储一个字节都没写
        verify(storage).abortUpload("users/1/", "big/backup.iso", "s3-upload-id");
        verify(storage, never()).completeUpload(anyString(), anyString(), anyString(), any());
        assertEquals(EsUploadSession.STATUS_ABORTED, uploading.getStatus());
    }

    @Test
    void completeRejectsStaleVersionBeforeTouchingObjectStorage() {
        // ⚠️ 这条钉的是**顺序**，比「是否校验」更要紧：
        // completeUpload 会把分片聚合到目标键上（= 覆盖那份文件）。
        // 若先聚合再校验，对方的改动已经被物理覆盖，此时报「版本冲突」只是事后通知——数据没了。
        EsUploadSession uploading = session(9L, EsUploadSession.STATUS_UPLOADING);
        uploading.setExpectedVersion(3L);
        when(mapper.selectById(9L)).thenReturn(uploading);
        // assertVersion 是 void 方法，打桩必须用 doThrow（when(...) 只适用于有返回值的方法）
        doThrow(new ServiceException("文件已被其他人修改（你看到的版本 3，当前版本 4）"))
            .when(fileService).assertVersion(EsUploadSession.TYPE_PERSONAL, 1L, "big/backup.iso", 3L);

        ServiceException ex = assertThrows(ServiceException.class,
            () -> service.complete(1L, 9L, List.of(new DriveStorage.UploadPart(1, "etag-a"))));
        assertEquals("文件已被其他人修改（你看到的版本 3，当前版本 4）", ex.getMessage());

        // 关键断言：对象存储**一个字节都没被写**
        verify(storage, never()).completeUpload(anyString(), anyString(), anyString(), any());
        // 目录层也不该被登记
        verify(fileService, never()).registerOnPresign(anyString(), anyLong(), anyLong(),
            anyString(), anyLong(), any());
    }

    @Test
    void completeAbortsSessionWhenRegistrationGateRejects() {
        // 条件 UPDATE 是真正的原子闸门（防 TOCTOU）。它拒绝时也不能写对象存储，
        // 并把已经没有价值的 S3 分片清掉，别让它白占容量。
        EsUploadSession uploading = session(9L, EsUploadSession.STATUS_UPLOADING);
        uploading.setExpectedVersion(3L);
        when(mapper.selectById(9L)).thenReturn(uploading);
        doThrow(new ServiceException("文件在本次上传期间被其他人修改（版本 3 已过期）"))
            .when(fileService).registerOnPresign(anyString(), anyLong(), anyLong(), anyString(), anyLong(), any());

        assertThrows(ServiceException.class,
            () -> service.complete(1L, 9L, List.of(new DriveStorage.UploadPart(1, "etag-a"))));

        verify(storage, never()).completeUpload(anyString(), anyString(), anyString(), any());
        verify(storage).abortUpload("users/1/", "big/backup.iso", "s3-upload-id");
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
        uploading.setExpectedVersion(5L);
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
        // 校验先于登记，且版本一路透传到目录层（条件 UPDATE 才是真正的原子闸门）
        verify(fileService).assertVersion(EsUploadSession.TYPE_PERSONAL, 1L, "big/backup.iso", 5L);
        verify(fileService).registerOnPresign(EsUploadSession.TYPE_PERSONAL, 1L, 1L,
            "big/backup.iso", 100L * 1024 * 1024, 5L);
        verify(usageService).invalidate("users/1/");
    }

    @Test
    void completeWritesToObjectStorageOnlyAfterVersionGatePasses() {
        // 顺序断言：CAS 闸门（登记）必须**先于** S3 聚合。
        // 用 InOrder 钉住调用次序——这是「先占版本，再让字节落地」的关键，
        // 反了就等于「先覆盖、后报冲突」，数据已经没了。
        EsUploadSession uploading = session(9L, EsUploadSession.STATUS_UPLOADING);
        uploading.setExpectedVersion(5L);
        when(mapper.selectById(9L)).thenReturn(uploading);

        service.complete(1L, 9L, List.of(new DriveStorage.UploadPart(1, "etag-a")));

        InOrder order = inOrder(fileService, storage);
        order.verify(fileService).assertVersion(anyString(), anyLong(), anyString(), any());
        order.verify(fileService).registerOnPresign(anyString(), anyLong(), anyLong(),
            anyString(), anyLong(), any());
        order.verify(storage).completeUpload(anyString(), anyString(), anyString(), any());
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
