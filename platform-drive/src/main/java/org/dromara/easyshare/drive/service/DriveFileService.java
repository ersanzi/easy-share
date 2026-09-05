package org.dromara.easyshare.drive.service;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.DriveKeys;
import org.dromara.easyshare.drive.DriveObject;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.domain.EsSpace;
import org.dromara.easyshare.drive.domain.EsSpaceMember;
import org.dromara.easyshare.drive.mapper.EsFileMapper;
import org.springframework.dao.DuplicateKeyException;
import org.springframework.stereotype.Service;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;

/**
 * 文件目录层服务：稳定 fileId 与空间内路径的绑定、列表回填与惰性补账。
 * <p>
 * 登记时机（控制面不在上传数据路径上，见 {@link EsFile} 类注）：
 * <ul>
 *   <li>presignPut 签发时 upsert——多数文件自此有稳定身份；</li>
 *   <li>列表时惰性补账——目录层上线前的存量对象在第一次被列举时自愈归账。</li>
 * </ul>
 * 本表不是存在性真相源：幽灵行（登记后从未写入）不影响列表正确性，
 * 会在删除或后续对账切片中清理。
 *
 * @author EasyShare
 */
@Slf4j
@Service
@RequiredArgsConstructor
public class DriveFileService {

    private final EsFileMapper fileMapper;

    /**
     * presignPut 签发时登记（幂等 upsert）。
     *
     * @param spaceType    personal / shared
     * @param ownerId      空间归属（personal=用户，shared=0）
     * @param uploadBy     真实上传者
     * @param relativePath 客户端相对路径（内部规范化）
     * @param declaredSize 申报字节数（可能为 0/未知，保持原值不覆盖）
     */
    public void registerOnPresign(String spaceType, Long ownerId, Long uploadBy,
                                  String relativePath, long declaredSize) {
        String path = DriveKeys.normalizeRelative(relativePath);
        EsFile existing = selectByPath(spaceType, ownerId, path);
        if (existing == null) {
            insertSafely(spaceType, ownerId, uploadBy, path, declaredSize);
            return;
        }
        if (declaredSize > 0) {
            existing.setFileSize(declaredSize);
            fileMapper.updateById(existing);
        }
    }

    /**
     * 列表回填：为存储列举结果补 fileId；缺行的（存量/绕过登记写入）惰性补账。
     *
     * @return path → fileId（与入参 objects 顺序一致的一一映射）
     */
    public Map<String, Long> reconcileAndMap(String spaceType, Long ownerId, Long uploadBy,
                                             List<DriveObject> objects) {
        Map<String, EsFile> byPath = new HashMap<>();
        for (EsFile row : fileMapper.selectList(new LambdaQueryWrapper<EsFile>()
            .eq(EsFile::getSpaceType, spaceType)
            .eq(EsFile::getOwnerId, ownerId))) {
            byPath.put(row.getFilePath(), row);
        }

        Map<String, Long> result = new HashMap<>(objects.size());
        for (DriveObject object : objects) {
            String path;
            try {
                path = DriveKeys.normalizeRelative(object.path());
            } catch (ServiceException ex) {
                // 存储里出现不合规键（历史脏数据）：照常返回，不补账
                log.warn("es_file 对账跳过不合规路径: {}", object.path());
                result.put(object.path(), null);
                continue;
            }
            EsFile row = byPath.get(path);
            if (row == null) {
                row = insertSafely(spaceType, ownerId, uploadBy, path, object.size());
            }
            result.put(object.path(), row.getFileId());
        }
        return result;
    }

    /**
     * 解析删除目标：fileId 优先（新链路），否则按路径。fileId 必须命中指定空间
     * 且归属校验通过，防止伪造他人 fileId 越权删文件。
     *
     * @return 规范化后的相对路径
     */
    public String resolveDeletePath(String spaceType, Long ownerId, Long fileId, String rawPath) {
        if (fileId != null) {
            EsFile row = fileMapper.selectById(fileId);
            if (row == null
                || !spaceType.equals(row.getSpaceType())
                || !ownerId.equals(row.getOwnerId())) {
                throw new ServiceException("文件不存在或不属于当前空间");
            }
            return row.getFilePath();
        }
        return DriveKeys.normalizeRelative(rawPath);
    }

