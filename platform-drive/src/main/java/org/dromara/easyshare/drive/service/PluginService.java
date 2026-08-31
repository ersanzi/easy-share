package org.dromara.easyshare.drive.service;

import com.baomidou.mybatisplus.core.toolkit.Wrappers;
import lombok.RequiredArgsConstructor;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.DriveKeys;
import org.dromara.easyshare.drive.DriveObject;
import org.dromara.easyshare.drive.DriveStorage;
import org.dromara.easyshare.drive.domain.EsPlugin;
import org.dromara.easyshare.drive.domain.EsPluginRelease;
import org.dromara.easyshare.drive.domain.EsPluginReleaseAsset;
import org.dromara.easyshare.drive.domain.PluginManifestVo;
import org.dromara.easyshare.drive.domain.UploadVo;
import org.dromara.easyshare.drive.mapper.EsPluginMapper;
import org.dromara.easyshare.drive.mapper.EsPluginReleaseAssetMapper;
import org.dromara.easyshare.drive.mapper.EsPluginReleaseMapper;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.time.LocalDateTime;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.regex.Pattern;
import java.util.stream.Collectors;

/**
 * 插件商城的发布与清单管理。
 * <p>
 * 平移 AppReleaseService 模式：插件/版本/资产放 PG，zip 本体放 RustFS
 * （plugins/{pluginId}/{version}/ 前缀），控制面只签发预签名 URL——字节不经过控制面。
 * <p>
 * 生命周期：prepareUpload（upsert 插件登记 + 建 pending 资产 + 预签名 PUT）→
 * 发布方直传 → publishAsset（校验对象存在且大小一致，置已发布）→ 客户端商城清单 /
 * 下载 URL。下架 = deleteRelease（删记录 + 删对象，已装客户端不受影响）。
 *
 * @author EasyShare
 */
@Service
@RequiredArgsConstructor
public class PluginService {

    private final EsPluginMapper pluginMapper;
    private final EsPluginReleaseMapper releaseMapper;
    private final EsPluginReleaseAssetMapper assetMapper;
    private final DriveStorage storage;

    /** 插件 ID：与客户端 manifest.id 同规则（小写字母开头，字母/数字/连字符，2~32 位） */
    private static final Pattern PLUGIN_ID_PATTERN = Pattern.compile("^[a-z][a-z0-9-]{1,31}$");

    /** 版本号：数字段 + 可选预发布后缀 */
    private static final Pattern VERSION_PATTERN = Pattern.compile("^\\d+(\\.\\d+)*(-[0-9A-Za-z.\\-]+)?$");

    /** 文件名：单段、无路径分隔——拼对象键时不能逃出 plugins/{id}/{version}/ 前缀 */
    private static final Pattern FILENAME_PATTERN = Pattern.compile("^[A-Za-z0-9._-]{1,120}$");

    private static final Pattern SHA256_PATTERN = Pattern.compile("^[0-9a-fA-F]{64}$");

    private static final String PLUGIN_PREFIX = "plugins/";

    private static final DateTimeFormatter TIME_FORMAT = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss");

    /**
     * 上传准备：upsert 插件登记（名称/说明/图标随发布刷新）+ 建/复用版本与资产（pending），
     * 返回预签名 PUT URL。同插件同版本重传是「覆盖发布」。
     */
    @Transactional(rollbackFor = Exception.class)
    public UploadVo prepareUpload(UploadCommand bo) {
        String pluginId = requireMatch(PLUGIN_ID_PATTERN, bo.pluginId(),
            "插件 ID 非法（小写字母开头，字母/数字/连字符，2~32 位）");
        String version = requireMatch(VERSION_PATTERN, bo.version(), "版本号格式非法（示例 1.0.0）");
        String filename = requireMatch(FILENAME_PATTERN, bo.filename(), "文件名只允许字母数字与 . _ - 且不含路径");
        String sha256 = requireMatch(SHA256_PATTERN, bo.sha256(), "sha256 必须是 64 位十六进制");
        if (bo.sizeBytes() == null || bo.sizeBytes() <= 0) {
            throw new ServiceException("文件大小必须为正数");
        }
        if (bo.name() == null || bo.name().isBlank()) {
            throw new ServiceException("插件名称不能为空");
        }

        // 插件登记：随发布 upsert（商城展示信息以最新发布为准）
        EsPlugin plugin = pluginMapper.selectById(pluginId);
        if (plugin == null) {
            plugin = new EsPlugin();
            plugin.setPluginId(pluginId);
        }
        plugin.setName(bo.name());
        if (bo.description() != null) plugin.setDescription(bo.description());
        if (bo.icon() != null) plugin.setIcon(bo.icon());
        if (bo.author() != null) plugin.setAuthor(bo.author());
        if (plugin.getCreateTime() == null) {
            pluginMapper.insert(plugin);
        } else {
            pluginMapper.updateById(plugin);
        }

        // 版本：同插件同版本复用（覆盖发布）
        EsPluginRelease release = releaseMapper.selectOne(Wrappers.<EsPluginRelease>lambdaQuery()
            .eq(EsPluginRelease::getPluginId, pluginId)
            .eq(EsPluginRelease::getVersion, version));
        if (release == null) {
            release = new EsPluginRelease();
            release.setPluginId(pluginId);
            release.setVersion(version);
            release.setNotes(bo.notes());
            releaseMapper.insert(release);
        } else if (bo.notes() != null && !bo.notes().isBlank()) {
            release.setNotes(bo.notes());
            releaseMapper.updateById(release);
        }

        // 资产：一个版本一个 zip
        String prefix = prefixOf(pluginId, version);
        EsPluginReleaseAsset asset = assetMapper.selectOne(Wrappers.<EsPluginReleaseAsset>lambdaQuery()
            .eq(EsPluginReleaseAsset::getReleaseId, release.getReleaseId()));
        if (asset == null) {
            asset = new EsPluginReleaseAsset();
            asset.setReleaseId(release.getReleaseId());
        }
        asset.setFilename(filename);
        asset.setSizeBytes(bo.sizeBytes());
        asset.setSha256(sha256.toLowerCase());
        asset.setObjectKey(DriveKeys.keyAt(prefix, filename));
        asset.setStatus(EsPluginReleaseAsset.STATUS_PENDING);
        if (asset.getId() == null) {
            assetMapper.insert(asset);
        } else {
            assetMapper.updateById(asset);
        }
        return new UploadVo(String.valueOf(asset.getId()), storage.presignPutAt(prefix, filename));
    }

