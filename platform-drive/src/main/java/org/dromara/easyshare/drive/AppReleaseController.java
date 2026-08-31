package org.dromara.easyshare.drive;

import cn.dev33.satoken.annotation.SaCheckLogin;
import cn.dev33.satoken.annotation.SaCheckRole;
import cn.dev33.satoken.annotation.SaIgnore;
import jakarta.validation.constraints.NotBlank;
import jakarta.validation.constraints.NotNull;
import lombok.RequiredArgsConstructor;
import org.dromara.common.core.domain.R;
import org.dromara.easyshare.drive.domain.AppManifestVo;
import org.dromara.easyshare.drive.domain.AppReleaseVo;
import org.dromara.easyshare.drive.domain.UploadVo;
import org.dromara.easyshare.drive.service.AppReleaseService;
import org.springframework.validation.annotation.Validated;
import org.springframework.web.bind.annotation.*;

import java.util.List;
import java.util.Map;

/**
 * 客户端在线升级接口。
 * <p>
 * 分两段：
 * <ul>
 *   <li>{@code /latest} 与 {@code /assets/{id}/url} —— 匿名可访问：升级检查先于登录发生
 *       （启动自动检查时用户可能还没登录）。匿名靠 Sa-Token 的 {@code @SaIgnore} 放开
 *       注解检查 + 路由级白名单（security.excludes，见 deploy/ruoyi-db/easyshare-drive.yml）；
 *       安装包本就公开，接受未认证拉取（频控留后续）。</li>
 *   <li>{@code /admin/**} —— 只有超级管理员能上传与发布/回滚，与空间管理同一信任级别。</li>
 * </ul>
 *
 * @author EasyShare
 */
@Validated
@SaCheckLogin
@RestController
@RequiredArgsConstructor
@RequestMapping("/easyshare/app")
public class AppReleaseController {

    private final AppReleaseService releaseService;

    /**
     * 某平台的最新升级清单。
     *
     * @param platform windows / macos
     * @return 清单；该平台从未发布过则为 data=null
     */
    @SaIgnore
    @GetMapping("/latest")
    public R<AppManifestVo> latest(@RequestParam("platform") String platform) {
        return R.ok(releaseService.latest(platform));
    }

    /**
     * 解析资产下载 URL。预签名 GET 有效期短，客户端每次下载前现取。
     *
     * @param assetId 资产 ID
     * @return {@code {"url": "..."}}
     */
    @SaIgnore
    @GetMapping("/assets/{assetId}/url")
    public R<Map<String, String>> downloadUrl(@PathVariable("assetId") Long assetId) {
        return R.ok(Map.of("url", releaseService.resolveDownloadUrl(assetId)));
    }

    /**
     * 上传准备：建/复用版本与资产记录，返回预签名 PUT URL，安装包直传 RustFS。
     *
     * @param body 上传请求
     * @return 资产 ID 与预签名 URL
     */
    @SaCheckRole("superadmin")
    @PostMapping("/admin/uploads")
    public R<UploadVo> prepareUpload(@Validated @RequestBody UploadBo body) {
        return R.ok(releaseService.prepareUpload(new AppReleaseService.UploadCommand(
            body.version(), body.platform(), body.kind(), body.filename(),
            body.sizeBytes(), body.sha256(), body.notes())));
    }

    /**
     * 发布资产：校验对象存在且大小一致后置已发布，进入 latest 清单。
     *
     * @param assetId 资产 ID
     * @return 操作结果
     */
    @SaCheckRole("superadmin")
    @PostMapping("/admin/assets/{assetId}/publish")
    public R<Void> publish(@PathVariable("assetId") Long assetId) {
        releaseService.publishAsset(assetId);
        return R.ok();
    }

    /**
     * 全部版本与资产，供管理端展示与回滚决策。
     *
     * @return 版本列表（新在前）
     */
    @SaCheckRole("superadmin")
    @GetMapping("/admin/releases")
    public R<List<AppReleaseVo>> listReleases() {
        return R.ok(releaseService.listAll());
    }

    /**
     * 删除版本（回滚）：对象与记录一并删除，客户端随即查不到该版本。
     *
     * @param releaseId 版本 ID
     * @return 操作结果
     */
    @SaCheckRole("superadmin")
    @DeleteMapping("/admin/releases/{releaseId}")
    public R<Void> deleteRelease(@PathVariable("releaseId") Long releaseId) {
        releaseService.deleteRelease(releaseId);
        return R.ok();
    }

    /**
     * 上传准备请求体。
     *
     * @param version   版本号（如 0.1.0 / 0.1.0-preview.1）
     * @param platform  windows / macos
     * @param kind      installer / dmg / zip
     * @param filename  原始文件名（单段，无路径分隔）
     * @param sizeBytes 字节数
     * @param sha256    文件 SHA256（发布方本地计算）
     * @param notes     更新说明（可空）
     */
    public record UploadBo(
        @NotBlank(message = "版本号不能为空") String version,
        @NotBlank(message = "平台不能为空") String platform,
        @NotBlank(message = "资产类型不能为空") String kind,
        @NotBlank(message = "文件名不能为空") String filename,
        @NotNull(message = "文件大小不能为空") Long sizeBytes,
        @NotBlank(message = "sha256 不能为空") String sha256,
        String notes
    ) {
    }
}
