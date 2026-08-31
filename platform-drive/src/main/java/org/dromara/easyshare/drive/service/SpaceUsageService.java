package org.dromara.easyshare.drive.service;

import lombok.RequiredArgsConstructor;
import org.dromara.common.redis.utils.RedisUtils;
import org.dromara.easyshare.drive.DriveStorage;
import org.springframework.stereotype.Service;

import java.time.Duration;
import java.util.HashMap;
import java.util.Map;

/**
 * 空间已用量。
 * <p>
 * 用量的**真值只在 RustFS**：控制面签发预签名 URL 后客户端直传，字节不经过控制面，
 * 所以库里存不出准确的用量，只能实时 list 聚合。
 * <p>
 * list 全前缀有成本，按前缀缓存 60s。缓存偏小的窗口内配额可能被略微超出——这是
 * 刻意的取舍：预签名模型下配额本就只能是「软上限」（一次签发 15 分钟有效，
 * 期间客户端传多少字节控制面无从干预），把窗口从 15 分钟压到 60 秒已是可做到的上限。
 *
 * @author EasyShare
 */
@Service
@RequiredArgsConstructor
public class SpaceUsageService {

    private static final String CACHE_PREFIX = "easyshare:space:usage:";
    private static final String GROUPED_CACHE_KEY = "easyshare:space:usage-grouped";
    private static final Duration CACHE_TTL = Duration.ofSeconds(60);

    private final DriveStorage storage;

    /**
     * 取某前缀下的已用字节数，带 60s 缓存。
     *
     * @param prefix 空间前缀
     * @return 已用字节数
     */
    public long usedBytes(String prefix) {
        String key = CACHE_PREFIX + prefix;
        // 刻意收成 Number 再取值，不直接声明 Long：Redis 的序列化器会把小数值的 long
        // 还原成 Integer，直接按 Long 接会在**缓存命中时**抛 ClassCastException——
        // 首次调用（未命中）反而正常，是个只在第二次才现形的坑。
        Number cached = RedisUtils.getCacheObject(key);
        if (cached != null) {
            return cached.longValue();
        }
        long used = storage.sumBytes(prefix);
        RedisUtils.setCacheObject(key, used, CACHE_TTL);
        return used;
    }

    /**
     * 取全部空间的已用量，按前缀分组，带 60s 缓存。
     * <p>
     * 管理页要一屏显示每个账号的用量。逐账号读会是 N 次 list，这里单次遍历全桶分组；
     * 缓存同样必要——否则每次打开管理页都全量遍历一遍桶。
     *
     * @return 空间前缀 → 已用字节数
     */
    public Map<String, Long> usedBytesGrouped() {
        // 同 usedBytes：Map 的值经 Redis 往返后可能变成 Integer，逐个按 Number 归一，
        // 否则调用方取值时才炸，且只在缓存命中的那一次。
        Map<String, ? extends Number> cached = RedisUtils.getCacheObject(GROUPED_CACHE_KEY);
        if (cached != null) {
            Map<String, Long> normalized = new HashMap<>(cached.size());
            cached.forEach((prefix, value) -> normalized.put(prefix, value.longValue()));
            return normalized;
        }
        Map<String, Long> grouped = storage.sumBytesGrouped();
        RedisUtils.setCacheObject(GROUPED_CACHE_KEY, grouped, CACHE_TTL);
        return grouped;
    }

    /**
     * 作废某前缀的用量缓存。上传签发与删除后调用，让下一次读取拿到新值。
     * <p>
     * 分组缓存一并作废：它包含同一份数据的另一个视图，只清单个前缀会让管理页继续读到旧值。
     *
     * @param prefix 空间前缀
     */
    public void invalidate(String prefix) {
        RedisUtils.deleteObject(CACHE_PREFIX + prefix);
        RedisUtils.deleteObject(GROUPED_CACHE_KEY);
    }
}
