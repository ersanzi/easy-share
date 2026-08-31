package org.dromara.easyshare.drive;

import org.dromara.common.core.exception.ServiceException;

/**
 * 对象键的用户命名空间与归属校验（ADR-0007 不变量 2，修 KI-3）。
 * <p>
 * 全部为纯函数，不依赖 Spring，便于单测覆盖越权路径。
 * 客户端永远只发相对路径（如 {@code photos/2024/img.jpg}），
 * 绝对键由服务端拼 {@code users/{userId}/} 前缀得出——客户端无法自己指定前缀，
 * 因此无法构造跨用户键。
 *
 * @author EasyShare
 */
public final class DriveKeys {

    private DriveKeys() {
    }

    /**
     * 用户命名空间前缀。
     *
     * @param userId 用户 ID
     * @return 形如 {@code users/1/} 的前缀
     */
    public static String userPrefix(Long userId) {
        if (userId == null) {
            throw new ServiceException("未登录，无法定位用户空间");
        }
        return "users/" + userId + "/";
    }

    /**
     * 共享空间前缀。共享空间是全局单例，不带账号标识——谁能读写由 es_space_member 授权决定，
     * 不靠键前缀隔离。
     *
     * @return {@code shared/}
     */
    public static String sharedPrefix() {
        return "shared/";
    }

    /**
     * 由前缀与相对路径拼绝对键。前缀只能由服务端产出（{@link #userPrefix} /
     * {@link #sharedPrefix}），客户端永远只发相对路径。
     * <p>
     * 刻意不与 {@link #absoluteKey(Long, String)} 做重载：两者只差首参类型，
     * 一个是用户 ID 一个是前缀，重载既会在 {@code null} 实参上产生歧义，
     * 也容易把 ID 当前缀传错。
     *
     * @param prefix   服务端产出的空间前缀
     * @param relative 客户端相对路径
     * @return 绝对对象键
     */
    public static String keyAt(String prefix, String relative) {
        return prefix + normalizeRelative(relative);
    }

    /**
     * 从绝对对象键还原相对路径，并强制校验前缀归属。
     *
     * @param prefix      期望的空间前缀
     * @param absoluteKey 绝对对象键
     * @return 相对路径
     */
    public static String relativeOfPrefix(String prefix, String absoluteKey) {
        if (absoluteKey == null || !absoluteKey.startsWith(prefix)) {
            throw new ServiceException("对象不属于该空间");
        }
        return absoluteKey.substring(prefix.length());
    }

    /**
     * 规范化客户端传入的相对路径，拒绝一切可能逃出用户空间的写法。
     *
     * @param raw 客户端相对路径
     * @return 规范化后的相对路径
     */
    public static String normalizeRelative(String raw) {
        if (raw == null || raw.isBlank()) {
            throw new ServiceException("文件路径不能为空");
        }
        // Windows 客户端可能传反斜杠，统一为 /
        String path = raw.trim().replace('\\', '/');
        if (path.startsWith("/")) {
            throw new ServiceException("文件路径不能以 / 开头");
        }
        // 折叠重复斜杠，避免 a//b 与 a/b 指向不同键
        while (path.contains("//")) {
            path = path.replace("//", "/");
        }
        if (path.endsWith("/")) {
            throw new ServiceException("文件路径不能以 / 结尾");
        }
        for (String segment : path.split("/")) {
            if (segment.isEmpty() || ".".equals(segment) || "..".equals(segment)) {
                throw new ServiceException("文件路径包含非法片段: " + raw);
            }
        }
        for (int i = 0; i < path.length(); i++) {
            if (path.charAt(i) < 0x20 || path.charAt(i) == 0x7F) {
                throw new ServiceException("文件路径包含控制字符");
            }
        }
        return path;
    }

    /**
     * 由登录用户与相对路径推导绝对对象键。
     *
     * @param userId   当前登录用户 ID
     * @param relative 客户端相对路径
     * @return 绝对对象键
     */
    public static String absoluteKey(Long userId, String relative) {
        return userPrefix(userId) + normalizeRelative(relative);
    }

    /**
     * 从绝对对象键还原相对路径，并强制校验归属。
     *
     * @param userId      当前登录用户 ID
     * @param absoluteKey 绝对对象键
     * @return 相对路径
     */
    public static String relativeOf(Long userId, String absoluteKey) {
        String prefix = userPrefix(userId);
        if (absoluteKey == null || !absoluteKey.startsWith(prefix)) {
            throw new ServiceException("对象不属于当前用户");
        }
        return absoluteKey.substring(prefix.length());
    }
}
