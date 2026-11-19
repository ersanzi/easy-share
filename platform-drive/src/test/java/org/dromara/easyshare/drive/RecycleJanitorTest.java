package org.dromara.easyshare.drive;

import com.baomidou.mybatisplus.core.MybatisConfiguration;
import com.baomidou.mybatisplus.core.metadata.TableInfoHelper;
import org.apache.ibatis.builder.MapperBuilderAssistant;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.mapper.EsFileMapper;
import org.dromara.easyshare.drive.service.DriveRecycleService;
import org.dromara.easyshare.drive.service.RecycleJanitor;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.time.LocalDateTime;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.mockito.ArgumentMatchers.any;
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
    private DriveRecycleService recycleService;
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
        recycleService = mock(DriveRecycleService.class);
        janitor = new RecycleJanitor(mapper, recycleService);
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

    private static DriveRecycleService.Result purged(int members) {
        return new DriveRecycleService.Result(null, null, null, null, false, "purged", members);
    }

    @Test
    void 拿到几条就清几条_返回值与条数一致() {
        LocalDateTime now = LocalDateTime.of(2026, 10, 17, 3, 30);
        when(mapper.selectList(any())).thenReturn(List.of(
            recycled(1L, now.minusDays(35)),
            recycled(2L, now.minusDays(31))
        ));
        when(recycleService.purgeExpiredFile(any())).thenReturn(purged(1));

        int purged = janitor.purgeExpired(now);

        assertEquals(2, purged);
        verify(recycleService, times(2)).purgeExpiredFile(any());
    }

    @Test
    void 单条失败不中断整批_其余照常清理() {
        LocalDateTime now = LocalDateTime.of(2026, 10, 17, 3, 30);
        when(mapper.selectList(any())).thenReturn(List.of(
            recycled(1L, now.minusDays(40)),
            recycled(2L, now.minusDays(35)),
            recycled(3L, now.minusDays(31))
        ));
        // 中间旧条目抛错：不计数，且不妨碍后续条目。
        when(recycleService.purgeExpiredFile(any())).thenAnswer(invocation -> {
            EsFile row = invocation.getArgument(0);
            if (row.getFileId() == 2L) { throw new RuntimeException("对象存储超时"); }
            return purged(1);
        });

        int purged = janitor.purgeExpired(now);

        assertEquals(2, purged);
        verify(recycleService, times(3)).purgeExpiredFile(any());
    }

    @Test
    void 没有过期记录时不调用彻底删除() {
        when(mapper.selectList(any())).thenReturn(List.of());

        int purged = janitor.purgeExpired(LocalDateTime.of(2026, 10, 17, 3, 30));

        assertEquals(0, purged);
        verify(recycleService, never()).purgeExpiredFile(any());
    }

    @Test
    void 同批只清根一次_子项不抢占批次_按成员数累计() {
        LocalDateTime now = LocalDateTime.of(2026, 10, 17, 3, 30);
        EsFile child = recycled(2L, now.minusDays(35));
        EsFile root = recycled(1L, now.minusDays(35));
        EsFile duplicateRoot = recycled(1L, now.minusDays(35));
        for (EsFile row : List.of(child, root, duplicateRoot)) {
            row.setSpaceType(EsFile.TYPE_SHARED); row.setOwnerId(EsFile.SHARED_OWNER);
            row.setRecycleBatchId("shared-batch");
            row.setRecycleRoot(row.getFileId() == 1L);
        }
        when(mapper.selectList(any())).thenReturn(List.of(child, root, duplicateRoot, child));
        when(recycleService.purgeExpiredFile(root)).thenReturn(purged(4));

        assertEquals(4, janitor.purgeExpired(now));
        verify(recycleService, times(1)).purgeExpiredFile(any());
        verify(recycleService).purgeExpiredFile(root);
        verify(recycleService, never()).purgeExpiredFile(child);
    }

    @Test
    void 未确认成功或空结果不计数_继续后续条目() {
        LocalDateTime now = LocalDateTime.of(2026, 10, 17, 3, 30);
        when(mapper.selectList(any())).thenReturn(List.of(
            recycled(1L, now.minusDays(35)), recycled(2L, now.minusDays(35)),
            recycled(3L, now.minusDays(35))
        ));
        when(recycleService.purgeExpiredFile(any())).thenReturn(
            new DriveRecycleService.Result(null, null, null, null, false, "purging", 8),
            null, purged(1));

        assertEquals(1, janitor.purgeExpired(now));
        verify(recycleService, times(3)).purgeExpiredFile(any());
    }
}
