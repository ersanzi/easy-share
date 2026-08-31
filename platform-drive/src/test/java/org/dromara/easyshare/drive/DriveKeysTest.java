package org.dromara.easyshare.drive;

import org.dromara.common.core.exception.ServiceException;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

/**
 * 用户命名空间与越权防护测试（ADR-0007 不变量 2）。
 *
 * @author EasyShare
 */
class DriveKeysTest {

    @Test
    void absoluteKeyCarriesUserPrefix() {
        assertEquals("users/1/a.txt", DriveKeys.absoluteKey(1L, "a.txt"));
        assertEquals("users/2/photos/2024/img.jpg", DriveKeys.absoluteKey(2L, "photos/2024/img.jpg"));
    }

    @Test
    void backslashNormalizedToSlash() {
        assertEquals("users/1/photos/img.jpg", DriveKeys.absoluteKey(1L, "photos\\img.jpg"));
    }

    @Test
    void duplicateSlashCollapsed() {
        assertEquals("users/1/a/b.txt", DriveKeys.absoluteKey(1L, "a//b.txt"));
    }

    @ParameterizedTest
    @ValueSource(strings = {
        "../other/secret.txt",
        "a/../../b.txt",
        "/etc/passwd",
        "users/2/hack.txt/",
        "./a.txt",
        "a/./b.txt",
    })
    void rejectsEscapingPaths(String evil) {
        assertThrows(ServiceException.class, () -> DriveKeys.absoluteKey(1L, evil));
    }

    @Test
    void rejectsBlankAndControlChars() {
        assertThrows(ServiceException.class, () -> DriveKeys.absoluteKey(1L, ""));
        assertThrows(ServiceException.class, () -> DriveKeys.absoluteKey(1L, "   "));
        assertThrows(ServiceException.class, () -> DriveKeys.absoluteKey(1L, null));
        assertThrows(ServiceException.class, () -> DriveKeys.absoluteKey(1L, "a\nb.txt"));
        assertThrows(ServiceException.class, () -> DriveKeys.absoluteKey(1L, "a" + (char) 0 + "b.txt"));
        assertThrows(ServiceException.class, () -> DriveKeys.absoluteKey(1L, "a" + (char) 127 + "b.txt"));
    }

    /**
     * 空格与中文是合法文件名，不能被误杀。
     */
    @Test
    void allowsSpaceAndChineseFileName() {
        assertEquals("users/1/我的 文档.txt", DriveKeys.absoluteKey(1L, "我的 文档.txt"));
    }

    @Test
    void rejectsWhenNotLoggedIn() {
        assertThrows(ServiceException.class, () -> DriveKeys.absoluteKey(null, "a.txt"));
    }

    @Test
    void ownershipCheckRejectsOtherUsersObject() {
        assertEquals("a.txt", DriveKeys.relativeOf(1L, "users/1/a.txt"));
        // 关键用例：用户 1 不能拿到用户 2 的对象
        assertThrows(ServiceException.class, () -> DriveKeys.relativeOf(1L, "users/2/a.txt"));
        // 前缀相似也必须拒绝（users/1 vs users/11）
        assertThrows(ServiceException.class, () -> DriveKeys.relativeOf(1L, "users/11/a.txt"));
        assertThrows(ServiceException.class, () -> DriveKeys.relativeOf(1L, "other/a.txt"));
        assertThrows(ServiceException.class, () -> DriveKeys.relativeOf(1L, null));
    }

    @Test
    void sharedPrefixIsSeparateFromUserSpaces() {
        assertEquals("shared/", DriveKeys.sharedPrefix());
        // 共享空间与个人空间必须是互不包含的两个前缀，否则一边的授权会漏到另一边
        assertEquals("shared/a.txt", DriveKeys.keyAt(DriveKeys.sharedPrefix(), "a.txt"));
        assertEquals("users/1/a.txt", DriveKeys.keyAt(DriveKeys.userPrefix(1L), "a.txt"));
    }

    @Test
    void keyAtAppliesSamePathValidationAsUserKeys() {
        // 换了前缀不等于放松校验：逃逸写法在共享空间同样要被拒
        String shared = DriveKeys.sharedPrefix();
        assertThrows(ServiceException.class, () -> DriveKeys.keyAt(shared, "../users/1/a.txt"));
        assertThrows(ServiceException.class, () -> DriveKeys.keyAt(shared, "/a.txt"));
        assertThrows(ServiceException.class, () -> DriveKeys.keyAt(shared, ""));
        assertThrows(ServiceException.class, () -> DriveKeys.keyAt(shared, "a\nb.txt"));
        assertEquals("shared/a/b.txt", DriveKeys.keyAt(shared, "a\\b.txt"));
    }

    @Test
    void relativeOfPrefixRejectsCrossSpaceObject() {
        assertEquals("a.txt", DriveKeys.relativeOfPrefix("shared/", "shared/a.txt"));
        // 共享前缀不能取到个人空间的对象，反向同理
        assertThrows(ServiceException.class,
            () -> DriveKeys.relativeOfPrefix("shared/", "users/1/a.txt"));
        assertThrows(ServiceException.class,
            () -> DriveKeys.relativeOfPrefix("users/1/", "shared/a.txt"));
        assertThrows(ServiceException.class,
            () -> DriveKeys.relativeOfPrefix("shared/", null));
    }
}
