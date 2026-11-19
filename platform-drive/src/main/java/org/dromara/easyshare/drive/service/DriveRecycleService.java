package org.dromara.easyshare.drive.service;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import lombok.RequiredArgsConstructor;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.DriveKeys;
import org.dromara.easyshare.drive.DriveRecycleKeys;
import org.dromara.easyshare.drive.DriveStorage;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.domain.EsFileChangeLog;
import org.dromara.easyshare.drive.domain.EsRecycleBatch;
import org.dromara.easyshare.drive.mapper.EsFileMapper;
import org.dromara.easyshare.drive.mapper.EsRecycleBatchMapper;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.time.LocalDateTime;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;
import java.util.Objects;
import java.util.UUID;

/** 独立回收正文与删除批次；服务端复制不消耗客户端带宽，失败不授权本机删除。 */
@Service
@RequiredArgsConstructor
public class DriveRecycleService {
    private final EsFileMapper files;
    private final EsRecycleBatchMapper batches;
    private final DriveFileService fileService;
    private final DriveStorage storage;
    private final DriveChangeLogService changes;
    private final FileRoleService roles;
    private final SpaceUsageService usage;
    private final RecycleBatchLedger ledger;

    public record Result(String batchId, String operationId, String fileId, String path,
                         boolean directory, String status, int memberCount) { }

