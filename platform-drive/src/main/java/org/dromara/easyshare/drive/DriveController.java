package org.dromara.easyshare.drive;

import cn.dev33.satoken.annotation.SaCheckLogin;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.validation.constraints.Max;
import jakarta.validation.constraints.Min;
import jakarta.validation.constraints.NotBlank;
import jakarta.validation.constraints.NotEmpty;
import jakarta.validation.constraints.NotNull;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.dromara.common.core.domain.R;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.domain.EsUploadSession;
import org.dromara.easyshare.drive.domain.EsSpace;
import org.dromara.easyshare.drive.domain.SysUserDept;
import org.dromara.easyshare.drive.service.DriveFileService;
import org.dromara.easyshare.drive.service.DriveMoveService;
import org.dromara.easyshare.drive.service.DriveUploadService;
import org.dromara.easyshare.drive.service.SpaceService;
import org.dromara.easyshare.drive.service.SpaceUsageService;
import org.springframework.validation.annotation.Validated;
import jakarta.validation.Valid;
import org.springframework.web.bind.annotation.*;

import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.stream.Collectors;

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
@Slf4j
@RequestMapping("/easyshare/drive")
public class DriveController {

    /**
     * 变更日志保留条数（切片 2 的裁剪策略）。
     *
     * <p>日志是给客户端做增量的缓存而非真相源：客户端离线太久、日志覆盖不了
     * 它的差距时走全量重建（不带 sinceVersion 再调一次）。因此只需保留
     * 「最近足够多条」——超过即裁。10000 条按每空间每天几百次变更估算约一个月。
     */
    private static final int changeLogTrimKeep = 10_000;

