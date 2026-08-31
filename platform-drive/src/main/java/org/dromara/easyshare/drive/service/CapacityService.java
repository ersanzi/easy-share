package org.dromara.easyshare.drive.service;

import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.dromara.easyshare.drive.DriveProperties;
import org.springframework.stereotype.Service;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;

/**
 * 物理容量探测。
 * <p>
 * 存在的理由：逐空间配额挡不住「承诺总量超过物理磁盘」。没有这一层，管理员可以给
 * 20 个账号各分 100 GB，系统全部接受，写到磁盘满时对象存储直接报错——
 * 而用户看到的却是「配额还剩 80 GB」。配额数字变成谎言是最糟的失败模式。
 * <p>
 * 刻意不落库：物理可用量随宿主磁盘变化，存下来必然过时。每次探测，缓存很短。
 *
 * @author EasyShare
 */
@Slf4j
@Service
@RequiredArgsConstructor
public class CapacityService {

    /** 探测结果的缓存时长（毫秒）。df 不便宜，但也不能拿太旧的数据判定。 */
    private static final long CACHE_MILLIS = 60_000L;

    private final DriveProperties properties;

    private volatile long cachedUsable = -1L;
    private volatile long cachedAt = 0L;

    /**
     * 池上限是否启用。未配置 capacity-path 时不启用，行为与引入本类之前完全一致。
     *
     * @return 是否启用
     */
    public boolean enabled() {
        String path = properties.getCapacityPath();
        return path != null && !path.isBlank();
    }

    /**
     * 探测到的物理可用字节数；未启用或探测失败返回 -1。
     *
     * @return 可用字节数，或 -1
     */
    public long usableBytes() {
        if (!enabled()) {
            return -1L;
        }
        long now = System.currentTimeMillis();
        if (cachedUsable >= 0 && now - cachedAt < CACHE_MILLIS) {
            return cachedUsable;
        }
        long usable = probe(properties.getCapacityPath());
        cachedUsable = usable;
        cachedAt = now;
        return usable;
    }

    /**
     * 扣掉预留水位后，云盘还能用的字节数；未启用返回 -1。
     *
     * @return 可用于云盘的字节数，或 -1
     */
    public long poolBytes() {
        long usable = usableBytes();
        if (usable < 0) {
            return -1L;
        }
        long pool = usable - properties.getReservedBytes().toBytes();
        return Math.max(pool, 0L);
    }

    /**
     * 探测某路径所在卷的可用空间。
     * <p>
     * 路径不存在时向上找最近的存在的父目录：配置里给的可能是尚未创建的数据目录，
     * 但它所在的卷是确定的，这时仍应能探测出容量，而不是直接放弃。
     */
    private long probe(String raw) {
        try {
            Path path = Paths.get(raw);
            while (path != null && !Files.exists(path)) {
                path = path.getParent();
            }
            if (path == null) {
                log.warn("容量探测：路径及其父目录均不存在，池上限本次不生效: {}", raw);
                return -1L;
            }
            return Files.getFileStore(path).getUsableSpace();
        } catch (IOException | RuntimeException e) {
            // 探测失败不该阻断上传：宁可这次不判池上限，也不能让云盘整体不可用
            log.warn("容量探测失败，池上限本次不生效: {}", e.getMessage());
            return -1L;
        }
    }
}