    /**
     * 发布资产：校验对象真实存在且大小一致后置已发布（同 AppReleaseService#publishAsset）。
     */
    public void publishAsset(Long assetId) {
        EsPluginReleaseAsset asset = requireAsset(assetId);
        if (EsPluginReleaseAsset.STATUS_PUBLISHED.equals(asset.getStatus())) {
            return;
        }
        DriveObject uploaded = storage.listAt(objectPrefix(asset.getObjectKey())).stream()
            .filter(o -> asset.getFilename().equals(o.path()))
            .findFirst()
            .orElseThrow(() -> new ServiceException(
                "对象存储中找不到 " + asset.getObjectKey() + "，请先完成直传再发布"));
        if (uploaded.size() != asset.getSizeBytes()) {
            throw new ServiceException(String.format(
                "对象大小与清单不一致：登记 %d 字节，实际上传 %d 字节", asset.getSizeBytes(), uploaded.size()));
        }
        asset.setStatus(EsPluginReleaseAsset.STATUS_PUBLISHED);
        assetMapper.updateById(asset);
    }

    /**
     * 商城清单：全部「有已发布版本」的插件，各带最新已发布版本。
     */
    public List<PluginManifestVo> marketList() {
        List<EsPlugin> plugins = pluginMapper.selectList(Wrappers.<EsPlugin>lambdaQuery());
        if (plugins.isEmpty()) {
            return List.of();
        }
        List<PluginManifestVo> out = new ArrayList<>();
        for (EsPlugin plugin : plugins) {
            PluginManifestVo latest = latestOf(plugin);
            if (latest != null) {
                out.add(latest);
            }
        }
        out.sort(Comparator.comparing(PluginManifestVo::publishedAt,
            Comparator.nullsLast(Comparator.reverseOrder())));
        return out;
    }

    /**
     * 单插件的最新清单；从未发布过则为 null。
     */
    public PluginManifestVo latest(String pluginId) {
        String id = requireMatch(PLUGIN_ID_PATTERN, pluginId, "插件 ID 非法");
        EsPlugin plugin = pluginMapper.selectById(id);
        if (plugin == null) {
            return null;
        }
        return latestOf(plugin);
    }

    /**
     * 解析资产下载 URL。预签名 GET 有效期短（默认 10 分钟），客户端现取现用。
     */
    public String resolveDownloadUrl(Long assetId) {
        EsPluginReleaseAsset asset = requireAsset(assetId);
        if (!EsPluginReleaseAsset.STATUS_PUBLISHED.equals(asset.getStatus())) {
            throw new ServiceException("该资产未发布");
        }
        return storage.presignGetAt(objectPrefix(asset.getObjectKey()), asset.getFilename());
    }

    /**
     * 管理端：某插件全部版本（新在前）。pluginId 为空时返回全部插件全部版本。
     */
    public List<PluginManifestVo> listAll(String pluginId) {
        var query = Wrappers.<EsPluginRelease>lambdaQuery()
            .orderByDesc(EsPluginRelease::getCreateTime)
            .orderByDesc(EsPluginRelease::getReleaseId);
        if (pluginId != null && !pluginId.isBlank()) {
            query.eq(EsPluginRelease::getPluginId, pluginId);
        }
        List<EsPluginRelease> releases = releaseMapper.selectList(query);
        if (releases.isEmpty()) {
            return List.of();
        }
        Map<String, EsPlugin> plugins = pluginMapper.selectList(Wrappers.<EsPlugin>lambdaQuery())
            .stream().collect(Collectors.toMap(EsPlugin::getPluginId, p -> p));
        Map<Long, EsPluginReleaseAsset> assets = assetMapper.selectList(
                Wrappers.<EsPluginReleaseAsset>lambdaQuery()).stream()
            .collect(Collectors.toMap(EsPluginReleaseAsset::getReleaseId, a -> a));
        return releases.stream()
            .map(r -> toVo(plugins.get(r.getPluginId()), r, assets.get(r.getReleaseId())))
            .filter(Objects::nonNull)
            .toList();
    }

