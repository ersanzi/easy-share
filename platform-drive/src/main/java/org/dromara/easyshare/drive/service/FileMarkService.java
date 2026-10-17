package org.dromara.easyshare.drive.service;

import com.baomidou.mybatisplus.core.toolkit.Wrappers;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.dromara.easyshare.drive.domain.EsFileMark;
import org.dromara.easyshare.drive.mapper.EsFileMarkMapper;
import org.springframework.dao.DuplicateKeyException;
import org.springframework.stereotype.Service;

import java.time.LocalDateTime;
import java.util.List;
import java.util.Set;
import java.util.stream.Collectors;

/**
 * 星标与最近访问。
 *
 * <p>两者结构相同、写入时机不同：星标由用户显式点击，最近访问由打开/预览/下载触发。
 * 放同一个服务是因为「标记」这件事的语义一致——一条用户 × 路径 × 类型的记录。
 *
 * @author EasyShare
 */
@Slf4j
@Service
@RequiredArgsConstructor
public class FileMarkService {

    /** 最近访问保留条数：超出后裁掉最旧的。星标不受此限制。 */
    static final int RECENT_KEEP = 50;

    private final EsFileMarkMapper markMapper;

    /**
     * 设置或取消星标。
     *
     * @return 操作后的状态（true=已星标）
     */
    public boolean setStar(Long userId, String spaceType, String path, boolean starred) {
        if (!starred) {
            markMapper.delete(Wrappers.<EsFileMark>lambdaQuery()
                .eq(EsFileMark::getUserId, userId)
                .eq(EsFileMark::getSpaceType, spaceType)
                .eq(EsFileMark::getFilePath, path)
                .eq(EsFileMark::getKind, EsFileMark.KIND_STAR));
            return false;
        }
        upsert(userId, spaceType, path, EsFileMark.KIND_STAR);
        return true;
    }

    /**
     * 记录一次最近访问。
     *
     * <p>同一路径只保留一条（刷新时间），并在写入后裁剪到 {@link #RECENT_KEEP} 条——
     * 不裁剪的话这张表会随使用时长无界增长，而「最近」这个入口本身只需要最近几十条。
     */
    public void touch(Long userId, String spaceType, String path) {
        upsert(userId, spaceType, path, EsFileMark.KIND_RECENT);
        trimRecent(userId, spaceType);
    }

    /** 当前空间下该用户星标的路径集合 */
    public Set<String> starredPaths(Long userId, String spaceType) {
        return markMapper.selectList(Wrappers.<EsFileMark>lambdaQuery()
                .eq(EsFileMark::getUserId, userId)
                .eq(EsFileMark::getSpaceType, spaceType)
                .eq(EsFileMark::getKind, EsFileMark.KIND_STAR))
            .stream()
            .map(EsFileMark::getFilePath)
            .collect(Collectors.toSet());
    }

    /** 最近访问的路径，按时间倒序 */
    public List<String> recentPaths(Long userId, String spaceType, int limit) {
        int capped = Math.max(1, Math.min(limit, RECENT_KEEP));
        return markMapper.selectList(Wrappers.<EsFileMark>lambdaQuery()
                .eq(EsFileMark::getUserId, userId)
                .eq(EsFileMark::getSpaceType, spaceType)
                .eq(EsFileMark::getKind, EsFileMark.KIND_RECENT)
                .orderByDesc(EsFileMark::getMarkedAt)
                .last("limit " + capped))
            .stream()
            .map(EsFileMark::getFilePath)
            .toList();
    }

    /**
     * 有则刷新时间，无则插入。
     *
     * <p>并发下两个请求可能同时判定「不存在」然后都去 insert，撞唯一索引。
     * 那不是错误——说明另一路已经建好了，这里退化成刷新时间即可。
     */
    private void upsert(Long userId, String spaceType, String path, String kind) {
        EsFileMark existing = markMapper.selectOne(Wrappers.<EsFileMark>lambdaQuery()
            .eq(EsFileMark::getUserId, userId)
            .eq(EsFileMark::getSpaceType, spaceType)
            .eq(EsFileMark::getFilePath, path)
            .eq(EsFileMark::getKind, kind));
        LocalDateTime now = LocalDateTime.now();
        if (existing != null) {
            existing.setMarkedAt(now);
            markMapper.updateById(existing);
            return;
        }
        EsFileMark row = new EsFileMark();
        row.setUserId(userId);
        row.setSpaceType(spaceType);
        row.setFilePath(path);
        row.setKind(kind);
        row.setMarkedAt(now);
        try {
            markMapper.insert(row);
        } catch (DuplicateKeyException ex) {
            log.debug("标记已被并发写入，退化为刷新时间：user={} path={} kind={}", userId, path, kind);
            EsFileMark raced = markMapper.selectOne(Wrappers.<EsFileMark>lambdaQuery()
                .eq(EsFileMark::getUserId, userId)
                .eq(EsFileMark::getSpaceType, spaceType)
                .eq(EsFileMark::getFilePath, path)
                .eq(EsFileMark::getKind, kind));
            if (raced != null) {
                raced.setMarkedAt(now);
                markMapper.updateById(raced);
            }
        }
    }

    /** 只保留最近 {@link #RECENT_KEEP} 条；多出来的按时间倒序之后的部分删掉 */
    private void trimRecent(Long userId, String spaceType) {
        List<EsFileMark> all = markMapper.selectList(Wrappers.<EsFileMark>lambdaQuery()
            .eq(EsFileMark::getUserId, userId)
            .eq(EsFileMark::getSpaceType, spaceType)
            .eq(EsFileMark::getKind, EsFileMark.KIND_RECENT)
            .orderByDesc(EsFileMark::getMarkedAt));
        if (all.size() <= RECENT_KEEP) {
            return;
        }
        List<Long> stale = all.subList(RECENT_KEEP, all.size()).stream()
            .map(EsFileMark::getMarkId)
            .toList();
        markMapper.deleteByIds(stale);
    }
}
