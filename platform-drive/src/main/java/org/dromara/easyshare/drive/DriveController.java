package org.dromara.easyshare.drive;

import cn.dev33.satoken.annotation.SaCheckLogin;
import jakarta.validation.constraints.NotBlank;
import lombok.RequiredArgsConstructor;
import org.dromara.common.core.domain.R;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.domain.EsSpace;
import org.dromara.easyshare.drive.service.SpaceService;
import org.dromara.easyshare.drive.service.SpaceUsageService;
import org.springframework.validation.annotation.Validated;
import org.springframework.web.bind.annotation.*;

import java.util.List;

/**
 * EasyShare 云盘存储授权接口（ADR-0007 决策 3）。
 * <p>
 * 隔离的强制点在这里：用户 ID 一律取自登录态（{@link LoginHelper#getUserId()}），
 * **从不接受客户端传入的用户标识**，因此客户端无法伪造他人身份或构造跨用户对象键。
 * 这也是不直接复用 RuoYi {@code /resource/oss} 的原因——那套的 {@code createBy}
 * 过滤是"客户端传了才生效"，且下载/删除无归属校验。
 * <p>
 * 客户端只能指定「个人还是共享」（{@code space}）与相对路径，前缀由
 * {@link SpaceService} 校验权限后产出——客户端无法自己拼前缀，也就无法跨空间。
 *
 * @author EasyShare
 */
@Validated
@SaCheckLogin
@RestController
@RequiredArgsConstructor
@RequestMapping("/easyshare/drive")
public class DriveController {

    private final DriveStorage storage;
    private final SpaceService spaceService;
    private final SpaceUsageService usageService;

    /**
     * 列举指定空间内的文件。默认个人空间。
     *
     * @param space personal / shared
     * @return 相对路径列表
     */
    @GetMapping("/objects")
    public R<List<DriveObject>> objects(
        @RequestParam(defaultValue = EsSpace.TYPE_PERSONAL) String space) {
        return R.ok(storage.listAt(readablePrefix(space)));
    }

    /**
     * 签发上传用预签名 URL，签发前校验空间配额。
     * <p>
     * 这是配额唯一能强制的时机：URL 签出后客户端直传 RustFS，字节不经控制面。
     * 因此配额是**软上限**——客户端申报的 size 可能小于实际写入量，且一次签名 15 分钟内
     * 有效。真实用量以 RustFS 为准，超额只能在下一次签发时被拦住。
     *
     * @param body 相对路径、空间、申报大小
     * @return 预签名 URL
     */
    @PostMapping("/presign-put")
    public R<PresignVo> presignPut(@Validated @RequestBody PutBo body) {
        Long userId = LoginHelper.getUserId();
        long size = body.size() == null ? 0L : body.size();
        String prefix = EsSpace.TYPE_SHARED.equals(body.space())
            ? spaceService.checkSharedWritable(userId, size)
            : spaceService.checkPersonalWritable(userId, size);
        // 签发即视为将写入：作废缓存，下一次校验重新聚合真实用量
        usageService.invalidate(prefix);
        return R.ok(new PresignVo(storage.presignPutAt(prefix, body.path()), body.path()));
    }

    /**
     * 签发下载用预签名 URL。
     *
     * @param body 相对路径与空间
     * @return 预签名 URL
     */
    @PostMapping("/presign-get")
    public R<PresignVo> presignGet(@Validated @RequestBody PathBo body) {
        String prefix = readablePrefix(body.space());
        return R.ok(new PresignVo(storage.presignGetAt(prefix, body.path()), body.path()));
    }

    /**
     * 删除指定空间内的文件。共享空间需要写权限。
     *
     * @param body 相对路径与空间
     * @return 操作结果
     */
    @DeleteMapping("/object")
    public R<Void> delete(@Validated @RequestBody PathBo body) {
        Long userId = LoginHelper.getUserId();
        String prefix = EsSpace.TYPE_SHARED.equals(body.space())
            ? spaceService.checkSharedWritable(userId, 0L)
            : DriveKeys.userPrefix(userId);
        storage.deleteAt(prefix, body.path());
        usageService.invalidate(prefix);
        return R.ok();
    }

    /**
     * 取可读空间的前缀。个人空间无需授权（自己的），共享空间要有成员行。
     */
    private String readablePrefix(String space) {
        Long userId = LoginHelper.getUserId();
        return EsSpace.TYPE_SHARED.equals(space)
            ? spaceService.checkSharedReadable(userId)
            : DriveKeys.userPrefix(userId);
    }

    /**
     * 相对路径请求体。
     *
     * @param path  空间内的相对路径
     * @param space personal / shared，空则按个人空间
     */
    public record PathBo(
        @NotBlank(message = "文件路径不能为空") String path,
        String space) {
    }

    /**
     * 上传签发请求体。
     *
     * @param path  空间内的相对路径
     * @param space personal / shared，空则按个人空间
     * @param size  申报的文件字节数，用于配额判定
     */
    public record PutBo(
        @NotBlank(message = "文件路径不能为空") String path,
        String space,
        Long size) {
    }

    /**
     * 预签名结果。
     *
     * @param url  预签名 URL
     * @param path 对应的相对路径
     */
    public record PresignVo(String url, String path) {
    }
}
