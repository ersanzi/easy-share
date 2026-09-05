package org.dromara.easyshare.drive;

import cn.dev33.satoken.annotation.SaCheckLogin;
import jakarta.validation.constraints.Max;
import jakarta.validation.constraints.Min;
import jakarta.validation.constraints.NotBlank;
import jakarta.validation.constraints.NotEmpty;
import jakarta.validation.constraints.NotNull;
import lombok.RequiredArgsConstructor;
import org.dromara.common.core.domain.R;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.domain.EsUploadSession;
import org.dromara.easyshare.drive.domain.EsSpace;
import org.dromara.easyshare.drive.service.DriveFileService;
import org.dromara.easyshare.drive.service.DriveUploadService;
import org.dromara.easyshare.drive.service.SpaceService;
import org.dromara.easyshare.drive.service.SpaceUsageService;
import org.springframework.validation.annotation.Validated;
import jakarta.validation.Valid;
import org.springframework.web.bind.annotation.*;

import java.time.Instant;
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
 * <p>
 * 2026-09-06 起接入 es_file 目录层：列表响应带稳定 {@code fileId}
 * （Cloudreve 对标 P0），删除可按 {@code fileId} 或路径（过渡期双轨）。
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
    private final DriveFileService fileService;
    private final DriveUploadService uploadService;

    /**
     * 列举指定空间内的文件。默认个人空间。响应按 es_file 目录层回填稳定 fileId
     * （目录层上线前的存量对象在首次列举时惰性补账，自愈归账）。
     *
     * @param space personal / shared
     * @return 相对路径列表（含 fileId）
     */
    @GetMapping("/objects")
    public R<List<FileVo>> objects(
        @RequestParam(defaultValue = EsSpace.TYPE_PERSONAL) String space) {
        Long userId = LoginHelper.getUserId();
        String prefix = readablePrefix(space);
        Long ownerId = ownerIdOf(space, userId);
        List<DriveObject> objects = storage.listAt(prefix);
        var fileIds = fileService.reconcileAndMap(space, ownerId, userId, objects);
        // 文档级可见性（片 2 数据面）：共享列表按用户部门过滤（上传者本人恒可见）；
        // 过滤在唯一列举出口做，网盘页/挂载盘/快搜文件路全部自动生效
        if (EsSpace.TYPE_SHARED.equals(space)) {
            objects = fileService.filterSharedVisible(objects,
                fileService.rowsByPath(space, ownerId), spaceService.deptIdOf(userId), userId);
        }
        return R.ok(objects.stream()
            .map(object -> new FileVo(
                object.path(), object.size(), object.lastModified(),
                fileIds.get(object.path())))
            .toList());
    }

    /**
     * 签发上传用预签名 URL，签发前校验空间配额。
     * <p>
     * 这是配额唯一能强制的时机：URL 签出后客户端直传 RustFS，字节不经控制面。
     * 因此配额是**软上限**——客户端申报的 size 可能小于实际写入量，且一次签名 15 分钟内
     * 有效。真实用量以 RustFS 为准，超额只能在下一次签发时被拦住。
     * <p>
     * 签发同时在 es_file 目录层登记（幂等 upsert），文件自此有稳定 fileId。
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
        fileService.registerOnPresign(body.space(), ownerIdOf(body.space(), userId), userId, body.path(), size);
        // 上传声明可见范围（可选，仅共享空间语义）：登记后立即写入目录层
        if (body.visibleDepts() != null && !body.visibleDepts().isEmpty()
            && EsSpace.TYPE_SHARED.equals(body.space())) {
            fileService.setRegisteredVisibleDepts(body.space(), ownerIdOf(body.space(), userId),
                userId, body.path(), body.visibleDepts());
        }
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
     * 支持 fileId（优先，稳定身份链路）或路径（过渡期兼容）；两者都给时以 fileId 为准。
     *
     * @param body 相对路径（或 fileId）与空间
     * @return 操作结果
     */
    @DeleteMapping("/object")
    public R<Void> delete(@Validated @RequestBody PathBo body) {
        Long userId = LoginHelper.getUserId();
        boolean shared = EsSpace.TYPE_SHARED.equals(body.space());
        String prefix = shared
            ? spaceService.checkSharedWritable(userId, 0L)
            : DriveKeys.userPrefix(userId);
        Long ownerId = ownerIdOf(body.space(), userId);
        String path = fileService.resolveDeletePath(body.space(), ownerId, body.fileId(), body.path());
        storage.deleteAt(prefix, path);
        fileService.deleteRegistered(body.space(), ownerId, path);
        usageService.invalidate(prefix);
        return R.ok();
    }

    /**
     * 目录层的空间归属键：个人空间 = 用户本人，共享空间 = 0（全局单例）。
     */
    private Long ownerIdOf(String space, Long userId) {
        return EsSpace.TYPE_SHARED.equals(space) ? EsFile.SHARED_OWNER : userId;
    }

    // ── Upload Session（Multipart 断点续传，2026-09-06）──────────────────
    // 分片大小由服务端定（easyshare.drive.part-size，默认 8MB），客户端不选——
    // 技术参数自动推断是产品原则；分片 ETag 清单存客户端本地会话文件，
    // 服务端只记账会话状态（幂等 Complete 的依据）。

    /**
     * 创建上传会话：配额校验 → 清理同路径遗留会话 → 建 S3 Multipart → 落行。
     *
     * @param body 相对路径、空间、申报大小
     * @return sessionId / uploadId / partSize
     */
    @PostMapping("/upload-session/create")
    public R<SessionVo> uploadCreate(@Validated @RequestBody PutBo body) {
        Long userId = LoginHelper.getUserId();
        long size = body.size() == null ? 0L : body.size();
        EsUploadSession session = uploadService.create(body.space(),
            ownerIdOf(body.space(), userId), userId, body.path(), size);
        return R.ok(new SessionVo(session.getSessionId(), session.getUploadId(),
            session.getPartSize()));
    }

    /**
     * 签发单分片上传 URL（会话归属强校验）。
     */
    @PostMapping("/upload-session/part")
    public R<PresignVo> uploadPart(@Validated @RequestBody PartBo body) {
        Long userId = LoginHelper.getUserId();
        String url = uploadService.presignPart(userId, body.sessionId(), body.partNumber());
        return R.ok(new PresignVo(url, null));
    }

    /**
     * 完成上传（幂等）：重复 Complete 直接返回成功与 fileId。
     */
    @PostMapping("/upload-session/complete")
    public R<CompleteVo> uploadComplete(@Validated @RequestBody CompleteBo body) {
        Long userId = LoginHelper.getUserId();
        Long fileId = uploadService.complete(userId, body.sessionId(),
            body.parts().stream()
                .map(part -> new DriveStorage.UploadPart(part.partNumber(), part.etag()))
                .toList());
        return R.ok(new CompleteVo(fileId));
    }

    /**
     * 放弃会话并清理 S3 端已传分片。
     */
    @PostMapping("/upload-session/abort")
    public R<Void> uploadAbort(@Validated @RequestBody SessionBo body) {
        Long userId = LoginHelper.getUserId();
        uploadService.abort(userId, body.sessionId());
        return R.ok();
    }

    /**
     * 设置文档可见部门（文档级可见性，2026-09-06）。visibleDepts 空数组=恢复全体可见。
     * 操作者校验：个人空间=owner、共享空间=上传者本人。
     *
     * @param body 相对路径与可见部门 ID 清单
     * @return 实际写入的逗号分隔部门串
     */
    @PostMapping("/file-visibility")
    public R<String> fileVisibility(@Validated @RequestBody VisibilityBo body) {
        Long userId = LoginHelper.getUserId();
        String saved = fileService.setRegisteredVisibleDepts(body.space(),
            ownerIdOf(body.space(), userId), userId, body.path(), body.visibleDepts());
        return R.ok(saved);
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
     * @param path  空间内的相对路径（fileId 给出时可空，过渡期兼容）
     * @param space personal / shared，空则按个人空间
     * @param fileId 目录层稳定文件 ID，可选；给出时优先于 path
     */
    public record PathBo(
        String path,
        String space,
        Long fileId) {
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
        Long size,
        List<Long> visibleDepts) {
    }

    /**
     * 预签名结果。
     *
     * @param url  预签名 URL
     * @param path 对应的相对路径
     */
    public record PresignVo(String url, String path) {
    }

    /**
     * 带稳定身份的文件视图。
     *
     * @param path         空间内相对路径
     * @param size         字节数
     * @param lastModified 最后修改时间
     * @param fileId       目录层稳定文件 ID（存量对象惰性补账后亦有）
     */
    public record FileVo(String path, long size, Instant lastModified, Long fileId) {
    }

    /**
     * 会话创建结果。
     *
     * @param sessionId 对客户端不透明的会话 ID（雪花）
     * @param uploadId  S3 MultipartUpload uploadId（分片 URL 签名用）
     * @param partSize  服务端定的分片大小（客户端按此切，不自行选择）
     */
    public record SessionVo(Long sessionId, String uploadId, Long partSize) {
    }

    /**
     * 分片签发请求体。
     */
    public record PartBo(
        @NotNull(message = "会话ID不能为空") Long sessionId,
        @NotNull(message = "分片号不能为空") @Min(value = 1, message = "分片号最小为 1") @Max(value = 10000, message = "分片号最大为 10000") Integer partNumber) {
    }

    /**
     * 完成请求体：分片回执清单（partNumber + ETag）。
     */
    public record CompleteBo(
        @NotNull(message = "会话ID不能为空") Long sessionId,
        @NotEmpty(message = "分片清单不能为空") @Valid List<PartEtag> parts) {

        public record PartEtag(
            @NotNull Integer partNumber,
            @NotBlank String etag) {
        }
    }

    /**
     * 完成结果：目录层稳定 fileId。
     */
    public record CompleteVo(Long fileId) {
    }

    /**
     * 会话放弃请求体。
     */
    public record SessionBo(
        @NotNull(message = "会话ID不能为空") Long sessionId) {
    }

    /**
     * 文档可见性请求体。
     */
    public record VisibilityBo(
        @NotBlank(message = "文件路径不能为空") String path,
        @NotBlank(message = "空间不能为空") String space,
        List<Long> visibleDepts) {
    }
}
