package org.dromara.easyshare.drive.service;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.DriveKeys;
import org.dromara.easyshare.drive.DriveProperties;
import org.dromara.easyshare.drive.DriveStorage;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.domain.EsUploadSession;
import org.dromara.easyshare.drive.mapper.EsUploadSessionMapper;
import org.springframework.stereotype.Service;

import java.util.List;

/**
 * Multipart 上传会话服务（Cloudreve 对标 P0：Upload Session 与目录记录联动）。
 * <p>
 * 职责边界：S3 端 uploadId 的创建/分片签名/提交/放弃是机械转发（{@link DriveStorage}），
 * 本服务管三件有业务含义的事——
 * <ul>
 *   <li>**配额与防泄漏**：create 走与单请求上传同口径的配额校验；同路径遗留的
 *       uploading 会话先 Abort 再新建，不让 S3 端孤儿分片吃容量；</li>
 *   <li>**幂等 Complete**：已完成会话重复 Complete 直接返回成功（S3 端 uploadId
 *       已消费，二次提交必然 NoSuchUpload）；分片清单变化则视为客户端错误；</li>
 *   <li>**与目录层联动**：Complete 成功后 upsert es_file（申报大小即真实大小，
 *       S3 已按分片聚合完毕）并作废用量缓存。</li>
 * </ul>
 * 分片清单的 ETag 真值在客户端本地会话文件里，服务端不存——断点续传的续传决策
 * 由客户端按本地指纹做，服务端只保证"会话可续、完成幂等、归属可校"。
 *
 * @author EasyShare
 */
@Slf4j
@Service
@RequiredArgsConstructor
public class DriveUploadService {

    private final EsUploadSessionMapper sessionMapper;
    private final DriveStorage storage;
    private final DriveFileService fileService;
    private final SpaceService spaceService;
    private final SpaceUsageService usageService;
    private final DriveProperties properties;

    /**
     * 创建会话：配额校验 → Abort 同路径遗留会话 → 建 S3 Multipart → 落行。
     *
     * @return 新会话（含 sessionId / uploadId / partSize）
     */
    public EsUploadSession create(String spaceType, Long ownerId, Long uploadBy,
                                  String relativePath, long declaredSize) {
        return create(spaceType, ownerId, uploadBy, relativePath, declaredSize, null, null);
    }

    /**
     * 创建会话（带 CAS 版本寄存）。
     *
     * @param expectedVersion 调用方看到的版本；null = 不校验。此处**只寄存不判定**——
     *                        真正的校验在 complete，因为那才是字节落地、es_file 被登记的时刻。
     *                        在 create 就拒绝会给出虚假的安全感（上游见 assertVersion 的说明）。
     */
    public EsUploadSession create(String spaceType, Long ownerId, Long uploadBy,
                                  String relativePath, long declaredSize, Long expectedVersion,
                                  String contentMd5) {
        String path = DriveKeys.normalizeRelative(relativePath);
        String prefix = EsUploadSession.TYPE_SHARED.equals(spaceType)
            ? spaceService.checkSharedWritable(uploadBy, declaredSize)
            : spaceService.checkPersonalWritable(uploadBy, declaredSize);

        abandonStaleSessions(spaceType, ownerId, uploadBy, path);

        String uploadId = storage.createUpload(prefix, path);
        EsUploadSession session = new EsUploadSession();
        session.setSpaceType(spaceType);
        session.setOwnerId(ownerId);
        session.setUploadBy(uploadBy);
        session.setFilePath(path);
        session.setUploadId(uploadId);
        session.setPartSize(properties.getPartSize().toBytes());
        session.setDeclaredSize(Math.max(0L, declaredSize));
        session.setStatus(EsUploadSession.STATUS_UPLOADING);
        session.setExpectedVersion(expectedVersion);
        session.setContentMd5(contentMd5);
        sessionMapper.insert(session);
        usageService.invalidate(prefix);
        return session;
    }

    /**
     * 签发单分片上传 URL。会话必须属于该用户且进行中。
     */
    public String presignPart(Long uploadBy, long sessionId, int partNumber) {
        EsUploadSession session = ownedUploading(uploadBy, sessionId);
        String prefix = prefixOf(session);
        return storage.presignPartAt(prefix, session.getFilePath(),
            session.getUploadId(), partNumber);
    }

