package org.dromara.easyshare.drive.service;

import lombok.RequiredArgsConstructor;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.*;
import org.dromara.easyshare.drive.domain.*;
import org.dromara.easyshare.drive.mapper.EsFileMapper;
import org.springframework.stereotype.Service;
import java.util.*;

/** 知识源文件的实时权限：先空间门再文件角色，未登记和非 active 一律拒绝。 */
@Service
@RequiredArgsConstructor
public class KnowledgeAclService {
    private final EsFileMapper files;
    private final SpaceService spaces;
    private final FileRoleService roles;
    private final FeaturePermissionService features;
    private final DriveStorage storage;
    private final DriveProperties properties;

    public record AccessVo(String fileId, boolean read, boolean download, String objectKey) {}

    public record BindingVo(String authority, String fileId, String versionId, String objectKey,
                            String bucket, String sourceSha256, long sourceBytes, String filename) {}
    @org.springframework.transaction.annotation.Transactional(rollbackFor = Exception.class)
    public BindingVo bind(KnowledgeAclController.BindBo body, Long userId) {
        if (body == null || (body.fileId() == null) == (body.objectKey() == null))
            throw new ServiceException("来源定位参数非法");
        var query = com.baomidou.mybatisplus.core.toolkit.Wrappers.<EsFile>query();
        if (body.fileId() != null) {
            query.eq("file_id", parseId(body.fileId()));
        } else {
            if (body.expectedVersionId() != null) throw new ServiceException("来源定位参数非法");
            String key = body.objectKey();
            String prefix;
            if (key.startsWith("shared/")) {
                prefix = "shared/";
                query.eq("space_type", "shared").eq("owner_id", 0L);
            } else if (key.matches("users/[1-9][0-9]{0,18}/.+")) {
                String owner = key.split("/", 3)[1];
                prefix = "users/" + owner + "/";
                query.eq("space_type", "personal").eq("owner_id", parseId(owner));
            } else throw new ServiceException("来源命名空间不支持");
            String path = DriveKeys.requireUserPath(key.substring(prefix.length()));
            if (!key.equals(prefix + path)) throw new ServiceException("来源键不是规范路径");
            query.eq("file_path", path).eq("state", "active");
        }
        EsFile row = files.selectOne(query.last("FOR UPDATE"));
        int required = body.objectKey() != null && "read".equals(body.action()) ? 1 : 2;
        if (body.action() != null && !Set.of("read", "download").contains(body.action()))
            throw new ServiceException("来源动作非法");
        if (FileRoleService.rank(roleOf(row, userId)) < required)
            throw new ServiceException("知识来源权限不足");
        if (body.fileId() != null && (row.getVersionId() == null || body.expectedVersionId() == null
            || !body.expectedVersionId().matches("[0-9]{1,19}")
            || !row.getVersionId().toString().equals(body.expectedVersionId())))
            throw new ServiceException("网盘版本已变化，请刷新后重新入库");
        String key = keyOf(row);
        if (body.objectKey() != null && !body.objectKey().equals(key))
            throw new ServiceException("来源登记不匹配");
        String prefix = EsFile.TYPE_SHARED.equals(row.getSpaceType()) ? DriveKeys.sharedPrefix() : DriveKeys.userPrefix(row.getOwnerId());
        var metadata = storage.metadataAt(prefix, row.getFilePath());
        if (metadata == null) throw new ServiceException("网盘原件不存在");
        String hash = storage.sha256At(prefix, row.getFilePath());
        if (hash == null || !hash.matches("[a-f0-9]{64}")) throw new ServiceException("来源摘要不可确认");
        return new BindingVo("ruoyi", row.getFileId().toString(), String.valueOf(row.getVersionId()), key,
            properties.getBucket(), hash, metadata.size(), row.getFileName());
    }

    public List<AccessVo> access(List<String> ids, Long userId) {
        if (ids == null || ids.size() > 200) throw new ServiceException("文件列表最多 200 项");
        List<Long> numeric = ids.stream().map(KnowledgeAclService::parseId).toList();
        Map<Long, EsFile> rows = new HashMap<>();
        if (!numeric.isEmpty()) for (EsFile row : files.selectByIds(numeric)) rows.put(row.getFileId(), row);
        return numeric.stream().map(id -> {
            EsFile row = rows.get(id);
            String role = roleOf(row, userId);
            return new AccessVo(id.toString(), FileRoleService.rank(role) >= 1,
                FileRoleService.rank(role) >= 2, role == null ? null : keyOf(row));
        }).toList();
    }

    private String roleOf(EsFile row, Long userId) {
        if (userId == null || row == null || row.directoryEntry() || !EsFile.STATE_ACTIVE.equals(row.getState())
            || !features.enabled(userId, FeaturePermissionService.KNOWLEDGE)
            || !features.enabled(userId, FeaturePermissionService.DRIVE)) return null;
        if (EsFile.TYPE_PERSONAL.equals(row.getSpaceType())) {
            EsSpace personal = spaces.findPersonal(userId);
            return Objects.equals(userId, row.getOwnerId()) && personal != null && "0".equals(personal.getStatus())
                && personal.getQuotaBytes() != null && personal.getQuotaBytes() != 0 ? FileRoleService.ROLE_MANAGE : null;
        }
        if (!EsFile.TYPE_SHARED.equals(row.getSpaceType()) || !Objects.equals(row.getOwnerId(), 0L)
            || !features.sharedFeatureEnabled(userId) || spaces.sharedPermissionOf(userId) == null) return null;
        EsSpace shared = spaces.findShared();
        if (!"0".equals(shared.getStatus()) || shared.getQuotaBytes() == null || shared.getQuotaBytes() == 0) return null;
        return roles.effective(row, userId);
    }

    private static String keyOf(EsFile row) {
        return DriveKeys.keyAt(EsFile.TYPE_SHARED.equals(row.getSpaceType()) ? DriveKeys.sharedPrefix()
            : DriveKeys.userPrefix(row.getOwnerId()), DriveKeys.requireUserPath(row.getFilePath()));
    }
    private static Long parseId(String id) {
        try {
            if (id == null || !id.matches("[1-9][0-9]{0,18}")) throw new NumberFormatException();
            return Long.parseLong(id);
        } catch (NumberFormatException ex) { throw new ServiceException("文件标识非法"); }
    }
}
