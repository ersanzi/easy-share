package org.dromara.easyshare.drive;

import com.baomidou.mybatisplus.core.MybatisConfiguration;
import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.metadata.TableInfoHelper;
import org.apache.ibatis.builder.MapperBuilderAssistant;
import org.dromara.easyshare.drive.domain.EsFileChangeLog;
import org.dromara.easyshare.drive.mapper.EsFileChangeLogMapper;
import org.dromara.easyshare.drive.mapper.EsSpaceMapper;
import org.dromara.easyshare.drive.service.DriveChangeLogService;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.junit.jupiter.params.provider.MethodSource;
import org.junit.jupiter.params.provider.ValueSource;
import org.mockito.ArgumentCaptor;
import org.springframework.dao.DataAccessResourceFailureException;

import java.util.List;
import java.util.stream.LongStream;
import java.util.stream.Stream;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

/** 钉住断档后的拒绝分支、分页水位和分空间裁剪，防止静默跳过文件变更。 */
class DriveChangeLogServiceTest {
    private EsFileChangeLogMapper logs;
    private DriveChangeLogService service;

    @BeforeAll
    static void initMetadata() {
        TableInfoHelper.initTableInfo(new MapperBuilderAssistant(new MybatisConfiguration(), ""),
            EsFileChangeLog.class);
    }

    @BeforeEach
    void setUp() {
        logs = mock(EsFileChangeLogMapper.class);
        service = new DriveChangeLogService(mock(EsSpaceMapper.class), logs);
    }

    @ParameterizedTest
    @CsvSource({"-1,10", "11,10", "9223372036854775807,10"})
    void invalidOrFutureCursorRequiresResetBeforeQuery(long since, long newest) {
        assertReset(service.changesAfter("personal", 7L, since, newest), since);
        verifyNoInteractions(logs);
    }

    @ParameterizedTest
    @ValueSource(longs = {0L, 10L, Long.MAX_VALUE})
    void unchangedWatermarkReturnsAnEmptyPageWithoutAdvancing(long cursor) {
        var page = service.changesAfter("personal", 7L, cursor, cursor);
        assertEquals(cursor, page.nextVersion());
        assertTrue(page.entries().isEmpty());
        assertFalse(page.resetRequired());
        assertFalse(page.hasMore());
        verifyNoInteractions(logs);
    }

    @ParameterizedTest
    @MethodSource("incompleteHistories")
    void missingReorderedOrInvalidHistoryNeverReturnsAPartialPage(List<EsFileChangeLog> entries) {
        when(logs.selectList(any())).thenReturn(entries);
        assertReset(service.changesAfter("personal", 7L, 10L, 13L), 10L);
    }

    static Stream<List<EsFileChangeLog>> incompleteHistories() {
        return Stream.of(
            List.of(),                                      // 日志已全部被裁剪。
            List.of(entry(12L), entry(13L)),                  // 首条丢失。
            List.of(entry(11L), entry(13L)),                  // 中间丢失。
            List.of(entry(11L), entry(12L)),                  // 未满页却缺尾条。
            List.of(entry(11L), entry(11L), entry(13L)),      // 重复版本。
            List.of(entry(12L), entry(11L), entry(13L)),      // 乱序版本。
            List.of(entry(null)),                            // 版本为空。
            List.of(entry(11L), entry(12L), entry(13L), entry(14L)) // 越过请求读取水位。
        );
    }

    @Test
    void pagesAdvanceOnlyThroughDeliveredEventsAndKeepSpaceScope() {
        int limit = DriveChangeLogService.CHANGE_PAGE_SIZE;
        long firstEnd = 10L + limit;
        long newest = firstEnd + 2;
        when(logs.selectList(any())).thenReturn(
            LongStream.rangeClosed(11L, firstEnd).mapToObj(DriveChangeLogServiceTest::entry).toList(),
            List.of(entry(firstEnd + 1), entry(newest)));

        var first = service.changesAfter("personal", 7L, 10L, newest);
        assertEquals(limit, first.entries().size());
        assertEquals(firstEnd, first.nextVersion());
        assertTrue(first.hasMore());
        assertFalse(first.resetRequired());
        var second = service.changesAfter("personal", 7L, first.nextVersion(), newest);
        assertEquals(List.of(firstEnd + 1, newest),
            second.entries().stream().map(EsFileChangeLog::getSpaceVersion).toList());
        assertEquals(newest, second.nextVersion());
        assertFalse(second.hasMore());
        assertFalse(second.resetRequired());

        var queries = ArgumentCaptor.forClass(LambdaQueryWrapper.class);
        verify(logs, times(2)).selectList(queries.capture());
        for (var query : queries.getAllValues()) {
            assertScope(query);
            String sql = query.getSqlSegment();
            assertTrue(sql.contains("space_version >"));
            assertTrue(sql.contains("space_version <="));
            assertTrue(sql.contains("ORDER BY space_version ASC"));
            assertTrue(sql.endsWith("LIMIT " + limit));
            assertTrue(query.getParamNameValuePairs().containsValue(newest));
        }
        assertTrue(queries.getAllValues().get(0).getParamNameValuePairs().containsValue(10L));
        assertTrue(queries.getAllValues().get(1).getParamNameValuePairs().containsValue(firstEnd));
    }

