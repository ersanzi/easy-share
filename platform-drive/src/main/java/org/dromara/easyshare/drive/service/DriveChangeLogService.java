package org.dromara.easyshare.drive.service;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.common.mybatis.utils.IdGeneratorUtil;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.domain.EsFileChangeLog;
import org.dromara.easyshare.drive.domain.EsSpace;
import org.dromara.easyshare.drive.mapper.EsFileChangeLogMapper;
import org.dromara.easyshare.drive.mapper.EsSpaceMapper;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.util.List;

/**
 * 增量列举：空间版本 + 变更日志（路线图切片 2）。
 *
 * <p>职责边界：
 * <ul>
 *   <li><b>不是</b>文件状态机的真相源——那是 {@code es_file}；</li>
 *   <li><b>是</b>给客户端看的差异流：客户端记住 {@code spaceVersion}，
 *       下次带 {@code sinceVersion} 只拉之后的变更；</li>
 *   <li>日志不足或客户端太久没同步时，调用方退全量重建（全量接口已存在）。</li>
 * </ul>
 *
 * <p>写入原则：任何文件生命周期变更（新建/覆盖/软删/还原/移动/彻底删除）
 * 都必须调用一次 {@code record(...)}。漏一次，客户端就会永远看不到那次变更——
 * 因此本方法刻意放在所有状态变更服务旁边，而不是让 Controller 自己记得。
 *
 * @author EasyShare
 */
@Slf4j
@Service
@RequiredArgsConstructor
public class DriveChangeLogService {

    /** 单页硬上限，避免长时间离线的客户端一次读取整个日志表。 */
    public static final int CHANGE_PAGE_SIZE = 500;

    private final EsSpaceMapper spaceMapper;
    private final EsFileChangeLogMapper logMapper;

    /**
     * 取空间的当前版本号（客户端拉全量后应记住这个值）。
     */
    public long currentVersion(String spaceType, Long ownerId) {
        EsSpace space = spaceOf(spaceType, ownerId);
        return space.getSpaceVersion() == null ? 0L : space.getSpaceVersion();
    }

    /**
     * 在请求开始的版本水位内取一页连续日志；游标只能推进到本页实际交付的位置。
     * 日志被裁剪、游标超前或中间缺条时必须重建，不能把不完整差异当成成功。
     */
    public ChangePage changesAfter(String spaceType, Long ownerId, long sinceVersion, long throughVersion) {
        if (sinceVersion < 0 || sinceVersion > throughVersion) {
            return ChangePage.reset(sinceVersion);
        }
        if (sinceVersion == throughVersion) {
            return new ChangePage(List.of(), sinceVersion, false, false);
        }
        List<EsFileChangeLog> entries = logMapper.selectList(new LambdaQueryWrapper<EsFileChangeLog>()
            .eq(EsFileChangeLog::getSpaceType, spaceType)
            .eq(EsFileChangeLog::getOwnerId, ownerId)
            .gt(EsFileChangeLog::getSpaceVersion, sinceVersion)
            .le(EsFileChangeLog::getSpaceVersion, throughVersion)
            .orderByAsc(EsFileChangeLog::getSpaceVersion)
            .last("LIMIT " + CHANGE_PAGE_SIZE));
        long cursor = sinceVersion;
        for (EsFileChangeLog entry : entries) {
            Long version = entry.getSpaceVersion();
            if (cursor == Long.MAX_VALUE || version == null || version != cursor + 1
                || version > throughVersion) {
                return ChangePage.reset(sinceVersion);
            }
            cursor = version;
        }
        // 未满页却没有走到读取水位，说明尾部日志缺失；满页则由下一页继续验证。
        if (entries.size() < CHANGE_PAGE_SIZE && cursor < throughVersion) {
            return ChangePage.reset(sinceVersion);
        }
        return new ChangePage(List.copyOf(entries), cursor, cursor < throughVersion, false);
    }

    /** reset 时不交付任何部分日志，也不把失败的读取水位交给客户端保存。 */
    public record ChangePage(List<EsFileChangeLog> entries, long nextVersion,
                             boolean hasMore, boolean resetRequired) {
        private static ChangePage reset(long sinceVersion) {
            return new ChangePage(List.of(), sinceVersion, false, true);
        }
    }