    /** 删除先复制并核验所有正文，再在同一目录事务内软删；对象失败不会提交目录状态。 */
    @Transactional(rollbackFor = Exception.class)
    public Result recycle(String space, Long owner, Long user, Long fileId, String rawPath,
                          Long expectedVersion, String operationId, boolean recursive) {
        requireSpace(space, owner, user);
        batches.lockSpace(space, owner);
        String operation = operationId == null ? UUID.randomUUID().toString() : DriveRecycleKeys.requireToken(operationId);
        EsRecycleBatch previous = byOperation(space, owner, user, operation);
        if (previous != null) {
            if (fileId != null && !fileId.equals(previous.getRootFileId())
                || !Objects.equals(expectedVersion, previous.getExpectedVersion())) {
                throw new ServiceException("重复操作标识的文件身份或版本不一致");
            }
            if (rawPath != null && !DriveKeys.normalizeRelative(rawPath.replaceAll("/+$", "")).equals(previous.getRootPath())) {
                throw new ServiceException("重复操作标识的目标不一致");
            }
            if ("recycled".equals(previous.getStatus()) || "restored".equals(previous.getStatus())) {
                return view(previous);
            }
            if (!"preparing".equals(previous.getStatus())) {
                throw new ServiceException("删除操作尚未确认，请查询原操作状态后重试");
            }
        }
        String path = fileService.resolveDeletePath(space, owner, fileId,
            rawPath == null ? null : rawPath.replaceAll("/+$", ""));
        EsFile root = fileService.lockActiveForPresign(space, owner, path);
        if (root == null || fileId != null && !fileId.equals(root.getFileId())) {
            throw new ServiceException("文件不存在或身份已变化，请刷新后重试");
        }
        if (expectedVersion != null && expectedVersion != version(root)) {
            throw new ServiceException("删除前文件版本已变化，已保留文件");
        }
        List<EsFile> members = new ArrayList<>();
        members.add(root);
        if (root.directoryEntry()) {
            List<EsFile> children = ownedRows(space, owner).stream()
                .filter(row -> active(row) && row.getFilePath().startsWith(path + "/")).toList();
            if (!recursive && !children.isEmpty()) {
                throw new ServiceException("目录非空，请使用明确递归删除");
            }
            if (recursive) { members.addAll(children); }
        }
        if (EsFile.TYPE_SHARED.equals(space)) {
            members.forEach(row -> roles.assertManage(row, user));
        }
        EsRecycleBatch batch;
        if (previous != null) {
            batch = previous;
            List<EsFile> frozen = ledger.decodeMembers(batch.getMembersJson());
            java.util.Set<String> actual = members.stream().map(row -> row.getFileId()+":"+version(row)+":"+row.getFilePath()).collect(java.util.stream.Collectors.toSet());
            java.util.Set<String> expected = frozen.stream().map(row -> row.getFileId()+":"+version(row)+":"+row.getFilePath()).collect(java.util.stream.Collectors.toSet());
            if (!actual.equals(expected)) { throw new ServiceException("未完成删除的成员或版本已变化，已保留文件"); }
            members = frozen;
        } else {
            batch = new EsRecycleBatch();
            batch.setBatchId(UUID.randomUUID().toString());
            batch.setSpaceType(space); batch.setOwnerId(owner); batch.setOperatorId(user);
            batch.setOperationId(operation); batch.setRootFileId(root.getFileId());
            batch.setRootPath(path); batch.setDirectory(root.directoryEntry());
            batch.setExpectedVersion(expectedVersion); batch.setStatus("preparing");
            batch.setMemberCount(members.size()); batch.setCreatedAt(LocalDateTime.now());
            batch.setUpdatedAt(batch.getCreatedAt());
            batch.setMembersJson(ledger.encodeMembers(members));
            batch = ledger.reserve(batch);
        }
        String prefix = prefix(space, owner);
        // 先处理文件快照；复制完成后重新HEAD核对源仍是同一内容，禁止采纳中途覆盖。
        for (EsFile row : members) {
            boolean sameSnapshotBatch = batch.getBatchId().equals(row.getRecycleBatchId());
            if (!sameSnapshotBatch) {
                // 还原后的文件可继续编辑和再次删除；旧批次快照仍由原操作账保管。
                row.setRecycleStoragePath(null);
                row.setRecycleSnapshotSha256(null);
                row.setRecycleSnapshotEtag(null);
                row.setRecycleSnapshotSize(null);
                row.setRecycleSnapshotState(null);
            }
            row.setRecycleBatchId(batch.getBatchId()); row.setRecycleOperationId(operation);
            row.setRecycleRoot(row.getFileId().equals(root.getFileId()));
            row.setRecycleSnapshotVersion(version(row));
            if (!row.directoryEntry()) {
                String key = DriveRecycleKeys.storagePathOf(batch.getBatchId(), row.getFileId(), version(row));
                DriveStorage.ObjectMetadata original = storage.metadataAt(prefix, row.getFilePath());
                if (original == null || original.size() != size(row) || original.etag() == null) {
                    throw new ServiceException("服务器正文尚未提交或与登记不一致，文件已保留");
                }
                String sourceHash = storage.sha256At(prefix, row.getFilePath());
                if (row.getRecycleSnapshotSha256() != null && !sourceHash.equals(row.getRecycleSnapshotSha256())) {
                    throw new ServiceException("原删除版本的正文已变化，保留文件和旧快照");
                }
                // 复制前先独立保存内容身份；断电重试不得用新正文覆盖旧快照。
                row.setRecycleStoragePath(key);
                row.setRecycleSnapshotSha256(sourceHash);
                batch.setMembersJson(ledger.encodeMembers(members));
                ledger.checkpoint(batch);
                DriveStorage.ObjectMetadata saved;
                if ("ready".equals(row.getRecycleSnapshotState()) && key.equals(row.getRecycleStoragePath())) {
                    saved = storage.metadataAt(prefix, key);
                    if (saved == null || !Objects.equals(saved.etag(), row.getRecycleSnapshotEtag()) || saved.size() != row.getRecycleSnapshotSize()) {
                        throw new ServiceException("原删除快照已变化，禁止覆盖或重新复制");
                    }
                } else {
                    storage.copyAt(prefix, row.getFilePath(), key);
                    saved = storage.metadataAt(prefix, key);
                }
                DriveStorage.ObjectMetadata latest = storage.metadataAt(prefix, row.getFilePath());
                if (!original.equals(latest) || saved == null || original.size() != saved.size()
                    || !sourceHash.equals(storage.sha256At(prefix, key))
                    || !sourceHash.equals(storage.sha256At(prefix, row.getFilePath()))) {
                    throw new ServiceException("回收站正文核验失败或文件正被修改，未执行删除");
                }
                row.setRecycleStoragePath(key); row.setRecycleSnapshotSize(saved.size());
                row.setRecycleSnapshotEtag(saved.etag());
            } else { row.setRecycleSnapshotSize(0L); }
            row.setOriginPath(row.getFilePath());
            row.setRecycleSnapshotState("ready");
            batch.setMembersJson(ledger.encodeMembers(members));
            ledger.checkpoint(batch);
        }
        // 目录保护触发器要求最深层先软删；所有快照已确认后才改目录状态。
        members.sort(Comparator.comparingInt((EsFile row) -> row.getFilePath().length()).reversed());
        for (EsFile row : members) {
            row.setOriginPath(row.getFilePath()); row.setDeletedAt(LocalDateTime.now());
            row.setState(EsFile.STATE_RECYCLED);
            if (files.updateById(row) != 1) { throw new ServiceException("删除记录更新失败"); }
            changes.record(space, owner, EsFileChangeLog.EVENT_DELETED, row, row.getFileId(), row.getFilePath(), null);
        }
        batch.setStatus("recycled"); batch.setUpdatedAt(LocalDateTime.now());
        batches.updateById(batch); usage.invalidate(prefix);
        return view(batch);
    }

