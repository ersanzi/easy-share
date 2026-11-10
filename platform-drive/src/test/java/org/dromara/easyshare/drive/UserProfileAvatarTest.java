package org.dromara.easyshare.drive;

import org.junit.jupiter.api.Test;

import java.nio.charset.StandardCharsets;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;

/**
 * 头像类型嗅探测试：只认文件头，不认客户端申报；SVG 与伪装有内容的都不放行。
 *
 * @author EasyShare
 */
class UserProfileAvatarTest {

    private static byte[] bytes(int... head) {
        byte[] data = new byte[16];
        for (int i = 0; i < head.length; i++) {
            data[i] = (byte) head[i];
        }
        return data;
    }

    @Test
    void detectsSupportedRasterFormats() {
        assertEquals("image/png", UserProfileController.detectImageMime(
            bytes(0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A)));
        assertEquals("image/jpeg", UserProfileController.detectImageMime(
            bytes(0xFF, 0xD8, 0xFF, 0xE0)));
        assertEquals("image/gif", UserProfileController.detectImageMime(
            bytes('G', 'I', 'F', '8', '9', 'a')));
        assertEquals("image/bmp", UserProfileController.detectImageMime(
            bytes('B', 'M', 0, 0)));
        byte[] webp = bytes('R', 'I', 'F', 'F', 0, 0, 0, 0, 'W', 'E', 'B', 'P');
        assertEquals("image/webp", UserProfileController.detectImageMime(webp));
    }

    @Test
    void rejectsSvgAndDisguisedFiles() {
        assertNull(UserProfileController.detectImageMime(
            "<svg xmlns=\"http://www.w3.org/2000/svg\"><script>alert(1)</script></svg>"
                .getBytes(StandardCharsets.UTF_8)));
        assertNull(UserProfileController.detectImageMime(
            "xx fake payload GIF89a".getBytes(StandardCharsets.UTF_8)));
        assertNull(UserProfileController.detectImageMime(new byte[0]));
        assertNull(UserProfileController.detectImageMime(null));
    }
}