    /**
     * 记录一次变更，并推进空间版本。
     *
     * <p>事务保证“版本推进”与“写日志”一起成功或一起失败——
     * 只推进不写日志会让客户端永远错过那次变更；只写日志不推进则下次拉不到。
     *
     * @param event 事件类型
     * @param row   变更后的目录行（purged 后行已删，可传 null，此时必须给 fileId/path）
     * @param fileId 稳定文件 ID（row 为 null 时必需）
     * @param newPath 事件后的路径；purged 可为 null
     * @param oldPath moved 的源路径；其他事件传 null
     */
    @Transactional(rollbackFor = Exception.class)
    public void record(String spaceType, Long ownerId, String event, EsFile row,
                       Long fileId, String newPath, String oldPath) {
        long version = bumpVersion(spaceType, ownerId);
        EsFileChangeLog entry = new EsFileChangeLog();
        entry.setLogId(IdGeneratorUtil.nextLongId());
        entry.setSpaceType(spaceType);
        entry.setOwnerId(ownerId);
        entry.setSpaceVersion(version);
        entry.setEventType(event);
        entry.setFileId(fileId);
        entry.setFilePath(newPath);
        entry.setOldPath(oldPath);
        entry.setDirectory(row != null && row.directoryEntry());
        logMapper.insert(entry);
    }

    /**
     * 推进空间版本并返回新值。
     *
     * <p>用条件 UPDATE（{@code WHERE space_version = 当前值}）保证并发下单调：
     * 两个并发变更不可能拿到同一个版本号。影响行数为 0 表示有人先推进了，重读重试。
     */
    private long bumpVersion(String spaceType, Long ownerId) {
        for (int attempt = 0; attempt < 5; attempt++) {
            EsSpace space = spaceOf(spaceType, ownerId);
            long current = space.getSpaceVersion() == null ? 0L : space.getSpaceVersion();
            long next = current + 1L;
            int updated = spaceMapper.update(null, new LambdaUpdateWrapper<EsSpace>()
                .eq(EsSpace::getSpaceId, space.getSpaceId())
                .eq(EsSpace::getSpaceVersion, current)
                .set(EsSpace::getSpaceVersion, next));
            if (updated > 0) {
                return next;
            }
        }
        throw new ServiceException("空间版本竞争过于频繁，请稍后重试");
    }

    /**
     * 变更日志裁剪（切片 2：日志是缓存，不是真相源）。
     *
     * <p>每个空间独立保留最近 {@code keep} 条；其他客户端可能仍在使用更早的游标，
     * {@link #changesAfter} 会检测其日志是否连续并要求重建，不能假设一次全量让所有旧日志失效。
     * 裁剪失败不阻塞列举，日志回收不改变已经交付的文件视图。
     */
    public void trim(String spaceType, Long ownerId, int keep) {
        if (keep <= 0) {
            throw new IllegalArgumentException("日志保留条数必须大于零");
        }
        try {
            // 不同空间的版本不可比较；只裁剪当前空间，查询也不再加载整张日志表。
            List<EsFileChangeLog> boundary = logMapper.selectList(
                new LambdaQueryWrapper<EsFileChangeLog>()
                    .select(EsFileChangeLog::getSpaceVersion)
                    .eq(EsFileChangeLog::getSpaceType, spaceType)
                    .eq(EsFileChangeLog::getOwnerId, ownerId)
                    .orderByDesc(EsFileChangeLog::getSpaceVersion)
                    .last("LIMIT 1 OFFSET " + (keep - 1)));
            if (boundary.isEmpty()) {
                return;
            }
            long cutoff = boundary.getFirst().getSpaceVersion();
            logMapper.delete(new LambdaQueryWrapper<EsFileChangeLog>()
                .eq(EsFileChangeLog::getSpaceType, spaceType)
                .eq(EsFileChangeLog::getOwnerId, ownerId)
                .lt(EsFileChangeLog::getSpaceVersion, cutoff));
        } catch (RuntimeException ex) {
            log.warn("变更日志裁剪失败（忽略，不影响列举）: {}", ex.getMessage());
        }
    }

    private EsSpace spaceOf(String spaceType, Long ownerId) {
        EsSpace space = spaceMapper.selectOne(new LambdaQueryWrapper<EsSpace>()
            .eq(EsSpace::getSpaceType, spaceType)
            .eq(EsSpace::getOwnerId, ownerId));
        if (space == null) {
            throw new ServiceException("空间不存在或尚未初始化");
        }
        return space;
    }
}