    /** 还原准确批次；旧回收站继续走旧入口，不能因新接口假称旧正文完整。 */
    @Transactional(rollbackFor = Exception.class)
    public Result restore(String space, Long owner, Long user, Long fileId) {
        requireSpace(space, owner, user);
        batches.lockSpace(space, owner);
        EsFile selected = files.selectById(fileId);
        requireOwned(selected, space, owner);
        if (selected.getRecycleBatchId() == null) {
            EsFile restored = fileService.restoreFromRecycle(space, owner, fileId);
            usage.invalidate(prefix(space, owner));
            return new Result(null, null, String.valueOf(restored.getFileId()), restored.getFilePath(), restored.directoryEntry(), "restored", 1);
        }
        EsRecycleBatch batch = batches.selectById(selected.getRecycleBatchId());
        requireBatch(batch, space, owner);
        if ("restored".equals(batch.getStatus())) { return view(batch); }
        if (!"recycled".equals(batch.getStatus()) && !"restoring".equals(batch.getStatus())) { throw new ServiceException("回收站操作正在处理，请查询后重试"); }
        List<EsFile> members = members(batch);
        if (members.size() != batch.getMemberCount()) { throw new ServiceException("删除批次成员不完整，禁止部分恢复"); }
        String prefix = prefix(space, owner);
        for (EsFile row : members) {
            if (!EsFile.STATE_RECYCLED.equals(row.getState())) { throw new ServiceException("批次状态已变化"); }
            if (EsFile.TYPE_SHARED.equals(space)) { roles.assertManage(row, user); }
            assertRestoreDestination(space, owner, row, members);
            if (!row.directoryEntry()) {
                DriveRecycleKeys.requireOwnedPath(row.getRecycleStoragePath(), batch.getBatchId(), row.getFileId(), row.getRecycleSnapshotVersion());
                DriveStorage.ObjectMetadata saved = storage.metadataAt(prefix, row.getRecycleStoragePath());
                if (saved == null || !Objects.equals(saved.etag(), row.getRecycleSnapshotEtag()) || saved.size() != row.getRecycleSnapshotSize()
                    || row.getRecycleSnapshotSha256() == null || !row.getRecycleSnapshotSha256().equals(storage.sha256At(prefix, row.getRecycleStoragePath()))) {
                    throw new ServiceException("回收站正文不完整，未还原");
                }
                DriveStorage.ObjectMetadata existing = storage.metadataAt(prefix, row.getOriginPath());
                if (existing != null && (existing.size() != saved.size()
                    || !row.getRecycleSnapshotSha256().equals(storage.sha256At(prefix, row.getOriginPath())))) {
                    throw new ServiceException("原位置存在不同正文，禁止覆盖");
                }
            }
        }
        batch.setStatus("restoring"); batch.setUpdatedAt(LocalDateTime.now()); ledger.checkpoint(batch);
        // 副本在原路径，失败仍保留回收站快照；重试按同一批次核验，不产生新fileId。
        for (EsFile row : members) {
            if (!row.directoryEntry()) {
                storage.copyAt(prefix, row.getRecycleStoragePath(), row.getOriginPath());
                DriveStorage.ObjectMetadata restored = storage.metadataAt(prefix, row.getOriginPath());
                if (restored == null || restored.size() != row.getRecycleSnapshotSize()
                    || !row.getRecycleSnapshotSha256().equals(storage.sha256At(prefix, row.getOriginPath()))) {
                    throw new ServiceException("还原正文核验失败，回收站副本保留");
                }
            }
        }
        members.sort(Comparator.comparingInt(row -> row.getFilePath().length()));
        for (EsFile row : members) {
            int changed = files.update(null, new LambdaUpdateWrapper<EsFile>().eq(EsFile::getFileId, row.getFileId())
                .eq(EsFile::getState, EsFile.STATE_RECYCLED).set(EsFile::getState, EsFile.STATE_ACTIVE)
                .set(EsFile::getDeletedAt, null).set(EsFile::getOriginPath, null));
            if (changed != 1) { throw new ServiceException("恢复状态未提交，回收站正文保留"); }
            row.setState(EsFile.STATE_ACTIVE);
            changes.record(space, owner, EsFileChangeLog.EVENT_RESTORED, row, row.getFileId(), row.getFilePath(), null);
        }
        batch.setStatus("restored"); batch.setRestoredAt(LocalDateTime.now()); batch.setUpdatedAt(batch.getRestoredAt());
        batches.updateById(batch); usage.invalidate(prefix);
        return view(batch);
    }