    /**
     * 完成上传（幂等）：已完成会话直接返回；进行中会话按分片清单提交 S3，
     * 成功后落 es_file 目录行并作废用量缓存。
     *
     * @return fileId（es_file 目录行）
     */
    public Long complete(Long uploadBy, long sessionId, List<DriveStorage.UploadPart> parts) {
        EsUploadSession session = owned(uploadBy, sessionId);
        if (EsUploadSession.STATUS_COMPLETED.equals(session.getStatus())) {
            // 幂等：S3 端 uploadId 已消费，重复 Complete 直接返回既有 fileId
            return fileService.fileIdOfPath(session.getSpaceType(),
                session.getOwnerId(), session.getFilePath());
        }
        if (!EsUploadSession.STATUS_UPLOADING.equals(session.getStatus())) {
            throw new ServiceException("上传会话已放弃，请重新创建会话");
        }
        String prefix = prefixOf(session);

        // ⚠️ **顺序是这段代码的正确性所在**：CAS 必须在 S3 写入**之前**。
        //
        // `storage.completeUpload` 会把分片聚合到目标键上——目标键就是那份文件，
        // 聚合即**覆盖**。若先聚合再校验，对方的改动已经被物理覆盖掉了，
        // 此时再报「版本冲突」只是补一张事后通知：数据没了。
        // 所以顺序必须是「先占版本（条件更新），再让字节落地」，
        // 与单请求上传（presign-put 先登记再直传）保持同一形状。
        //
        // 两道：
        //   ① assertVersion 给出**可读的错误归属**（区分「已被删除」与「版本被推进」）；
        //   ② registerOnPresign 的条件 UPDATE 才是真正的原子闸门（防 TOCTOU）——
        //      影响行数为 0 即表示「在我读完之后、写之前，有人先改了」。
        // ⚠️ **两道校验都必须包在 try 里**。
        //
        // `assertVersion` 与 `registerOnPresign` 都会在版本过期时抛异常。
        // 若只包后者（曾如此），前者抛出就不会走到 abortQuietly——
        // 会话于是**永久停在 uploading**：S3 端分片白占容量、库里留一条
        // 永不结束的会话，后续同路径的 create 还得先把它 Abort 掉才走得下去。
        // 实测：跑一次「过期版本被拒」的分片上传，库里就多一条 uploading（KI-26）。
        try {
            fileService.assertVersion(session.getSpaceType(), session.getOwnerId(),
                session.getFilePath(), session.getExpectedVersion());
            fileService.registerOnPresign(session.getSpaceType(), session.getOwnerId(),
                session.getUploadBy(), session.getFilePath(), session.getDeclaredSize(),
                session.getExpectedVersion());
        } catch (RuntimeException ex) {
            // 校验没过：S3 端的分片已经没有价值了，顺手清掉，别让它白占容量。
            // 注意这里**还没有聚合**，目标键上仍是对方的内容，一个字节都没动。
            abortQuietly(prefix, session);
            throw ex;
        }

        // ── 秒传（切片 6）：CAS 已占住版本，此处哈希命中的话字节不必落地 ──
        //
        // 判据：会话带了可靠哈希 + 同空间已有同哈希的 active 行。
        // 此时对象存储里**已经有这份内容**（源行的对象），服务端 CopyObject
        // 把既有对象复制到目标键即可——分片白传了（客户端算哈希在传之前，
        // 命中与否传完才知道；这是分片路径秒传的固有代价），
        // 但聚合不再发生，目标键不会被本次分片覆盖。
        // CopyObject 失败则退回常规聚合——秒传是加速器，不是正确性闸门。
        String hash = session.getContentMd5();
        if (hash != null && !hash.isBlank()) {
            EsFile source = fileService.findByContentHash(
                session.getSpaceType(), session.getOwnerId(), hash);
            if (source != null
                && !source.getFilePath().equals(session.getFilePath())) {
                storage.copyAt(prefix, source.getFilePath(), session.getFilePath());
                abortQuietly(prefix, session); // 清掉已传分片，不再聚合
                markStatus(session, EsUploadSession.STATUS_COMPLETED);
                usageService.invalidate(prefix);
                return fileService.fileIdOfPath(session.getSpaceType(),
                    session.getOwnerId(), session.getFilePath());
            }
        }

        // 版本已占住，此处才允许字节落地。
        // 若这一步失败，es_file 上会留一行「登记了版本但没有对应内容」的幽灵行——
        // 这与单请求上传路径的既有取舍一致（见 DriveFileService 类注：
        // 幽灵行不影响列表正确性，由后续对账清理），且远好于覆盖对方内容。
        storage.completeUpload(prefix, session.getFilePath(),
            session.getUploadId(), parts);
        markStatus(session, EsUploadSession.STATUS_COMPLETED);
        usageService.invalidate(prefix);
        return fileService.fileIdOfPath(session.getSpaceType(),
            session.getOwnerId(), session.getFilePath());
    }

