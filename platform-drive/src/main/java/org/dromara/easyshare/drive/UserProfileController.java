package org.dromara.easyshare.drive;

import cn.dev33.satoken.annotation.SaIgnore;
import cn.dev33.satoken.annotation.SaCheckLogin;
import com.baomidou.mybatisplus.core.toolkit.Wrappers;
import lombok.RequiredArgsConstructor;
import org.dromara.common.core.domain.R;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.domain.EsUserProfile;
import org.dromara.easyshare.drive.mapper.EsUserProfileMapper;
import org.springframework.http.CacheControl;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.validation.annotation.Validated;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.multipart.MultipartFile;

import java.io.IOException;
import java.time.Duration;
import java.time.LocalDateTime;

/**
 * 成员个人资料：头像的读写。
 * <p>
 * 昵称与密码走 RuoYi 标准个人中心接口（任何登录用户都能改自己的），本控制器只补头像——
 * 标准通道绑定 sys_oss 且要 {@code system:oss:upload} 权限，普通成员用不了。
 * <p>
 * 头像地址对外公开（不带鉴权）：桌面端用 {@code <img>} 直接加载，标签发不了
 * Authorization 头。头像是组织内部的可公开信息，与插件商城同一定位；不上传敏感图片。
 *
 * @author EasyShare
 */
@Validated
@SaCheckLogin
@RestController
@RequiredArgsConstructor
@RequestMapping("/easyshare/profile")
public class UserProfileController {

    /** 头像大小上限：够放一张手机照片，又不至于把库撑大 */
    private static final long MAX_AVATAR_BYTES = 2L * 1024 * 1024;

    private final EsUserProfileMapper mapper;

    /**
     * 当前账号的头像地址（空串表示没传过）。
     *
     * @return 头像地址
     */
    @GetMapping("/mine")
    public R<AvatarVo> mine() {
        return R.ok(new AvatarVo(avatarUrlOf(metaOf(LoginHelper.getUserId()))));
    }

    /**
     * 上传/替换自己的头像。
     *
     * @param file 图片文件（png / jpeg / gif / webp / bmp）
     * @return 新头像地址
     */
    @PostMapping("/avatar")
    public R<AvatarVo> upload(@RequestParam("file") MultipartFile file) {
        Long userId = LoginHelper.getUserId();
        if (file == null || file.isEmpty()) {
            throw new ServiceException("请选择图片文件");
        }
        if (file.getSize() > MAX_AVATAR_BYTES) {
            throw new ServiceException("头像不能超过 2MB");
        }
        byte[] data;
        try {
            data = file.getBytes();
        } catch (IOException e) {
            throw new ServiceException("读取图片失败，请重试");
        }
        String mime = detectImageMime(data);
        if (mime == null) {
            throw new ServiceException("只支持 png / jpeg / gif / webp / bmp 图片");
        }
        EsUserProfile existing = mapper.selectById(userId);
        if (existing == null) {
            EsUserProfile row = new EsUserProfile();
            row.setUserId(userId);
            row.setAvatarMime(mime);
            row.setAvatarData(data);
            mapper.insert(row);
        } else {
            existing.setAvatarMime(mime);
            existing.setAvatarData(data);
            mapper.updateById(existing);
        }
        return R.ok(new AvatarVo(avatarUrlOf(metaOf(userId))));
    }

    /**
     * 清除自己的头像，回到昵称首字。
     *
     * @return 空响应
     */
    @DeleteMapping("/avatar")
    public R<Void> clear() {
        mapper.deleteById(LoginHelper.getUserId());
        return R.ok();
    }

    /**
     * 读某账号的头像字节。公开：头像要能被 {@code <img>} 直接引用。
     *
     * @param userId 账号 ID
     * @return 图片字节；未设置头像时 404
     */
    @SaIgnore
    @GetMapping("/avatars/{userId}")
    public ResponseEntity<byte[]> avatar(@PathVariable("userId") Long userId) {
        if (userId == null || userId <= 0) {
            return ResponseEntity.notFound().build();
        }
        EsUserProfile row = mapper.selectById(userId);
        if (row == null || row.getAvatarData() == null || row.getAvatarData().length == 0) {
            return ResponseEntity.notFound().build();
        }
        return ResponseEntity.ok()
            .contentType(MediaType.parseMediaType(row.getAvatarMime()))
            // 地址带内容版本号（?v=），内容变了 URL 就变，可以放心长缓存
            .cacheControl(CacheControl.maxAge(Duration.ofDays(7)).cachePublic())
            .body(row.getAvatarData());
    }

    /** 只取头像元信息，避免为了拼 URL 把 2MB 字节读出来 */
    private EsUserProfile metaOf(Long userId) {
        return mapper.selectOne(Wrappers.<EsUserProfile>lambdaQuery()
            .select(EsUserProfile::getUserId, EsUserProfile::getAvatarMime, EsUserProfile::getUpdateTime)
            .eq(EsUserProfile::getUserId, userId));
    }

    private static String avatarUrlOf(EsUserProfile meta) {
        if (meta == null || meta.getAvatarMime() == null) {
            return "";
        }
        LocalDateTime updated = meta.getUpdateTime();
        long version = updated == null ? 0L
            : updated.atZone(java.time.ZoneId.systemDefault()).toInstant().toEpochMilli();
        return "/easyshare/profile/avatars/" + meta.getUserId() + "?v=" + version;
    }

    /**
     * 按文件头判断图片类型。不信客户端申报的 Content-Type——
     * 伪装成图片的任意文件会被浏览器按图片渲染，等于给攻击者一个可控的字节出口。
     * 刻意不支持 SVG：它是可执行文档（内嵌脚本），当图片直出等于存储型 XSS。
     */
    static String detectImageMime(byte[] data) {
        if (data == null || data.length < 12) {
            return null;
        }
        if ((data[0] & 0xFF) == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G') {
            return "image/png";
        }
        if ((data[0] & 0xFF) == 0xFF && (data[1] & 0xFF) == 0xD8 && (data[2] & 0xFF) == 0xFF) {
            return "image/jpeg";
        }
        if (data[0] == 'G' && data[1] == 'I' && data[2] == 'F') {
            return "image/gif";
        }
        if (data[0] == 'R' && data[1] == 'I' && data[2] == 'F' && data[3] == 'F'
            && data[8] == 'W' && data[9] == 'E' && data[10] == 'B' && data[11] == 'P') {
            return "image/webp";
        }
        if (data[0] == 'B' && data[1] == 'M') {
            return "image/bmp";
        }
        return null;
    }

    /**
     * 头像地址响应。
     *
     * @param avatarUrl 相对控制面的地址；空串表示未设置
     */
    public record AvatarVo(String avatarUrl) {
    }
}