    /** 查询仅限当前空间和操作者，网络超时后用原operationId核对真实结果。 */
    public Result operation(String space, Long owner, Long user, String operationId) {
        requireSpace(space, owner, user);
        EsRecycleBatch batch = byOperation(space, owner, user, DriveRecycleKeys.requireToken(operationId));
        if (batch == null) { throw new ServiceException("删除操作尚未提交，请保留本机内容"); }
        return view(batch);
    }

    /** 新回收站条目只显示删除根；旧数据仍逐条展示。 */
    public List<EsFile> list(String space, Long owner) {
        return fileService.listRecycled(space, owner).stream()
            .filter(row -> row.getRecycleBatchId() == null || Boolean.TRUE.equals(row.getRecycleRoot())).toList();
    }

    /** 彻底删除仅作用于准确批次；新正文快照与后来同名active文件互不影响。 */
    @Transactional(rollbackFor = Exception.class)
    public Result purge(String space, Long owner, Long user, Long fileId) {
        requireSpace(space, owner, user);
        batches.lockSpace(space, owner);
        EsFile selected = files.selectById(fileId);
        requireOwned(selected, space, owner);
        if (!EsFile.STATE_RECYCLED.equals(selected.getState())) { throw new ServiceException("只允许彻底删除回收站条目"); }
        String prefix = prefix(space, owner);
        if (selected.getRecycleBatchId() == null) {
            DriveFileService.PurgeResult legacy = fileService.purgeFromRecycle(space, owner, fileId);
            if (legacy.deletable()) { storage.deleteAt(prefix, legacy.row().getFilePath()); }
            usage.invalidate(prefix);
            return new Result(null, null, String.valueOf(fileId), selected.getFilePath(), selected.directoryEntry(), "purged", 1);
        }
        EsRecycleBatch batch = batches.selectById(selected.getRecycleBatchId());
        requireBatch(batch, space, owner);
        if (!"recycled".equals(batch.getStatus()) && !"purging".equals(batch.getStatus())) { throw new ServiceException("批次不能彻底删除，请刷新状态"); }
        List<EsFile> members = members(batch);
        if ("purging".equals(batch.getStatus())) { members = ledger.decodeMembers(batch.getMembersJson()); }
        if (members.size() != batch.getMemberCount()) { throw new ServiceException("回收站批次成员不完整"); }
        for (EsFile row : members) {
            requireOwned(row, space, owner);
            if (EsFile.TYPE_SHARED.equals(space)) { roles.assertManage(row, user); }
            if (!row.directoryEntry()) {
                DriveRecycleKeys.requireOwnedPath(row.getRecycleStoragePath(), batch.getBatchId(), row.getFileId(), row.getRecycleSnapshotVersion());
            }
        }
        batch.setMembersJson(ledger.encodeMembers(members));
        batch.setStatus("purging"); batch.setUpdatedAt(LocalDateTime.now()); ledger.checkpoint(batch);
        // 从此不再允许还原；每次删除只命中冻结的独立快照，失败后继续同一批次。
        for (EsFile row : members) {
            if (!row.directoryEntry()) { storage.deleteAt(prefix, row.getRecycleStoragePath()); }
            EsFile current = files.selectById(row.getFileId());
            if (current != null) {
                if (!EsFile.STATE_RECYCLED.equals(current.getState()) || !batch.getBatchId().equals(current.getRecycleBatchId())) {
                    throw new ServiceException("清理对象身份变化，已停止");
                }
                DriveFileService.PurgeResult purged = fileService.purgeFromRecycle(space, owner, row.getFileId());
                if (purged.deletable()) {
                    DriveStorage.ObjectMetadata original = storage.metadataAt(prefix, row.getFilePath());
                    if (original != null && original.size() == row.getRecycleSnapshotSize()
                        && row.getRecycleSnapshotSha256() != null
                        && row.getRecycleSnapshotSha256().equals(storage.sha256At(prefix, row.getFilePath()))
                        && original.equals(storage.metadataAt(prefix, row.getFilePath()))) {
                        // CopyObject 后分片 ETag 可改变；正文身份按已保存SHA核对，
                        // 再次HEAD确认期间未换对象，不留下会被清单重新补账的旧正文。
                        storage.deleteAt(prefix, row.getFilePath());
                    }
                }
            }
        }
        batch.setStatus("purged"); batch.setUpdatedAt(LocalDateTime.now());
        batches.updateById(batch); usage.invalidate(prefix);
        return view(batch);
    }