    /**
     * 删除版本（下架/回滚）：先删对象再删记录，对象删除失败则整体回滚——记录留着便于重试。
     * 已安装客户端不受影响（插件本地自足，仅停止更新推送）。
     */
    @Transactional(rollbackFor = Exception.class)
    public void deleteRelease(Long releaseId) {
        EsPluginRelease release = releaseMapper.selectById(releaseId);
        if (release == null) {
            throw new ServiceException("版本不存在: " + releaseId);
        }
        List<EsPluginReleaseAsset> assets = assetMapper.selectList(Wrappers.<EsPluginReleaseAsset>lambdaQuery()
            .eq(EsPluginReleaseAsset::getReleaseId, releaseId));
        for (EsPluginReleaseAsset asset : assets) {
            // S3 删除幂等：对象不存在也返回成功
            storage.deleteAt(objectPrefix(asset.getObjectKey()), asset.getFilename());
            assetMapper.deleteById(asset.getId());
        }
        releaseMapper.deleteById(releaseId);
    }

    /**
     * 上传准备请求。
     *
     * @param pluginId    插件 ID（manifest.id）
     * @param name        插件显示名
     * @param description 插件说明（可空，null 保持原值）
     * @param icon        图标（emoji 或包内路径，可空）
     * @param author      作者（可空）
     * @param version     版本号
     * @param filename    zip 文件名（单段）
     * @param sizeBytes   字节数
     * @param sha256      文件 SHA256
     * @param notes       更新说明（可空）
     */
    public record UploadCommand(
        String pluginId,
        String name,
        String description,
        String icon,
        String author,
        String version,
        String filename,
        Long sizeBytes,
        String sha256,
        String notes
    ) {
    }

    /** 某插件「最新已发布版本」的清单项；无已发布版本返回 null。 */
    private PluginManifestVo latestOf(EsPlugin plugin) {
        List<EsPluginRelease> releases = releaseMapper.selectList(
            Wrappers.<EsPluginRelease>lambdaQuery()
                .eq(EsPluginRelease::getPluginId, plugin.getPluginId()));
        if (releases.isEmpty()) {
            return null;
        }
        Map<Long, EsPluginReleaseAsset> published = assetMapper.selectList(
                Wrappers.<EsPluginReleaseAsset>lambdaQuery()
                    .eq(EsPluginReleaseAsset::getStatus, EsPluginReleaseAsset.STATUS_PUBLISHED))
            .stream().collect(Collectors.toMap(EsPluginReleaseAsset::getReleaseId, a -> a));
        EsPluginRelease latest = releases.stream()
            .filter(r -> published.containsKey(r.getReleaseId()))
            .max(Comparator.comparing(EsPluginRelease::getCreateTime,
                    Comparator.nullsFirst(Comparator.naturalOrder()))
                .thenComparing(EsPluginRelease::getReleaseId))
            .orElse(null);
        if (latest == null) {
            return null;
        }
        return toVo(plugin, latest, published.get(latest.getReleaseId()));
    }

    private static PluginManifestVo toVo(EsPlugin plugin, EsPluginRelease release, EsPluginReleaseAsset asset) {
        PluginManifestVo.AssetVo assetVo = asset == null ? null : new PluginManifestVo.AssetVo(
            String.valueOf(asset.getId()), asset.getFilename(), asset.getSizeBytes(), asset.getSha256());
        return new PluginManifestVo(
            release.getPluginId(),
            plugin == null ? release.getPluginId() : plugin.getName(),
            plugin == null ? null : plugin.getDescription(),
            plugin == null ? null : plugin.getIcon(),
            plugin == null ? null : plugin.getAuthor(),
            release.getVersion(),
            release.getNotes(),
            formatTime(release.getCreateTime()),
            assetVo
        );
    }

    private EsPluginReleaseAsset requireAsset(Long assetId) {
        EsPluginReleaseAsset asset = assetMapper.selectById(assetId);
        if (asset == null) {
            throw new ServiceException("资产不存在: " + assetId);
        }
        return asset;
    }

    private static String prefixOf(String pluginId, String version) {
        return PLUGIN_PREFIX + pluginId + "/" + version + "/";
    }

    /** 对象键 plugins/{id}/{version}/{filename} → 前缀 plugins/{id}/{version}/ */
    private static String objectPrefix(String objectKey) {
        int slash = objectKey.lastIndexOf('/');
        return slash >= 0 ? objectKey.substring(0, slash + 1) : PLUGIN_PREFIX;
    }

    private static String requireMatch(Pattern pattern, String raw, String message) {
        String value = raw == null ? "" : raw.trim();
        if (!pattern.matcher(value).matches()) {
            throw new ServiceException(message);
        }
        return value;
    }

    private static String formatTime(LocalDateTime time) {
        return time == null ? null : TIME_FORMAT.format(time);
    }
}
