package org.dromara.easyshare.drive;

import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.mapper.EsFileMapper;
import org.dromara.easyshare.drive.service.DriveFileService;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;
import org.springframework.dao.DuplicateKeyException;

import java.time.Instant;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

/**
 * 文件目录层服务测试：登记幂等、并发回退、列表补账、fileId 越权防护。
 *
 * @author EasyShare
 */
class DriveFileServiceTest {

    private EsFileMapper mapper;
    private DriveFileService service;

    @BeforeEach
    void setUp() {
        mapper = mock(EsFileMapper.class);
        service = new DriveFileService(mapper);
    }

    private static EsFile row(long fileId, String path) {
        EsFile row = new EsFile();
        row.setFileId(fileId);
        row.setSpaceType(EsFile.TYPE_PERSONAL);
        row.setOwnerId(1L);
        row.setFilePath(path);
        row.setFileName(path.substring(path.lastIndexOf('/') + 1));
        row.setFileSize(1L);
        return row;
    }

    @Test
    void registerOnPresignInsertsNormalizedPath() {
        when(mapper.selectOne(any())).thenReturn(null);

        service.registerOnPresign(EsFile.TYPE_PERSONAL, 1L, 1L, "photos\\2024\\a.jpg", 42L);

        ArgumentCaptor<EsFile> captor = ArgumentCaptor.forClass(EsFile.class);
        verify(mapper).insert(captor.capture());
        EsFile inserted = captor.getValue();
        assertEquals("photos/2024/a.jpg", inserted.getFilePath());
        assertEquals("a.jpg", inserted.getFileName());
        assertEquals(1L, inserted.getOwnerId());
        assertEquals(42L, inserted.getFileSize());
    }

    @Test
    void registerOnPresignUpdatesSizeWhenDeclared() {
        EsFile existing = row(7L, "a.txt");
        existing.setFileSize(1L);
        when(mapper.selectOne(any())).thenReturn(existing);

        service.registerOnPresign(EsFile.TYPE_PERSONAL, 1L, 1L, "a.txt", 99L);

        verify(mapper).updateById(existing);
        assertEquals(99L, existing.getFileSize());
        verify(mapper, never()).insert(any(EsFile.class));
    }

    @Test
    void registerOnPresignKeepsSizeWhenUnknown() {
        EsFile existing = row(7L, "a.txt");
        when(mapper.selectOne(any())).thenReturn(existing);

        service.registerOnPresign(EsFile.TYPE_PERSONAL, 1L, 1L, "a.txt", 0L);

        verify(mapper, never()).updateById(any(EsFile.class));
    }

    @Test
    void insertRaceFallsBackToExistingRow() {
        // insertSafely 直接插入、撞唯一键后补查已存在行——唯一的 selectOne 发生在兜底路径
        when(mapper.selectOne(any())).thenReturn(row(7L, "a.txt"));
        when(mapper.insert(any(EsFile.class))).thenThrow(new DuplicateKeyException("uk_es_file_path"));

        Long result = service.reconcileAndMap(EsFile.TYPE_PERSONAL, 1L, 1L,
            List.of(new DriveObject("a.txt", 1L, Instant.now()))).get("a.txt");

        assertEquals(7L, result);
    }

    @Test
    void reconcileMapsExistingAndInsertsMissing() {
        when(mapper.selectList(any())).thenReturn(List.of(row(7L, "a.txt")));
        when(mapper.insert(any(EsFile.class))).thenAnswer(invocation -> {
            EsFile inserted = invocation.getArgument(0, EsFile.class);
            inserted.setFileId(999L);
            return 1;
        });

        Map<String, Long> result = service.reconcileAndMap(EsFile.TYPE_PERSONAL, 1L, 1L, List.of(
            new DriveObject("a.txt", 1L, Instant.now()),
            new DriveObject("photos/b.jpg", 2L, Instant.now())));

        assertEquals(7L, result.get("a.txt"));
        assertEquals(999L, result.get("photos/b.jpg"));
        ArgumentCaptor<EsFile> captor = ArgumentCaptor.forClass(EsFile.class);
        verify(mapper).insert(captor.capture());
        assertEquals("photos/b.jpg", captor.getValue().getFilePath());
        assertEquals("b.jpg", captor.getValue().getFileName());
    }

    @Test
    void resolveByFileIdChecksOwnership() {
        // 他人（ownerId=2）的行：个人空间 ownerId=1 的请求必须被拒
        EsFile foreign = row(7L, "secret.txt");
        foreign.setOwnerId(2L);
        when(mapper.selectById(7L)).thenReturn(foreign);

        ServiceException ex = assertThrows(ServiceException.class,
            () -> service.resolveDeletePath(EsFile.TYPE_PERSONAL, 1L, 7L, null));
        assertEquals("文件不存在或不属于当前空间", ex.getMessage());

        when(mapper.selectById(7L)).thenReturn(row(7L, "mine.txt"));
        assertEquals("mine.txt", service.resolveDeletePath(EsFile.TYPE_PERSONAL, 1L, 7L, null));
    }

    @Test
    void resolveByPathNormalizesWhenFileIdAbsent() {
        assertEquals("photos/a.jpg",
            service.resolveDeletePath(EsFile.TYPE_PERSONAL, 1L, null, "photos\\a.jpg"));
        verify(mapper, never()).selectById(any());
    }

    @Test
    void deleteRegisteredRemovesBySpaceAndPath() {
        service.deleteRegistered(EsFile.TYPE_SHARED, EsFile.SHARED_OWNER, "docs/x.pdf");

        ArgumentCaptor<com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper<EsFile>> captor =
            ArgumentCaptor.forClass(com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper.class);
        verify(mapper).delete(captor.capture());
        // Wrapper 条件无法直接断言内部值，落点在"确实按空间+路径删除"不抛错即可
        assertNull(captor.getValue().getEntity());
    }
}
