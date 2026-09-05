package org.dromara.easyshare.drive.service;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.DriveKeys;
import org.dromara.easyshare.drive.DriveProperties;
import org.dromara.easyshare.drive.DriveStorage;
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
        storage.completeUpload(prefix, session.getFilePath(),
            session.getUploadId(), parts);
        markStatus(session, EsUploadSession.STATUS_COMPLETED);
        usageService.invalidate(prefix);
        // 分片聚合完毕，申报大小即真实大小；复用目录层的幂等 upsert 后回查 fileId
        fileService.registerOnPresign(session.getSpaceType(), session.getOwnerId(),
            session.getUploadBy(), session.getFilePath(), session.getDeclaredSize());
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
