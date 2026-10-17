package org.dromara.easyshare.drive.service;

import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.baomidou.mybatisplus.core.toolkit.Wrappers;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.DriveStorage;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.domain.EsFileVersion;
import org.dromara.easyshare.drive.mapper.EsFileMapper;
import org.dromara.easyshare.drive.mapper.EsFileVersionMapper;
import org.springframework.dao.DuplicateKeyException;
import org.springframework.stereotype.Service;

import java.time.LocalDateTime;
import java.util.List;

/**
 * 文件版本历史：覆盖前留档、查历史、回滚。
 *
 * <p><b>为什么留档必须发生在 presign-put</b>：客户端直传对象存储，覆盖写发生在
 * 「拿 URL」和「登记」两次 API 调用之间，控制面根本没有中间机会。等服务端收到
 * register 时，旧字节已经被新内容替换——那时候再想留档已经晚了。所以抢在签发
 * URL 之前做一次 copyObject（服务端复制，不耗客户端带宽）。
 *
 * <p>代价是：客户端拿了 URL 却没真正上传时，会多出一份无用的版本副本。
 * 这是「可能浪费一点存储」与「可能永久丢失用户内容」之间的取舍，选前者。
 *
 * @author EasyShare
 */
@Slf4j
@Service
@RequiredArgsConstructor
public class FileVersionService {

    private final EsFileVersionMapper versionMapper;
    private final EsFileMapper fileMapper;
    private final DriveStorage storage;

    /**
     * 覆盖前留档：把当前对象复制到版本前缀，并写一行历史。
     *
     * <p>幂等：同一个 (fileId, versionId) 只留一份，presign 重试不会产生重复历史。
     *
     * @param prefix 空间前缀（调用方已做权限校验）
     */
    public void snapshot(Long userId, String spaceType, Long ownerId, String prefix, String path) {
        EsFile row = fileMapper.selectOne(Wrappers.<EsFile>lambdaQuery()
            .eq(EsFile::getSpaceType, spaceType)
            .eq(EsFile::getOwnerId, ownerId)
            .eq(EsFile::getFilePath, path)
            .eq(EsFile::getState, EsFile.STATE_ACTIVE));
        if (row == null) {
            // 首次上传：没有旧内容可留
            return;
        }
        long version = row.getVersionId() == null ? 0L : row.getVersionId();
        Long existing = versionMapper.selectCount(Wrappers.<EsFileVersion>lambdaQuery()
            .eq(EsFileVersion::getFileId, row.getFileId())
            .eq(EsFileVersion::getVersionId, version));
        if (existing != null && existing > 0) {
            return;
        }
        String storagePath = EsFileVersion.storagePathOf(row.getFilePath(), version);
        storage.copyAt(prefix, row.getFilePath(), storagePath);

        EsFileVersion record = new EsFileVersion();
        record.setFileId(row.getFileId());
        record.setVersionId(version);
        record.setSpaceType(spaceType);
        record.setOwnerId(ownerId);
        record.setFilePath(row.getFilePath());
        record.setFileSize(row.getFileSize() == null ? 0L : row.getFileSize());
        record.setContentMd5(row.getContentMd5());
        record.setStoragePath(storagePath);
        record.setCreatedAt(LocalDateTime.now());
        record.setCreatedBy(userId);
        try {
            versionMapper.insert(record);
        } catch (DuplicateKeyException ex) {
            // 两次 presign 并发都判定「没留过」：唯一索引挡住第二行，
            // 对象已经被复制过一次，这里静默即可（内容相同）
            log.debug("版本已由并发请求留档：fileId={} version={}", row.getFileId(), version);
        }
    }

    /** 某文件的全部历史版本，按版本号倒序 */
    public List<EsFileVersion> list(String spaceType, Long ownerId, String path) {
        return versionMapper.selectList(Wrappers.<EsFileVersion>lambdaQuery()
            .eq(EsFileVersion::getSpaceType, spaceType)
            .eq(EsFileVersion::getOwnerId, ownerId)
            .eq(EsFileVersion::getFilePath, path)
            .orderByDesc(EsFileVersion::getVersionId));
    }

    /**
     * 回滚到指定版本。
     *
     * <p>先把**当前内容**留一份档再覆盖——否则「回滚」本身会变成一次不可撤销的丢失。
     * 回滚后主版本号继续 +1，而不是退回旧号：version_id 单调是乐观并发校验的前提，
     * 退号会让所有客户端手里的 expectedVersion 瞬间失效。
     */
    public void restore(Long userId, String spaceType, Long ownerId, String prefix,
                        String path, long versionId) {
        EsFileVersion target = versionMapper.selectOne(Wrappers.<EsFileVersion>lambdaQuery()
            .eq(EsFileVersion::getSpaceType, spaceType)
            .eq(EsFileVersion::getOwnerId, ownerId)
            .eq(EsFileVersion::getFilePath, path)
            .eq(EsFileVersion::getVersionId, versionId));
        if (target == null) {
            throw new ServiceException("该版本不存在或已清理，请刷新后重试");
        }

        // 1) 先留当前档，回滚动作本身也要可撤销
        snapshot(userId, spaceType, ownerId, prefix, path);
        // 2) 版本对象复制回主路径
        storage.copyAt(prefix, target.getStoragePath(), path);
        // 3) 主版本号继续递增（条件更新，防止与并发上传互相覆盖）
        EsFile current = fileMapper.selectOne(Wrappers.<EsFile>lambdaQuery()
            .eq(EsFile::getSpaceType, spaceType)
            .eq(EsFile::getOwnerId, ownerId)
            .eq(EsFile::getFilePath, path)
            .eq(EsFile::getState, EsFile.STATE_ACTIVE));
        if (current == null) {
            return;
        }
        long now = current.getVersionId() == null ? 0L : current.getVersionId();
        int changed = fileMapper.update(null, new LambdaUpdateWrapper<EsFile>()
            .eq(EsFile::getFileId, current.getFileId())
            .eq(EsFile::getVersionId, now)
            .set(EsFile::getVersionId, now + 1L)
            .set(EsFile::getFileSize, target.getFileSize()));
        if (changed == 0) {
            throw new ServiceException("恢复期间文件被其他人修改，请刷新后重试");
        }
    }
}