    /**
     * 放弃会话：S3 Abort（清理已传分片）+ 状态推进。已完成会话不可放弃。
     */
    public void abort(Long uploadBy, long sessionId) {
        EsUploadSession session = owned(uploadBy, sessionId);
        if (EsUploadSession.STATUS_COMPLETED.equals(session.getStatus())) {
            throw new ServiceException("会话已完成，不能放弃");
        }
        if (EsUploadSession.STATUS_UPLOADING.equals(session.getStatus())) {
            storage.abortUpload(prefixOf(session), session.getFilePath(), session.getUploadId());
        }
        markStatus(session, EsUploadSession.STATUS_ABORTED);
    }

    /**
     * 会话详情（客户端续传前核对指纹用：路径/分片大小/申报大小）。
     */
    public EsUploadSession owned(Long uploadBy, long sessionId) {
        EsUploadSession session = sessionMapper.selectById(sessionId);
        if (session == null || !uploadBy.equals(session.getUploadBy())) {
            throw new ServiceException("上传会话不存在或不属于当前用户");
        }
        return session;
    }

    private EsUploadSession ownedUploading(Long uploadBy, long sessionId) {
        EsUploadSession session = owned(uploadBy, sessionId);
        if (!EsUploadSession.STATUS_UPLOADING.equals(session.getStatus())) {
            throw new ServiceException("上传会话已结束");
        }
        return session;
    }

    /**
     * 同路径遗留的进行中会话先放弃（S3 Abort + 状态推进），防孤儿分片。
     */
    private void abandonStaleSessions(String spaceType, Long ownerId, Long uploadBy, String path) {
        List<EsUploadSession> stale = sessionMapper.selectList(new LambdaQueryWrapper<EsUploadSession>()
            .eq(EsUploadSession::getSpaceType, spaceType)
            .eq(EsUploadSession::getOwnerId, ownerId)
            .eq(EsUploadSession::getUploadBy, uploadBy)
            .eq(EsUploadSession::getFilePath, path)
            .eq(EsUploadSession::getStatus, EsUploadSession.STATUS_UPLOADING));
        for (EsUploadSession stale_session : stale) {
            try {
                storage.abortUpload(prefixOf(stale_session), path, stale_session.getUploadId());
            } catch (ServiceException ex) {
                log.warn("放弃遗留上传会话失败（继续新建）: session={} {}", stale_session.getSessionId(), ex.getMessage());
            }
            markStatus(stale_session, EsUploadSession.STATUS_ABORTED);
        }
    }

    /** 尽力中止会话（失败只记日志：它的作用是清理，不该掩盖原始异常）。 */
    private void abortQuietly(String prefix, EsUploadSession session) {
        try {
            storage.abortUpload(prefix, session.getFilePath(), session.getUploadId());
        } catch (RuntimeException ex) {
            log.warn("清理上传会话失败（忽略）: session={} {}", session.getSessionId(), ex.getMessage());
        }
        markStatus(session, EsUploadSession.STATUS_ABORTED);
    }

    private void markStatus(EsUploadSession session, String status) {
        session.setStatus(status);
        sessionMapper.updateById(session);
    }

    private String prefixOf(EsUploadSession session) {
        return EsUploadSession.TYPE_SHARED.equals(session.getSpaceType())
            ? DriveKeys.sharedPrefix()
            : DriveKeys.userPrefix(session.getUploadBy());
    }
}
