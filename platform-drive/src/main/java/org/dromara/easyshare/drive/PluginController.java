package org.dromara.easyshare.drive;

import cn.dev33.satoken.annotation.SaCheckLogin;
import cn.dev33.satoken.annotation.SaCheckRole;
import cn.dev33.satoken.annotation.SaIgnore;
import jakarta.validation.constraints.NotBlank;
import jakarta.validation.constraints.NotNull;
import lombok.RequiredArgsConstructor;
import org.dromara.common.core.domain.R;
import org.dromara.easyshare.drive.domain.PluginManifestVo;
import org.dromara.easyshare.drive.domain.UploadVo;
import org.dromara.easyshare.drive.service.PluginService;
import org.springframework.validation.annotation.Validated;
import org.springframework.web.bind.annotation.*;

import java.util.List;
import java.util.Map;

/**
 * 插件商城接口（官方自营：superadmin 发布，客户端匿名浏览与安装）。
 * <p>
 * 分两段（同 AppReleaseController 的信任模型）：
 * <ul>
 *   <li>{@code /}（商城列表）、{@code /{id}/latest}、{@code /assets/{id}/url} —— 匿名可访问：
 *       插件包本就面向全部客户端公开。匿名靠 {@code @SaIgnore} + 路由级白名单
 *       （security.excludes，见 deploy/ruoyi-db/easyshare-drive.yml）。</li>
 *   <li>{@code /admin/**} —— 只有超级管理员能上传/发布/下架。</li>
 * </ul>
 *
 * @author EasyShare
 */
@Validated
@SaCheckLogin
@RestController
@RequiredArgsConstructor
@RequestMapping("/easyshare/plugins")
public class PluginController {

    private final PluginService pluginService;

    /**
     * 商城列表：全部有已发布版本的插件（各带最新版本）。
     *
     * @return 清单列表；从未发布过任何插件则为空数组
     */
    @SaIgnore
    @GetMapping
    public R<List<PluginManifestVo>> market() {
        return R.ok(pluginService.marketList());
    }

    /**
     * 单插件最新清单（客户端检查插件更新用）。
     *
     * @param pluginId 插件 ID
     * @return 清单；从未发布过则为 data=null
     */
    @SaIgnore
    @GetMapping("/{pluginId}/latest")
    public R<PluginManifestVo> latest(@PathVariable("pluginId") String pluginId) {
        return R.ok(pluginService.latest(pluginId));
    }

    /**
     * 解析插件包下载 URL。预签名 GET 有效期短，客户端安装前现取。
     *
     * @param assetId 资产 ID
     * @return {@code {"url": "..."}}
     */
    @SaIgnore
    @GetMapping("/assets/{assetId}/url")
    public R<Map<String, String>> downloadUrl(@PathVariable("assetId") Long assetId) {
        return R.ok(Map.of("url", pluginService.resolveDownloadUrl(assetId)));
    }

    /**
     * 上传准备：upsert 插件登记 + 建版本与资产记录，返回预签名 PUT URL，zip 直传 RustFS。
     *
     * @param body 上传请求
     * @return 资产 ID 与预签名 URL
     */
    @SaCheckRole("superadmin")
    @PostMapping("/admin/uploads")
    public R<UploadVo> prepareUpload(@Validated @RequestBody UploadBo body) {
        return R.ok(pluginService.prepareUpload(new PluginService.UploadCommand(
            body.pluginId(), body.name(), body.description(), body.icon(), body.author(),
            body.version(), body.filename(), body.sizeBytes(), body.sha256(), body.notes())));
    }

    /**
     * 发布资产：校验对象存在且大小一致后置已发布，进入商城清单。
     *
     * @param assetId 资产 ID
     * @return 操作结果
     */
    @SaCheckRole("superadmin")
    @PostMapping("/admin/assets/{assetId}/publish")
    public R<Void> publish(@PathVariable("assetId") Long assetId) {
        pluginService.publishAsset(assetId);
        return R.ok();
    }

    /**
     * 管理端版本列表（新在前），pluginId 可选过滤。
     *
     * @param pluginId 插件 ID（可空 = 全部）
     * @return 版本列表
     */
    @SaCheckRole("superadmin")
    @GetMapping("/admin/releases")
    public R<List<PluginManifestVo>> listReleases(
        @RequestParam(value = "pluginId", required = false) String pluginId) {
        return R.ok(pluginService.listAll(pluginId));
    }

    /**
     * 删除版本（下架/回滚）：对象与记录一并删除；已装客户端不受影响。
     *
     * @param releaseId 版本 ID
     * @return 操作结果
     */
    @SaCheckRole("superadmin")
    @DeleteMapping("/admin/releases/{releaseId}")
    public R<Void> deleteRelease(@PathVariable("releaseId") Long releaseId) {
        pluginService.deleteRelease(releaseId);
        return R.ok();
    }

    /**
     * 上传准备请求体。
     *
     * @param pluginId    插件 ID（manifest.id）
     * @param name        插件显示名
     * @param description 插件说明（可空）
     * @param icon        图标（可空）
     * @param author      作者（可空）
     * @param version     版本号（如 1.0.0）
     * @param filename    zip 文件名（单段，无路径分隔）
     * @param sizeBytes   字节数
     * @param sha256      文件 SHA256（发布方本地计算）
     * @param notes       更新说明（可空）
     */
    public record UploadBo(
        @NotBlank(message = "插件 ID 不能为空") String pluginId,
        @NotBlank(message = "插件名称不能为空") String name,
        String description,
        String icon,
        String author,
        @NotBlank(message = "版本号不能为空") String version,
        @NotBlank(message = "文件名不能为空") String filename,
        @NotNull(message = "文件大小不能为空") Long sizeBytes,
        @NotBlank(message = "sha256 不能为空") String sha256,
        String notes
    ) {
    }
}