    @Test
    void aTrimBetweenPagesRequiresResetInsteadOfSkippingMissingEvents() {
        long boundary = DriveChangeLogService.CHANGE_PAGE_SIZE;
        when(logs.selectList(any())).thenReturn(
            LongStream.rangeClosed(1, boundary).mapToObj(DriveChangeLogServiceTest::entry).toList(),
            List.of(entry(boundary + 2)));
        var first = service.changesAfter("personal", 7L, 0L, boundary + 2);
        assertTrue(first.hasMore());
        assertReset(service.changesAfter("personal", 7L, first.nextVersion(), boundary + 2), boundary);
    }

    @Test
    void lastRepresentableVersionDoesNotOverflowContinuityCheck() {
        when(logs.selectList(any())).thenReturn(List.of(entry(Long.MAX_VALUE)));
        var page = service.changesAfter("personal", 7L, Long.MAX_VALUE - 1, Long.MAX_VALUE);
        assertEquals(Long.MAX_VALUE, page.nextVersion());
        assertFalse(page.resetRequired());
        assertFalse(page.hasMore());
    }

    @Test
    void retentionCannotTrimAnotherUsersLogUsingAnUnrelatedSpaceVersion() {
        when(logs.selectList(any())).thenReturn(List.of(entry(700L)));
        service.trim("personal", 7L, 10_000);
        var query = ArgumentCaptor.forClass(LambdaQueryWrapper.class);
        var deletion = ArgumentCaptor.forClass(LambdaQueryWrapper.class);
        verify(logs).selectList(query.capture());
        verify(logs).delete(deletion.capture());
        assertScope(query.getValue());
        assertTrue(query.getValue().getSqlSegment().endsWith("LIMIT 1 OFFSET 9999"));
        assertScope(deletion.getValue());
        assertTrue(deletion.getValue().getSqlSegment().contains("space_version <"));
        assertTrue(deletion.getValue().getParamNameValuePairs().containsValue(700L));
    }

    @Test
    void retentionDoesNotDeleteWhenSpaceHasFewerThanKeepEntries() {
        when(logs.selectList(any())).thenReturn(List.of());
        service.trim("personal", 7L, 10_000);
        verify(logs, never()).delete(any(LambdaQueryWrapper.class));
    }

    @Test
    void retentionFailureDoesNotHideSuccessfulFullListing() {
        when(logs.selectList(any())).thenThrow(new DataAccessResourceFailureException("日志存储暂不可用"));
        assertDoesNotThrow(() -> service.trim("personal", 7L, 10_000));
        verify(logs, never()).delete(any(LambdaQueryWrapper.class));
    }

    private static void assertScope(LambdaQueryWrapper<?> query) {
        String sql = query.getSqlSegment();
        assertTrue(sql.contains("space_type ="));
        assertTrue(sql.contains("owner_id ="));
        assertTrue(query.getParamNameValuePairs().containsValue("personal"));
        assertTrue(query.getParamNameValuePairs().containsValue(7L));
    }

    private static void assertReset(DriveChangeLogService.ChangePage page, long since) {
        assertTrue(page.resetRequired());
        assertEquals(since, page.nextVersion());
        assertTrue(page.entries().isEmpty());
        assertFalse(page.hasMore());
    }

    private static EsFileChangeLog entry(Long version) {
        EsFileChangeLog entry = new EsFileChangeLog();
        entry.setSpaceType("personal");
        entry.setOwnerId(7L);
        entry.setSpaceVersion(version);
        entry.setFileId(71L);
        entry.setEventType(EsFileChangeLog.EVENT_UPDATED);
        entry.setFilePath("mine.txt");
        return entry;
    }
}