    private final DriveStorage storage;
    private final SpaceService spaceService;
    private final SpaceUsageService usageService;
    private final DriveFileService fileService;
    private final DriveUploadService uploadService;
    private final DriveMoveService moveService;
    private final org.dromara.easyshare.drive.service.DriveChangeLogService changeLogService;
    /**
     * 系统用户只读投影，仅用于把 es_file.upload_by 还原成可读名字。
     * 与 SpaceService 共用同一个 Mapper（同一张 sys_user 投影），不再单独建 DAO。
     */
    private final org.dromara.easyshare.drive.mapper.SysUserDeptMapper sysUserDeptMapper;
    /** 星标 / 最近访问 */
    private final org.dromara.easyshare.drive.service.FileMarkService markService;
    /** 文件分享 */
    private final org.dromara.easyshare.drive.service.ShareService shareService;

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
        // 查库 → 藏掉软删 → 回填 fileId（KI-10）。三步的配套关系是正确性的一部分，
        // 故收在 DriveFileService.list 里一次做完，控制器不再自己串。
        DriveFileService.Listing listing = fileService.list(
            space, ownerId, userId, storage.listAt(prefix));
        List<DriveObject> objects = listing.objects();
        // 文档级可见性（片 2 数据面）：共享列表按用户部门过滤（上传者本人恒可见）；
        // 过滤在唯一列举出口做，网盘页/挂载盘/快搜文件路全部自动生效
        if (EsSpace.TYPE_SHARED.equals(space)) {
            objects = fileService.filterSharedVisible(objects,
                listing.activeByPath(), spaceService.deptIdOf(userId), userId);
        }
        // 上传者昵称一次 IN 查询批量取（不在流里逐行查库）。
        // 行不存在（未登记的存量对象）时 createdAt / uploadByName 均为 null，
        // 客户端按「没有就不显示」处理，不用占位符充数。
        Map<String, EsFile> activeRows = listing.activeByPath();
        Map<Long, String> uploaderNames = uploaderNamesOf(activeRows);
        return R.ok(objects.stream()
            .map(object -> toFileVo(object, listing, activeRows, uploaderNames))
            .toList());
    }

    /**
     * 列表项的唯一装配点：列举（/objects）与增量全量重建（/changes）两条路径都走这里。
     *
     * <p>收成一个方法的原因是**这两条路径必须产出同构的 FileVo**：上一轮给
     * {@code FileVo} 加字段时只改了 /objects，/changes 那条当场编译失败——
     * 两处各写一遍构造器，就等于给「改一半」留了固定的口子。
     *
     * <p>行不存在（未登记的存量对象）时 md5 / createdAt / uploadByName 均为 null，
     * 与「登记了但未上报」同语义，客户端一律按「没有就不显示」处理。
     */
    private static FileVo toFileVo(DriveObject object, DriveFileService.Listing listing,
                                   Map<String, EsFile> activeRows, Map<Long, String> uploaderNames) {
        EsFile row = activeRows.get(object.path());
        return new FileVo(
            object.path(), object.size(), object.lastModified(),
            listing.fileIds().get(object.path()),
            listing.versions().get(object.path()),
            row == null ? null : row.getContentMd5(),
            row == null ? null : toInstant(row.getCreateTime()),
            row == null ? null : uploaderNames.get(row.getUploadBy()));
    }

    /**
     * 批量取上传者显示名。
     *
     * <p>一次 {@code IN} 查询解决整页，避免在流里逐行查库（N+1）。
     * 优先 {@code nickName}，为空退回 {@code userName}；两者都空则该 ID 不进结果集，
     * 调用方拿到 null，客户端按「没有就不显示」处理。
     */
    private Map<Long, String> uploaderNamesOf(Map<String, EsFile> activeRows) {
        Set<Long> ids = activeRows.values().stream()
            .map(EsFile::getUploadBy)
            .filter(id -> id != null && id > 0)
            .collect(Collectors.toSet());
        if (ids.isEmpty()) {
            return Map.of();
        }
        Map<Long, String> names = new java.util.HashMap<>();
        for (SysUserDept user : sysUserDeptMapper.selectByIds(ids)) {
            if (user.getUserId() == null) {
                continue;
            }
            String name = user.getNickName() != null && !user.getNickName().isBlank()
                ? user.getNickName() : user.getUserName();
            if (name != null && !name.isBlank()) {
                names.put(user.getUserId(), name);
            }
        }
        return names;
    }

    /**
     * es_file 的时间列是服务器本地时区的 {@link LocalDateTime}，
     * 对外统一成 {@link Instant}（与 lastModified 同类型），客户端解析无歧义。
     */
    private static Instant toInstant(LocalDateTime time) {
        return time == null ? null : time.atZone(ZoneId.systemDefault()).toInstant();
    }

    /**
     * 增量列举（路线图切片 2）：拉取 sinceVersion 之后的文件变更。
     *
     * <p>不带 {@code sinceVersion} 表示**全量重建**：返回当前完整列表 +
     * {@code newestSpaceVersion}，客户端从此版本继续增量。
     * 带 {@code sinceVersion} 则只返回变更日志（不再附带全量对象）。
     *
     * <p>变更日志是差异流而非真相源；日志被裁剪或客户端离线太久时，
     * 客户端应退全量重建（即不带 sinceVersion 再调一次）。
     */
    @GetMapping("/changes")
    public R<ChangesVo> changes(
        @RequestParam(defaultValue = EsSpace.TYPE_PERSONAL) String space,
        @RequestParam(required = false) Long sinceVersion) {
        Long userId = LoginHelper.getUserId();
        String prefix = readablePrefix(space);
        Long ownerId = ownerIdOf(space, userId);
        long newest = changeLogService.currentVersion(space, ownerId);
        if (sinceVersion == null) {
            DriveFileService.Listing listing = fileService.list(
                space, ownerId, userId, storage.listAt(prefix));
            List<DriveObject> objects = listing.objects();
            if (EsSpace.TYPE_SHARED.equals(space)) {
                objects = fileService.filterSharedVisible(objects,
                    listing.activeByPath(), spaceService.deptIdOf(userId), userId);
            }
            Map<String, EsFile> activeRows = listing.activeByPath();
            Map<Long, String> uploaderNames = uploaderNamesOf(activeRows);
            List<FileVo> full = objects.stream()
                .map(object -> toFileVo(object, listing, activeRows, uploaderNames))
                .toList();
            // 全量重建意味着客户端即将拿到完整列表：老日志从此无人需要，顺手裁剪
            // （切片 2 的日志回收；失败不影响列举）
            changeLogService.trim(changeLogTrimKeep);
            return R.ok(new ChangesVo(newest, null, full, null));
        }
        List<ChangeVo> changes = changeLogService.changesAfter(space, ownerId, sinceVersion)
            .stream()
            .map(log -> {
                // 变更日志只记身份；给客户端补上 size/lastModified，
                // 否则挂载盘增量合并时只能得到 0 字节的占位对象。
                var file = fileService.selectActiveById(space, ownerId, log.getFileId());
                return new ChangeVo(
                    log.getSpaceVersion(), log.getEventType(), log.getFileId(),
                    log.getFilePath(), log.getOldPath(),
                    file == null ? 0L : file.getFileSize(),
                    file == null ? null : file.getCreateTime());
            })
            .toList();
        return R.ok(new ChangesVo(newest, sinceVersion, null, changes));
    }

    /**
     * 签发上传用预签名 URL，签发前校验空间配额。
     * <p>
     * 这是配额唯一能强制的时机：URL 签出后客户端直传 RustFS，字节不经控制面。
     * 因此配额是**软上限**——客户端申报的 size 可能小于实际写入量，且一次签名 15 分钟内
     * 有效。真实用量以 RustFS 为准，超额只能在下一次签发时被拦住。
     * <p>
     * 签发同时在 es_file 目录层登记（幂等 upsert），文件自此有稳定 fileId。
     * <p>
     * **秒传（切片 6）**：body 带 {@code contentMd5} 且同空间已有同哈希 active 行时，
     * 服务端直接 {@code CopyObject} 复制既有对象到目标键，响应置 {@code deduped=true}，
     * 客户端跳过 PUT。未带哈希的旧调用方行为完全不变。
     *
     * @param body 相对路径、空间、申报大小（可选 contentMd5 / expectedVersion）
     * @return 预签名 URL（秒传命中时 url 为 null、deduped=true）
     */
    @PostMapping("/presign-put")
    public R<PresignVo> presignPut(@Validated @RequestBody PutBo body) {
        Long userId = LoginHelper.getUserId();
        long size = body.size() == null ? 0L : body.size();
        String space = body.space();
        Long ownerId = ownerIdOf(space, userId);
        String prefix = EsSpace.TYPE_SHARED.equals(space)
            ? spaceService.checkSharedWritable(userId, size)
            : spaceService.checkPersonalWritable(userId, size);
        // 签发即视为将写入：作废缓存，下一次校验重新聚合真实用量
        usageService.invalidate(prefix);
        // 秒传判定先于登记：命中时字节路径整段跳过，哈希随登记一并落行
        if (body.contentMd5() != null && !body.contentMd5().isBlank()) {
            EsFile source = fileService.findByContentHash(space, ownerId, body.contentMd5());
            if (source != null && !source.getFilePath().equals(DriveKeys.normalizeRelative(body.path()))) {
                fileService.registerOnPresign(space, ownerId, userId, body.path(), size,
                    body.expectedVersion(), body.contentMd5());
                storage.copyAt(prefix, source.getFilePath(), DriveKeys.normalizeRelative(body.path()));
                usageService.invalidate(prefix);
                return R.ok(new PresignVo(null, body.path(), true));
            }
        }
        fileService.registerOnPresign(space, ownerId, userId, body.path(), size,
            body.expectedVersion(), body.contentMd5());
        // 上传声明可见范围（可选，仅共享空间语义）：登记后立即写入目录层
        if (body.visibleDepts() != null && !body.visibleDepts().isEmpty()
            && EsSpace.TYPE_SHARED.equals(space)) {
            fileService.setRegisteredVisibleDepts(space, ownerId,
                userId, body.path(), body.visibleDepts());
        }
        return R.ok(new PresignVo(storage.presignPutAt(prefix, body.path()), body.path(), false));
    }

    /**
     * 签发下载用预签名 URL。
     * <p>
     * 与列表共用同一份可见性口径：**软删的行不能被签、受限的行不能被签**。
     * 只在列表里过滤而这里不查，等于「看不见但拿得到」——
     * 知道路径的人仍能绕过文档级可见性把文件拿走。
     *
     * @param body 相对路径与空间
     * @return 预签名 URL
     */
    @PostMapping("/presign-get")
    public R<PresignVo> presignGet(@Validated @RequestBody PathBo body) {
        Long userId = LoginHelper.getUserId();
        String prefix = readablePrefix(body.space());
        fileService.assertDownloadable(body.space(), ownerIdOf(body.space(), userId), body.path(),
            spaceService.deptIdOf(userId), userId);
        return R.ok(new PresignVo(storage.presignGetAt(prefix, body.path()), body.path(), false));
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
        // 软删（P1 回收站）：RustFS 对象保留，目录行置 recycled + 原路径快照。
        // 还原时同键复位，无需搬对象；彻底删除走 /recycle/purge 才真删对象。
        fileService.markRecycled(body.space(), ownerId, path);
        usageService.invalidate(prefix);
        return R.ok();
    }

    // ── 回收站（P1，wps-cloud-drive-benchmark.md §6-P1 第 1 项）──────────────
    // 还原冲突策略：原路径已被同名 active 文件占用时返回可读错误，不自动覆盖。

    /**
     * 改名 / 移动（切片 4）。
     * <p>
     * **服务端复制 + 删除源**，字节不出对象存储：旧实现是客户端
     * 「下载 + 上传 + 删除」，一个 2 GB 文件改名要先拉回本地再传上去，
     * 等几分钟、流量翻倍、中途失败留两份。S3 的 CopyObject 把这一步变成服务端操作。
     * <p>
     * **fileId 不变**——这是「身份与路径解耦」的落点：改名后分享链接、任务引用、
     * 知识索引仍指向同一个文件。旧实现走「上传新键 + 删旧键」，
     * 等于换了一个文件（新 fileId），引用全断。
     * <p>
     * 顺序刻意是「先占目录行、再复制、最后删源」：目录行是唯一性闸门，
     * 目标已存在时先拒绝，不会留下复制出来的垃圾；源最后删，失败也不丢数据。
     *
     * @param body 源路径、目标路径与空间
     * @return 目标路径
     */
    @PostMapping("/move")
    public R<MoveVo> move(@Validated @RequestBody MoveBo body) {
        Long userId = LoginHelper.getUserId();
        boolean shared = EsSpace.TYPE_SHARED.equals(body.space());
        String prefix = shared
            ? spaceService.checkSharedWritable(userId, 0L)
            : DriveKeys.userPrefix(userId);
        Long ownerId = ownerIdOf(body.space(), userId);
        String from = DriveKeys.normalizeRelative(body.from());
        String to = DriveKeys.normalizeRelative(body.to());
        if (from.equals(to)) {
            return R.ok(new MoveVo(to));
        }
        // 行改 → 复制 → 删源，失败时补偿（顺序理由与补偿细节见 DriveMoveService）
        moveService.move(body.space(), ownerId, prefix, from, to);
        usageService.invalidate(prefix);
        return R.ok(new MoveVo(to));
    }

    /**
     * 回收站列表：当前空间的 recycled 行（按删除时间倒序）。
     *
     * @param space personal / shared
     */
    @GetMapping("/recycle")
    public R<List<RecycleVo>> recycle(@RequestParam(defaultValue = EsSpace.TYPE_PERSONAL) String space) {
        Long userId = LoginHelper.getUserId();
        Long ownerId = ownerIdOf(space, userId);
        return R.ok(fileService.listRecycled(space, ownerId).stream()
            .map(row -> new RecycleVo(row.getFileId(), row.getFilePath(), row.getOriginPath(),
                row.getFileSize(), row.getVersionId(),
                row.getDeletedAt() == null ? null : row.getDeletedAt().atZone(java.time.ZoneId.systemDefault()).toInstant()))
            .toList());
    }

    /**
     * 还原：按 origin_path 复位为 active。原位置有同名 active 文件时报冲突。
     */
    @PostMapping("/recycle/restore")
    public R<Void> recycleRestore(@Validated @RequestBody RecycleBo body) {
        Long userId = LoginHelper.getUserId();
        fileService.restoreFromRecycle(body.space(), ownerIdOf(body.space(), userId), body.fileId());
        usageService.invalidate(readablePrefix(body.space()));
        return R.ok();
    }

    /**
     * 彻底删除：目录行摘除 + RustFS 对象删除（不可还原）。
     */
    @PostMapping("/recycle/purge")
    public R<Void> recyclePurge(@Validated @RequestBody RecycleBo body) {
        Long userId = LoginHelper.getUserId();
        boolean shared = EsSpace.TYPE_SHARED.equals(body.space());
        String prefix = shared
            ? spaceService.checkSharedWritable(userId, 0L)
            : DriveKeys.userPrefix(userId);
        Long ownerId = ownerIdOf(body.space(), userId);
        DriveFileService.PurgeResult purged = fileService.purgeFromRecycle(body.space(), ownerId, body.fileId());
        // KI-7：只有当该路径没有 active 行在用时才删对象。
        // 对象键由路径派生（DriveKeys.keyAt），而用户可能「删完又重新上传了同名文件」——
        // 那个键此刻属于新文件，删了就会丢掉用户当前在用的内容。
        if (purged.deletable()) {
            storage.deleteAt(prefix, purged.row().getFilePath());
        } else {
            log.info("purge 跳过删对象：路径 {} 已被同路径 active 文件占用（回收站旧行的对象早已被覆盖）",
                purged.row().getFilePath());
        }
        usageService.invalidate(prefix);
        return R.ok();
    }

    /**
     * 回收站操作请求体：按稳定 fileId 定位（挂 fileId，不新增路径绑定）。
     */
    public record RecycleBo(
        @NotNull(message = "文件ID不能为空") Long fileId,
        String space) {
    }

    /**
     * 回收站行视图：路径快照与删除时间（还原落点展示用）。
     */
    public record RecycleVo(Long fileId, String path, String originPath,
                            Long size, Long versionId, Instant deletedAt) {
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
            ownerIdOf(body.space(), userId), userId, body.path(), size, body.expectedVersion(),
            body.contentMd5());
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
        return R.ok(new PresignVo(url, null, false));
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
     * 当前空间下该用户的星标路径集合。
     *
     * <p>只返回路径不返回文件信息：星标是「在列表里打标」的辅助视图，
     * 列表本来就会拿到完整对象，前端按路径匹配即可，不必重复传一遍元数据。
     *
     * @param space personal / shared
     * @return 星标路径集合
     */
    @GetMapping("/stars")
    public R<Set<String>> stars(@RequestParam(defaultValue = EsSpace.TYPE_PERSONAL) String space) {
        return R.ok(markService.starredPaths(LoginHelper.getUserId(), space));
    }

    /**
     * 设置或取消星标。
     *
     * @param body 相对路径 + 目标状态
     * @return 操作后的状态（true=已星标）
     */
    @PostMapping("/star")
    public R<Boolean> star(@Validated @RequestBody StarBo body) {
        Long userId = LoginHelper.getUserId();
        String space = body.space() == null || body.space().isBlank()
            ? EsSpace.TYPE_PERSONAL : body.space();
        return R.ok(markService.setStar(userId, space, body.path(), body.starred()));
    }

    /**
     * 记录一次最近访问（打开 / 预览 / 下载时由客户端上报）。
     *
     * <p>不返回内容：这是埋点语义，写失败不该阻断用户正在做的事。
     *
     * @param body 相对路径
     */
    @PostMapping("/touch")
    public R<Void> touch(@Validated @RequestBody PathBo body) {
        // PathBo 的 path 没有 @NotBlank（别的接口允许只给 fileId），这里自己兜
        if (body.path() == null || body.path().isBlank()) {
            return R.ok();
        }
        Long userId = LoginHelper.getUserId();
        String space = body.space() == null || body.space().isBlank()
            ? EsSpace.TYPE_PERSONAL : body.space();
        markService.touch(userId, space, body.path());
        return R.ok();
    }

    /**
     * 最近访问路径（按时间倒序）。
     *
     * @param space personal / shared
     * @param limit 条数上限，服务端再兜一次
     * @return 路径列表
     */
    @GetMapping("/recents")
    public R<List<String>> recents(
        @RequestParam(defaultValue = EsSpace.TYPE_PERSONAL) String space,
        @RequestParam(defaultValue = "30") int limit) {
        return R.ok(markService.recentPaths(LoginHelper.getUserId(), space, limit));
    }

    /**
     * 创建分享链接。
     *
     * <p>返回的是**长效且可撤销**的外链，与 {@code /presign-get} 的一次性地址不同：
     * 后者 10 分钟就失效、也无法收回，只适合「马上要下载一次」。
     *
     * @param body    相对路径 + 期望有效期（小时）
     * @param request 用于拼出绝对 URL（企业内网场景下客户端拿到的是控制面地址）
     * @return 分享记录（含完整链接）
     */
    @PostMapping("/share")
    public R<ShareVo> share(@Validated @RequestBody ShareBo body, HttpServletRequest request) {
        Long userId = LoginHelper.getUserId();
        String space = body.space() == null || body.space().isBlank()
            ? EsSpace.TYPE_PERSONAL : body.space();
        var share = shareService.create(userId, space, body.path(), body.expiresHours());
        return R.ok(toShareVo(share, request));
    }

    /**
     * 我创建的分享（按时间倒序）。
     *
     * <p>包含已撤销/已过期的记录：用户需要看到「我分享过什么、哪些已经失效」，
     * 直接隐藏会让「我明明分享过」变成悬案。
     */
    @GetMapping("/shares")
    public R<List<ShareVo>> shares(HttpServletRequest request) {
        return R.ok(shareService.listMine(LoginHelper.getUserId()).stream()
            .map(share -> toShareVo(share, request))
            .toList());
    }

    /**
     * 撤销分享。撤销立即生效，不等自然过期。
     *
     * @param body 分享 ID
     * @return 是否确实撤销了一条（false = 不存在或不属于当前用户）
     */
    @PostMapping("/share/revoke")
    public R<Boolean> revokeShare(@Validated @RequestBody ShareRevokeBo body) {
        return R.ok(shareService.revoke(LoginHelper.getUserId(), body.shareId()));
    }

    /** 分享记录 → 对外 VO；链接由 token 现拼，不落库（换域名不必改数据） */
    private static ShareVo toShareVo(org.dromara.easyshare.drive.domain.EsShare share,
                                     HttpServletRequest request) {
        return new ShareVo(
            share.getShareId(),
            share.getToken(),
            shareBaseUrl(request) + "/easyshare/s/" + share.getToken(),
            share.getFileName(),
            share.getFileSize() == null ? 0L : share.getFileSize(),
            toInstant(share.getExpiresAt()),
            Boolean.TRUE.equals(share.getRevoked()),
            share.getVisitCount() == null ? 0 : share.getVisitCount(),
            toInstant(share.getCreatedAt()));
    }

    /** 从当前请求推导绝对地址；默认端口不拼端口号 */
    private static String shareBaseUrl(HttpServletRequest request) {
        String scheme = request.getScheme();
        int port = request.getServerPort();
        boolean defaultPort = ("http".equals(scheme) && port == 80)
            || ("https".equals(scheme) && port == 443);
        return scheme + "://" + request.getServerName() + (defaultPort ? "" : ":" + port);
    }

    /** 取可读空间的前缀。个人空间无需授权（自己的），共享空间要有成员行。
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
    /**
     * 创建分享请求。
     *
     * @param path         空间内相对路径
     * @param expiresHours 期望有效期（小时）；空或非正数走默认 7 天，上限 30 天
     * @param space        personal / shared，空则按个人空间
     */
    public record ShareBo(
        @NotBlank String path,
        Integer expiresHours,
        String space) {
    }

    /**
     * 撤销分享请求。
     *
     * @param shareId 分享记录 ID
     */
    public record ShareRevokeBo(
        @NotNull Long shareId) {
    }

    /**
     * 分享记录对外视图。
     *
     * @param shareId    分享 ID（撤销时用）
     * @param token      URL 中的随机码
     * @param url        完整可复制链接
     * @param fileName   创建时的文件名快照
     * @param fileSize   创建时的字节数快照
     * @param expiresAt  到期时间
     * @param revoked    是否已撤销
     * @param visitCount 成功跳转次数
     * @param createdAt  创建时间
     */
    public record ShareVo(Long shareId, String token, String url, String fileName, long fileSize,
                          Instant expiresAt, boolean revoked, int visitCount, Instant createdAt) {
    }

    /**
     * 星标请求。
     *
     * @param path    空间内相对路径
     * @param starred true=打星标，false=取消
     * @param space   personal / shared，空则按个人空间
     */
    public record StarBo(
        @NotBlank String path,
        boolean starred,
        String space) {
    }

    public record PathBo(
        String path,
        String space,
        Long fileId) {
    }
    /**
     * 改名 / 移动请求体。
     *
     * @param from  源相对路径
     * @param to    目标相对路径
     * @param space personal / shared，空则按个人空间
     */
    public record MoveBo(
        @NotBlank(message = "源路径不能为空") String from,
        @NotBlank(message = "目标路径不能为空") String to,
        String space) {
    }

    /**
     * 改名结果。
     *
     * @param path 移动后的相对路径
     */
    public record MoveVo(String path) {
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
        List<Long> visibleDepts,
        /**
         * 调用方看到的版本号（可选，路线图切片 1 的 CAS）。
         *
         * <p>传了就做**乐观并发校验**：与服务端当前版本不一致时拒绝签发，
         * 让用户刷新后重试，而不是静默覆盖别人的修改。
         *
         * <p>不传 = 不做校验（旧调用方/新建场景），行为与本字段引入前一致——
         * 这是刻意的向后兼容：升级客户端与服务端不必同时上线。
         */
        Long expectedVersion,
        /**
         * 内容 MD5（十六进制小写，切片 6 的秒传判定键）。
         *
         * <p>客户端**整文件读算**后随请求上报。服务端同空间已有同哈希 active 行时
         * 直接服务端复制既有对象，客户端跳过字节上传（分片路径在 complete 判定）。
         * 不传 = 不判秒传，行为与本字段引入前一致。
         */
        String contentMd5) {
    }

    /**
     * 预签名结果。
     *
     * @param url     预签名 URL；秒传命中时为 null（客户端跳过 PUT）
     * @param path    对应的相对路径
     * @param deduped 秒传命中标记（切片 6）：true = 服务端已复制既有对象，无需上传
     */
    public record PresignVo(String url, String path, boolean deduped) {
    }

    /**
     * 带稳定身份的文件视图。
     *
     * @param path         空间内相对路径
     * @param size         字节数
     * @param lastModified 最后修改时间
     * @param fileId       目录层稳定文件 ID（存量对象惰性补账后亦有）
     * @param versionId    该文件的当前版本号；客户端下次写同一路径时应带回做 CAS
     * @param contentMd5   内容 MD5（切片 3 冲突判定 + 切片 6 秒传；未上报为 null）
     * @param createdAt    首次登记时间（es_file.create_time）；存量未登记对象为 null
     * @param uploadByName 上传者显示名（nickName 优先，退回 userName）；未知为 null
     */
    public record FileVo(String path, long size, Instant lastModified, Long fileId, Long versionId,
                         String contentMd5, Instant createdAt, String uploadByName) {
    }

    /**
     * 增量列举结果。
     *
     * @param newestSpaceVersion 客户端下次增量拉取应带的版本号
     * @param sinceVersion       本次请求带入的版本；null 表示这是全量重建
     * @param files              仅全量重建时非空：当前完整文件列表
     * @param changes            仅增量拉取时非空：sinceVersion 之后的变更
     */
    public record ChangesVo(long newestSpaceVersion, Long sinceVersion,
                            List<FileVo> files, List<ChangeVo> changes) {
    }

    /**
     * 单条文件变更。
     *
     * @param spaceVersion 该变更对应的空间版本
     * @param event        created / updated / deleted / restored / moved / purged
     * @param fileId       稳定文件 ID
     * @param path         事件后的当前路径；purged 为 null
     * @param oldPath      moved 的源路径；其他事件为 null
     */
    public record ChangeVo(long spaceVersion, String event, Long fileId,
                           String path, String oldPath, long size,
                           java.time.LocalDateTime lastModified) {
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