    /**
     * 设置文档可见部门（文档级可见性片 2 数据面）。deptIds 空 = 恢复全体可见。
     * 仅共享空间文件有此语义；目录行不存在时报错。
     *
     * @return 更新后的逗号分隔部门串（空串=全体可见）
     */
    public String setRegisteredVisibleDepts(String spaceType, Long ownerId, Long operatorId,
                                            String path, List<Long> deptIds) {
        EsFile row = selectByPath(spaceType, ownerId, path);
        if (row == null) {
            throw new ServiceException("文件不存在或未登记目录层");
        }
        // 操作者校验：个人空间=owner 本人；共享空间=上传者本人（上传者总能管理自己的文件）
        boolean allowed = EsSpace.TYPE_PERSONAL.equals(spaceType)
            ? ownerId.equals(row.getOwnerId())
            : operatorId != null && operatorId.equals(row.getUploadBy());
        if (!allowed) {
            throw new ServiceException("只有文件的上传者可以调整可见范围");
        }
        row.setVisibleDepts(serializeDepts(deptIds));
        fileMapper.updateById(row);
        return row.getVisibleDepts();
    }

    /**
     * 共享列表可见性过滤：visible_depts 为空的行全体可见；非空时仅
     * 用户所属部门命中或上传者本人可见（上传者总能看到自己传的文件）。
     *
     * @param uploaderOf path → 上传者（供"自己可见"判断；查询不到按 0 处理）
     */
    public List<DriveObject> filterSharedVisible(List<DriveObject> objects,
                                                 Map<String, EsFile> rowsByPath,
                                                 Long viewerDeptId, Long viewerUserId) {
        List<DriveObject> visible = new ArrayList<>(objects.size());
        for (DriveObject object : objects) {
            EsFile row = rowsByPath.get(object.path());
            if (row == null || row.getVisibleDepts() == null || row.getVisibleDepts().isBlank()) {
                visible.add(object);
                continue;
            }
            if (viewerUserId != null && Objects.equals(row.getUploadBy(), viewerUserId)) {
                visible.add(object);
                continue;
            }
            if (viewerDeptId != null && deptListContains(row.getVisibleDepts(), viewerDeptId)) {
                visible.add(object);
            }
        }
        return visible;
    }

    public static String serializeDepts(List<Long> deptIds) {
        if (deptIds == null || deptIds.isEmpty()) {
            return "";
        }
        return deptIds.stream().map(String::valueOf).collect(java.util.stream.Collectors.joining(","));
    }

    public static boolean deptListContains(String csv, long deptId) {
        for (String part : csv.split(",")) {
            if (part.trim().equals(String.valueOf(deptId))) {
                return true;
            }
        }
        return false;
    }

    /**
     * 目录行按 path 索引（列表可见性过滤用）。
     */
    public Map<String, EsFile> rowsByPath(String spaceType, Long ownerId) {
        Map<String, EsFile> byPath = new HashMap<>();
        for (EsFile row : fileMapper.selectList(new LambdaQueryWrapper<EsFile>()
            .eq(EsFile::getSpaceType, spaceType)
            .eq(EsFile::getOwnerId, ownerId))) {
            byPath.put(row.getFilePath(), row);
        }
        return byPath;
    }

    /**
     * 按路径查稳定 fileId；目录行不存在返回 null（Upload Session 完成后回查用）。
     */
    public Long fileIdOfPath(String spaceType, Long ownerId, String path) {
        EsFile row = selectByPath(spaceType, ownerId, path);
        return row == null ? null : row.getFileId();
    }

    /**
     * 删除文件后清理目录行；行不存在视为已清理。
     */
    public void deleteRegistered(String spaceType, Long ownerId, String path) {
        fileMapper.delete(new LambdaQueryWrapper<EsFile>()
            .eq(EsFile::getSpaceType, spaceType)
            .eq(EsFile::getOwnerId, ownerId)
            .eq(EsFile::getFilePath, path));
    }

    private EsFile selectByPath(String spaceType, Long ownerId, String path) {
        return fileMapper.selectOne(new LambdaQueryWrapper<EsFile>()
            .eq(EsFile::getSpaceType, spaceType)
            .eq(EsFile::getOwnerId, ownerId)
            .eq(EsFile::getFilePath, path)
            .last("limit 1"));
    }

    /**
     * 插入；并发撞唯一键时按已存在行处理（返回已存在行），不让列表/签发失败。
     */
    private EsFile insertSafely(String spaceType, Long ownerId, Long uploadBy,
                                String path, long size) {
        EsFile row = new EsFile();
        row.setSpaceType(spaceType);
        row.setOwnerId(ownerId);
        row.setUploadBy(uploadBy);
        row.setFilePath(path);
        row.setFileName(fileNameOf(path));
        row.setFileSize(Math.max(0L, size));
        try {
            fileMapper.insert(row);
            return row;
        } catch (DuplicateKeyException ex) {
            EsFile existing = selectByPath(spaceType, ownerId, path);
            if (existing != null) {
                return existing;
            }
            throw ex;
        }
    }

    private static String fileNameOf(String path) {
        int index = path.lastIndexOf('/');
        return index < 0 ? path : path.substring(index + 1);
    }
}
