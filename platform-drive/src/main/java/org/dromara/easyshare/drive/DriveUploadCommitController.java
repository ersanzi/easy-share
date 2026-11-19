package org.dromara.easyshare.drive;

import cn.dev33.satoken.annotation.SaCheckLogin;
import jakarta.validation.Valid;
import jakarta.validation.constraints.Min;
import jakarta.validation.constraints.NotBlank;
import jakarta.validation.constraints.NotNull;
import jakarta.validation.constraints.Pattern;
import lombok.RequiredArgsConstructor;
import org.dromara.common.core.domain.R;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.domain.EsSpace;
import org.dromara.easyshare.drive.service.DriveFileService;
import org.dromara.easyshare.drive.service.SpaceService;
import org.dromara.easyshare.drive.service.SpaceUsageService;
import org.springframework.transaction.annotation.Transactional;
import org.springframework.validation.annotation.Validated;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

import java.util.Objects;

/** 上传后的独立核验口：只 HEAD，不下载正文、不再次登记版本或计算配额增量。 */
@Validated
@SaCheckLogin
@RestController
@RequiredArgsConstructor
@RequestMapping("/easyshare/drive")
public class DriveUploadCommitController {
    private final DriveStorage storage;
    private final SpaceService spaceService;
    private final SpaceUsageService usageService;
    private final DriveFileService fileService;

    @PostMapping("/upload-commit")
    @Transactional(rollbackFor = Exception.class)
    public R<CommitVo> commit(@Valid @RequestBody CommitBo body) {
        Long userId = LoginHelper.getUserId();
        if (userId == null) {
            throw new ServiceException("未登录，无法核验上传");
        }
        if (body == null || body.size() == null || body.size() < 0
            || body.contentMd5() == null || !body.contentMd5().matches("(?i)[0-9a-f]{32}")) {
            throw new ServiceException("上传核验大小或 MD5 非法");
        }
        String path = DriveKeys.requireUserPath(body.path());
        String space = body.space() == null || body.space().isBlank() ? EsSpace.TYPE_PERSONAL : body.space();
        if (!EsSpace.TYPE_PERSONAL.equals(space) && !EsSpace.TYPE_SHARED.equals(space)) {
            throw new ServiceException("未知空间类型");
        }
        // 字节已经写入；只复核空间权限，不能把本次大小再加到已用容量上。
        String prefix = EsSpace.TYPE_SHARED.equals(space)
            ? spaceService.checkSharedWritable(userId, 0L)
            : spaceService.checkPersonalWritable(userId, 0L);
        Long ownerId = EsSpace.TYPE_SHARED.equals(space) ? EsFile.SHARED_OWNER : userId;
        // 与签发共用行锁，事务覆盖身份检查、HEAD 和缓存失效；不推进版本。
        EsFile row = fileService.lockActiveForPresign(space, ownerId, path);
        if (row == null || row.directoryEntry() || !EsFile.STATE_ACTIVE.equals(row.getState())
            || !Objects.equals(row.getSpaceType(), space) || !Objects.equals(row.getOwnerId(), ownerId)
            || !Objects.equals(row.getFilePath(), path) || !Objects.equals(row.getUploadBy(), userId)
            || row.getFileId() == null || row.getFileId() <= 0
            || row.getVersionId() == null || row.getVersionId() <= 0
            || (body.fileId() != null && !Objects.equals(body.fileId(), row.getFileId()))
            || (body.currentVersion() != null && !Objects.equals(body.currentVersion(), row.getVersionId()))
            || row.getContentMd5() == null || !body.contentMd5().equalsIgnoreCase(row.getContentMd5())) {
            throw new ServiceException("上传文件身份或当前版本不匹配，请刷新后重试");
        }
        DriveStorage.ObjectMetadata metadata = storage.metadataAt(prefix, path);
        if (metadata == null || metadata.size() != body.size()) {
            throw new ServiceException("上传对象缺失或真实大小不匹配");
        }
        String etag = metadata.etag();
        if (etag != null && etag.startsWith("\"") && etag.endsWith("\"") && etag.length() >= 2) {
            etag = etag.substring(1, etag.length() - 1);
        }
        // Multipart ETag 是分片摘要的摘要，不是整文件 MD5；信任 Complete 登记的当前行哈希。
        boolean multipart = etag != null && etag.matches("(?i)[0-9a-f]{32}-[1-9][0-9]*");
        if (!multipart && !body.contentMd5().equalsIgnoreCase(etag)) {
            throw new ServiceException("上传对象 MD5 核验不匹配");
        }
        usageService.invalidate(prefix);
        return R.ok(new CommitVo(row.getFileId().toString(), row.getVersionId(), metadata.size(), true));
    }

    public record CommitBo(@NotBlank String path, String space, @NotNull @Min(0) Long size,
                           @NotBlank @Pattern(regexp = "(?i)[0-9a-f]{32}") String contentMd5,
                           @Min(1) Long fileId, @Min(1) Long currentVersion) {
    }

    public record CommitVo(String fileId, Long version, long size, boolean verified) {
    }
}
