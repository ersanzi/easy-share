package org.dromara.easyshare.drive.service;

import com.baomidou.mybatisplus.core.toolkit.Wrappers;
import lombok.RequiredArgsConstructor;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.DriveKeys;
import org.dromara.easyshare.drive.DriveObject;
import org.dromara.easyshare.drive.DriveStorage;
import org.dromara.easyshare.drive.domain.AppManifestVo;
import org.dromara.easyshare.drive.domain.AppReleaseVo;
import org.dromara.easyshare.drive.domain.EsAppRelease;
import org.dromara.easyshare.drive.domain.EsAppReleaseAsset;
import org.dromara.easyshare.drive.domain.UploadVo;
import org.dromara.easyshare.drive.mapper.EsAppReleaseAssetMapper;
import org.dromara.easyshare.drive.mapper.EsAppReleaseMapper;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.time.LocalDateTime;
import java.time.format.DateTimeFormatter;
import java.util.Comparator;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Set;
import java.util.regex.Pattern;
import java.util.stream.Collectors;

/**
 * 客户端在线升级的发布管理。
 * <p>
 * 版本与资产放 PG，安装包本体放 RustFS（releases/{version}/ 前缀），控制面只签发
 * 预签名 URL——与云盘同一套哲学：字节不经过控制面（ADR-0007）。
 * <p>
 * 生命周期：prepareUpload（建 pending 资产 + 预签名 PUT）→ 发布方直传 →
 * publishAsset（校验对象存在且大小一致，置已发布）→ 客户端 latest / download-url。
 * 回滚 = deleteRelease（删记录 + 删对象）。
 *
 * @author EasyShare
 */
@Service
@RequiredArgsConstructor
public class AppReleaseService {

    private final EsAppReleaseMapper releaseMapper;
    private final EsAppReleaseAssetMapper assetMapper;
    private final DriveStorage storage;

    /** 版本号：数字段 + 可选预发布后缀，如 0.1.0、0.1.0-preview.1 */
    private static final Pattern VERSION_PATTERN = Pattern.compile("^\\d+(\\.\\d+)*(-[0-9A-Za-z.\\-]+)?$");

    /** 文件名：单段、无路径分隔——拼对象键时不能逃出 releases/{version}/ 前缀 */
    private static final Pattern FILENAME_PATTERN = Pattern.compile("^[A-Za-z0-9._-]{1,120}$");

    private static final Pattern SHA256_PATTERN = Pattern.compile("^[0-9a-fA-F]{64}$");

    private static final Set<String> PLATFORMS = Set.of(
        EsAppReleaseAsset.PLATFORM_WINDOWS, EsAppReleaseAsset.PLATFORM_MACOS);

    private static final Set<String> KINDS = Set.of(
        EsAppReleaseAsset.KIND_INSTALLER, EsAppReleaseAsset.KIND_DMG, EsAppReleaseAsset.KIND_ZIP);

    private static final String RELEASE_PREFIX = "releases/";

    /** 对外时间格式（清单 publishedAt / 管理列表 createTime） */
    private static final DateTimeFormatter TIME_FORMAT = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss");

