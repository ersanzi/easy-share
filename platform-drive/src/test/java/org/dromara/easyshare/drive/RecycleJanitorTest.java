package org.dromara.easyshare.drive;

import com.baomidou.mybatisplus.core.MybatisConfiguration;
import com.baomidou.mybatisplus.core.metadata.TableInfoHelper;
import org.apache.ibatis.builder.MapperBuilderAssistant;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.mapper.EsFileMapper;
import org.dromara.easyshare.drive.service.DriveFileService;
import org.dromara.easyshare.drive.service.RecycleJanitor;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.time.LocalDateTime;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyLong;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.times;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

/**
 * 回收站保留期清理测试。
 *
 * <p><b>测什么、不测什么</b>：`state='recycled'` 与 `deleted_at < cutoff` 这两个条件
 * 由 LambdaQueryWrapper 表达，在真实数据库里生效；这里是纯 mock，`selectList`
 * 直接返回预置列表。所以本类只覆盖**拿到一批行之后的行为**——
 * 逐条清理、单条失败不中断、计数正确、空列表不打搅下游。
 * 过滤条件本身靠代码审阅（`eq(state)` + `lt(deletedAt, now - RETENTION_DAYS)`）。
 *
 * @author EasyShare
 */
class RecycleJanitorTest {

    private EsFileMapper mapper;
    private DriveFileService fileService;
    private RecycleJanitor janitor;

    @BeforeAll
    static void initMybatisPlusMetadata() {
        // 同 DriveFileServiceTest：纯 mock 环境没有 MyBatis 上下文，
        // LambdaQueryWrapper 解析 EsFile::getState 时找不到 lambda 缓存
        MybatisConfiguration configuration = new MybatisConfiguration();
        MapperBuilderAssistant assistant = new MapperBuilderAssistant(configuration, "");
        TableInfoHelper.initTableInfo(assistant, EsFile.class);
    }

    @BeforeEach
    void setUp() {
        mapper = mock(EsFileMapper.class);
        fileService = mock(DriveFileService.class);
        janitor = new RecycleJanitor(mapper, fileService);
    }

    private static EsFile recycled(long fileId, LocalDateTime deletedAt) {
        EsFile row = new EsFile();
        row.setFileId(fileId);
        row.setSpaceType(EsFile.TYPE_PERSONAL);
        row.setOwnerId(1L);
        row.setFilePath("dir/f" + fileId + ".txt");
        row.setState(EsFile.STATE_RECYCLED);
        row.setDeletedAt(deletedAt);
        return row;
    }

    @Test
    void 拿到几条就清几条_返回值与条数一致() {
        LocalDateTime now = LocalDateTime.of(2026, 10, 17, 3, 30);
        when(mapper.selectList(any())).thenReturn(List.of(
            recycled(1L, now.minusDays(35)),
            recycled(2L, now.minusDays(31))
        ));

        int purged = janitor.purgeExpired(now);

        assertEquals(2, purged);
        verify(fileService, times(2)).purgeFromRecycle(anyString(), anyLong(), anyLong());
    }

    @Test
    void 单条失败不中断整批_其余照常清理() {
        LocalDateTime now = LocalDateTime.of(2026, 10, 17, 3, 30);
        when(mapper.selectList(any())).thenReturn(List.of(
            recycled(1L, now.minusDays(40)),
            recycled(2L, now.minusDays(35)),
            recycled(3L, now.minusDays(31))
        ));
        // 中间那条抛错：既不能让它带走整批，也不能被算作「已清理」
        when(fileService.purgeFromRecycle(anyString(), anyLong(), anyLong()))
            .thenAnswer(invocation -> {
                Long id = invocation.getArgument(2);
                if (id == 2L) {
                    throw new RuntimeException("对象存储超时");
                }
                return null;
            });

        int purged = janitor.purgeExpired(now);

        assertEquals(2, purged);
        verify(fileService, times(3)).purgeFromRecycle(anyString(), anyLong(), anyLong());
    }

    @Test
    void 没有过期记录时不调用彻底删除() {
        when(mapper.selectList(any())).thenReturn(List.of());

        int purged = janitor.purgeExpired(LocalDateTime.of(2026, 10, 17, 3, 30));

        assertEquals(0, purged);
        verify(fileService, never()).purgeFromRecycle(anyString(), anyLong(), anyLong());
    }
}