    /** 仅供保留期调度调用；未知旧共享归属保守保留，不能借系统清理绕过权限。 */
    @Transactional(rollbackFor = Exception.class)
    public Result purgeExpiredFile(EsFile row) {
        if (row == null || !EsFile.STATE_RECYCLED.equals(row.getState()) || row.getDeletedAt() == null
            || !row.getDeletedAt().isBefore(LocalDateTime.now().minusDays(RecycleJanitor.RETENTION_DAYS))) {
            throw new ServiceException("条目不属于可清理的过期回收站");
        }
        Long user = row.getOwnerId();
        if (EsFile.TYPE_SHARED.equals(row.getSpaceType())) {
            if (row.getRecycleBatchId() != null) {
                EsRecycleBatch batch = batches.selectById(row.getRecycleBatchId());
                requireBatch(batch, row.getSpaceType(), row.getOwnerId());
                user = batch.getOperatorId();
            } else { user = row.getUploadBy(); }
            if (user == null || user <= 0) { throw new ServiceException("旧共享条目缺少可核实操作者，保留正文"); }
        }
        return purge(row.getSpaceType(), row.getOwnerId(), user, row.getFileId());
    }

    private void assertRestoreDestination(String space, Long owner, EsFile row, List<EsFile> members) {
        String path = DriveKeys.normalizeRelative(row.getOriginPath());
        if (fileService.activeAt(space, owner, path) != null) {
            throw new ServiceException("原位置已有同名文件，请先处理现有文件后再还原");
        }
        for (int slash = path.indexOf('/'); slash >= 0; slash = path.indexOf('/', slash + 1)) {
            EsFile ancestor = fileService.activeAt(space, owner, path.substring(0, slash));
            if (ancestor != null && !ancestor.directoryEntry()) {
                throw new ServiceException("父路径已被文件占用，未恢复目录批次");
            }
        }
        if (!row.directoryEntry() && ownedRows(space, owner).stream().anyMatch(other -> active(other)
            && other.getFilePath().startsWith(path + "/"))) {
            throw new ServiceException("原位置已有目录子项，禁止覆盖");
        }
    }