    /**
     * 上传准备：建/复用版本与资产（pending），返回预签名 PUT URL。
     * 同版本同平台同类型重传是「覆盖发布」：复用资产行、对象同名覆盖。
     *
     * @param bo 上传请求
     * @return 资产 ID 与预签名 URL
     */
    @Transactional(rollbackFor = Exception.class)
    public UploadVo prepareUpload(UploadCommand bo) {
        String version = requireMatch(VERSION_PATTERN, bo.version(), "版本号格式非法（示例 0.1.0 / 0.1.0-preview.1）");
        String filename = requireMatch(FILENAME_PATTERN, bo.filename(), "文件名只允许字母数字与 . _ - 且不含路径");
        String sha256 = requireMatch(SHA256_PATTERN, bo.sha256(), "sha256 必须是 64 位十六进制");
        String platform = requirePlatform(bo.platform());
        String kind = requireKind(bo.kind());
        if (bo.sizeBytes() == null || bo.sizeBytes() <= 0) {
            throw new ServiceException("文件大小必须为正数");
        }

        EsAppRelease release = releaseMapper.selectOne(Wrappers.<EsAppRelease>lambdaQuery()
            .eq(EsAppRelease::getVersion, version));
        if (release == null) {
            release = new EsAppRelease();
            release.setVersion(version);
            release.setNotes(bo.notes());
            releaseMapper.insert(release);
        } else if (bo.notes() != null && !bo.notes().isBlank()) {
            // 重传时允许顺带更新说明；不传则保留原文
            release.setNotes(bo.notes());
            releaseMapper.updateById(release);
        }

        String prefix = prefixOf(version);
        EsAppReleaseAsset asset = assetMapper.selectOne(Wrappers.<EsAppReleaseAsset>lambdaQuery()
            .eq(EsAppReleaseAsset::getReleaseId, release.getReleaseId())
            .eq(EsAppReleaseAsset::getPlatform, platform)
            .eq(EsAppReleaseAsset::getKind, kind));
        if (asset == null) {
            asset = new EsAppReleaseAsset();
            asset.setReleaseId(release.getReleaseId());
        }
        asset.setPlatform(platform);
        asset.setKind(kind);
        asset.setFilename(filename);
        asset.setSizeBytes(bo.sizeBytes());
        asset.setSha256(sha256.toLowerCase());
        asset.setObjectKey(DriveKeys.keyAt(prefix, filename));
        asset.setStatus(EsAppReleaseAsset.STATUS_PENDING);
        if (asset.getId() == null) {
            assetMapper.insert(asset);
        } else {
            assetMapper.updateById(asset);
        }
        return new UploadVo(String.valueOf(asset.getId()), storage.presignPutAt(prefix, filename));
    }

    /**
     * 发布资产：校验对象真实存在且大小一致后置已发布。
     * 中断/失败的上传不允许进入 latest 清单——校验是发布与上传之间唯一的关卡。
     *
     * @param assetId 资产 ID
     */
    public void publishAsset(Long assetId) {
        EsAppReleaseAsset asset = requireAsset(assetId);
        if (EsAppReleaseAsset.STATUS_PUBLISHED.equals(asset.getStatus())) {
            return;
        }
        DriveObject uploaded = storage.listAt(prefixOfVersion(asset)).stream()
            .filter(o -> asset.getFilename().equals(o.path()))
            .findFirst()
            .orElseThrow(() -> new ServiceException(
                "对象存储中找不到 " + asset.getObjectKey() + "，请先完成直传再发布"));
        if (uploaded.size() != asset.getSizeBytes()) {
            throw new ServiceException(String.format(
                "对象大小与清单不一致：登记 %d 字节，实际上传 %d 字节", asset.getSizeBytes(), uploaded.size()));
        }
        asset.setStatus(EsAppReleaseAsset.STATUS_PUBLISHED);
        assetMapper.updateById(asset);
    }

    /**
     * 某平台的最新清单：已发布资产中按版本首次发布时间取最新。
     *
     * @param platform windows / macos
     * @return 清单；该平台从未发布过则为 null（客户端据此提示「暂无发布」）
     */
    public AppManifestVo latest(String platform) {
        String target = requirePlatform(platform);
        List<EsAppReleaseAsset> published = assetMapper.selectList(Wrappers.<EsAppReleaseAsset>lambdaQuery()
            .eq(EsAppReleaseAsset::getPlatform, target)
            .eq(EsAppReleaseAsset::getStatus, EsAppReleaseAsset.STATUS_PUBLISHED));
        if (published.isEmpty()) {
            return null;
        }
        Map<Long, List<EsAppReleaseAsset>> byRelease = published.stream()
            .collect(Collectors.groupingBy(EsAppReleaseAsset::getReleaseId));
        EsAppRelease release = byRelease.keySet().stream()
            .map(releaseMapper::selectById)
            .filter(Objects::nonNull)
            .max(Comparator.comparing(EsAppRelease::getCreateTime,
                    Comparator.nullsFirst(Comparator.naturalOrder()))
                .thenComparing(EsAppRelease::getReleaseId))
            .orElseThrow(() -> new ServiceException("发布记录缺失，请检查 es_app_release 表"));
        return new AppManifestVo(release.getVersion(), release.getNotes(),
            formatTime(release.getCreateTime()),
            byRelease.get(release.getReleaseId()).stream().map(AppReleaseService::toAssetVo).toList());
    }

