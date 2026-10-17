package org.dromara.easyshare.drive.service;

import com.baomidou.mybatisplus.core.toolkit.Wrappers;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.mapper.EsFileMapper;
import org.springframework.scheduling.annotation.Scheduled;
import org.springframework.stereotype.Component;

import java.time.LocalDateTime;
import java.util.List;

/**
 * 回收站保留期清理。
 *
 * <p>软删只是把行置为 {@code recycled}，对象和行都还在——不清理的话空间只增不减，
 * 用户看到的「回收站」也会无限堆积。本任务按保留期做彻底删除（对象 + 行）。
 *
 * <p><b>保留期常量为什么放在这个类</b>：前端删除确认框与回收站面板都写着「保留 30 天」，
 * 那句话的可信度直接取决于这个值。把它和清理逻辑放在一起，改动时更容易被看见。
 *
 * <p><b>失败处理</b>：单条失败只记日志、继续下一条。下次调度会重新扫到它，
 * 不需要人工补偿——{@code purgeFromRecycle} 对已被清掉的行会抛错，
 * 那只会让它退出「待清理」集合，不会留下半截状态。
 *
 * @author EasyShare
 */
@Slf4j
@Component
@RequiredArgsConstructor
public class RecycleJanitor {

    /** 回收站保留天数（前端文案「保留 30 天」以此为据，改动须同步改文案）。 */
    public static final int RETENTION_DAYS = 30;

    private final EsFileMapper fileMapper;
    private final DriveFileService fileService;

    /** 每天 03:30 跑一次：避开业务高峰，也给「刚删完就后悔」留足当场还原的窗口。 */
    @Scheduled(cron = "0 30 3 * * ?")
    public void scheduledPurge() {
        int purged = purgeExpired(LocalDateTime.now());
        if (purged > 0) {
            log.info("回收站清理：彻底删除 {} 条超过 {} 天的记录", purged, RETENTION_DAYS);
        }
    }

    /**
     * 清理所有空间里超过保留期的回收站记录。
     *
     * <p>入参是「以哪个时刻为现在」，便于测试边界——不必真的等 30 天。
     *
     * @return 成功彻底删除的条数
     */
    public int purgeExpired(LocalDateTime now) {
        LocalDateTime cutoff = now.minusDays(RETENTION_DAYS);
        List<EsFile> expired = fileMapper.selectList(
            Wrappers.<EsFile>lambdaQuery()
                .eq(EsFile::getState, EsFile.STATE_RECYCLED)
                .lt(EsFile::getDeletedAt, cutoff));
        int done = 0;
        for (EsFile row : expired) {
            try {
                fileService.purgeFromRecycle(row.getSpaceType(), row.getOwnerId(), row.getFileId());
                done++;
            } catch (RuntimeException ex) {
                log.warn("回收站清理失败 fileId={} path={}", row.getFileId(), row.getFilePath(), ex);
            }
        }
        return done;
    }
}