    private List<EsFile> ownedRows(String space, Long owner) {
        return files.selectList(new LambdaQueryWrapper<EsFile>().eq(EsFile::getSpaceType, space).eq(EsFile::getOwnerId, owner));
    }
    private List<EsFile> members(EsRecycleBatch batch) {
        return files.selectList(new LambdaQueryWrapper<EsFile>().eq(EsFile::getSpaceType, batch.getSpaceType())
            .eq(EsFile::getOwnerId, batch.getOwnerId()).eq(EsFile::getRecycleBatchId, batch.getBatchId()));
    }
    private EsRecycleBatch byOperation(String space, Long owner, Long user, String operation) {
        return batches.selectOne(new LambdaQueryWrapper<EsRecycleBatch>().eq(EsRecycleBatch::getSpaceType, space)
            .eq(EsRecycleBatch::getOwnerId, owner).eq(EsRecycleBatch::getOperatorId, user)
            .eq(EsRecycleBatch::getOperationId, operation));
    }
    private static Result view(EsRecycleBatch batch) {
        return new Result(batch.getBatchId(), batch.getOperationId(), String.valueOf(batch.getRootFileId()),
            batch.getRootPath(), Boolean.TRUE.equals(batch.getDirectory()), batch.getStatus(), batch.getMemberCount());
    }
    private static boolean active(EsFile row) { return row.getState() == null || EsFile.STATE_ACTIVE.equals(row.getState()); }
    private static long version(EsFile row) { return row.getVersionId() == null ? 0L : row.getVersionId(); }
    private static long size(EsFile row) { return row.getFileSize() == null ? 0L : row.getFileSize(); }
    private static String prefix(String space, Long owner) { return EsFile.TYPE_SHARED.equals(space) ? DriveKeys.sharedPrefix() : DriveKeys.userPrefix(owner); }
    private static void requireSpace(String space, Long owner, Long user) {
        if (user == null || user <= 0 || owner == null
            || EsFile.TYPE_PERSONAL.equals(space) && (!owner.equals(user) || owner <= 0)
            || EsFile.TYPE_SHARED.equals(space) && owner != 0L
            || !EsFile.TYPE_PERSONAL.equals(space) && !EsFile.TYPE_SHARED.equals(space)) {
            throw new ServiceException("回收站空间归属无效");
        }
    }
    private static void requireOwned(EsFile row, String space, Long owner) {
        if (row == null || !space.equals(row.getSpaceType()) || !owner.equals(row.getOwnerId())) {
            throw new ServiceException("文件不存在或不属于当前空间");
        }
    }
    private static void requireBatch(EsRecycleBatch batch, String space, Long owner) {
        if (batch == null || !space.equals(batch.getSpaceType()) || !owner.equals(batch.getOwnerId())) {
            throw new ServiceException("删除批次不存在或不属于当前空间");
        }
    }
}