    /**
     * 解析资产的下载 URL。预签名 GET 有效期短（默认 10 分钟），客户端现取现用。
     *
     * @param assetId 资产 ID
     * @return 预签名 GET URL
     */
    public String resolveDownloadUrl(Long assetId) {
        EsAppReleaseAsset asset = requireAsset(assetId);
        if (!EsAppReleaseAsset.STATUS_PUBLISHED.equals(asset.getStatus())) {
            throw new ServiceException("该资产未发布");
        }
        return storage.presignGetAt(objectPrefix(asset.getObjectKey()), asset.getFilename());
    }

    /**
     * 管理端版本列表（新在前），含各版本名下全部资产。
     *
     * @return 版本视图列表
     */
    public List<AppReleaseVo> listAll() {
        List<EsAppRelease> releases = releaseMapper.selectList(Wrappers.<EsAppRelease>lambdaQuery()
            .orderByDesc(EsAppRelease::getCreateTime)
            .orderByDesc(EsAppRelease::getReleaseId));
        if (releases.isEmpty()) {
            return List.of();
        }
        Map<Long, List<EsAppReleaseAsset>> byRelease = assetMapper.selectList(
                Wrappers.<EsAppReleaseAsset>lambdaQuery()).stream()
            .collect(Collectors.groupingBy(EsAppReleaseAsset::getReleaseId));
        return releases.stream()
            .map(r -> new AppReleaseVo(String.valueOf(r.getReleaseId()), r.getVersion(), r.getNotes(),
                formatTime(r.getCreateTime()),
                byRelease.getOrDefault(r.getReleaseId(), List.of()).stream()
                    .map(AppReleaseService::toAssetVo).toList()))
            .toList();
    }

    /**
     * 删除版本（回滚）：先删对象再删记录，对象删除失败则整体回滚——记录留着便于重试。
     *
     * @param releaseId 版本 ID
     */
    @Transactional(rollbackFor = Exception.class)
    public void deleteRelease(Long releaseId) {
        List<EsAppReleaseAsset> assets = assetMapper.selectList(Wrappers.<EsAppReleaseAsset>lambdaQuery()
            .eq(EsAppReleaseAsset::getReleaseId, releaseId));
        for (EsAppReleaseAsset asset : assets) {
            // S3 删除幂等：对象不存在也返回成功
            storage.deleteAt(objectPrefix(asset.getObjectKey()), asset.getFilename());
            assetMapper.deleteById(asset.getId());
        }
        releaseMapper.deleteById(releaseId);
    }

    /**
     * 上传准备请求。
     *
     * @param version   版本号
     * @param platform  windows / macos
     * @param kind      installer / dmg / zip
     * @param filename  原始文件名
     * @param sizeBytes 字节数
     * @param sha256    文件 SHA256（发布方本地计算）
     * @param notes     更新说明（可空）
     */
    public record UploadCommand(
        String version,
        String platform,
        String kind,
        String filename,
        Long sizeBytes,
        String sha256,
        String notes
    ) {
    }

    private EsAppReleaseAsset requireAsset(Long assetId) {
        EsAppReleaseAsset asset = assetMapper.selectById(assetId);
        if (asset == null) {
            throw new ServiceException("资产不存在: " + assetId);
        }
        return asset;
    }

    private static AppManifestVo.AssetVo toAssetVo(EsAppReleaseAsset asset) {
        return new AppManifestVo.AssetVo(String.valueOf(asset.getId()), asset.getPlatform(), asset.getKind(),
            asset.getFilename(), asset.getSizeBytes(), asset.getSha256(), asset.getStatus());
    }

    private static String prefixOf(String version) {
        return RELEASE_PREFIX + version + "/";
    }

    private static String prefixOfVersion(EsAppReleaseAsset asset) {
        return objectPrefix(asset.getObjectKey());
    }

    /** 对象键 releases/{version}/{filename} → 前缀 releases/{version}/ */
    private static String objectPrefix(String objectKey) {
        int slash = objectKey.lastIndexOf('/');
        return slash >= 0 ? objectKey.substring(0, slash + 1) : RELEASE_PREFIX;
    }

    private static String requirePlatform(String raw) {
        String value = raw == null ? "" : raw.trim();
        if (!PLATFORMS.contains(value)) {
            throw new ServiceException("platform 只允许 windows 或 macos");
        }
        return value;
    }

    private static String requireKind(String raw) {
        String value = raw == null ? "" : raw.trim();
        if (!KINDS.contains(value)) {
            throw new ServiceException("kind 只允许 installer、dmg 或 zip");
        }
        return value;
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
