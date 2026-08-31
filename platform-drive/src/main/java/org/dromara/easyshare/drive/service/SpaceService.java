package org.dromara.easyshare.drive.service;

import com.baomidou.mybatisplus.core.toolkit.Wrappers;
import lombok.RequiredArgsConstructor;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.DriveKeys;
import org.dromara.easyshare.drive.domain.EsSpace;
import org.dromara.easyshare.drive.domain.EsSpaceMember;
import org.dromara.easyshare.drive.mapper.EsSpaceMapper;
import org.dromara.easyshare.drive.mapper.EsSpaceMemberMapper;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.util.List;

/**
 * 空间授权与配额。
 * <p>
 * 这里是「一个地方砍空间」的落点：共享空间与个人空间的容量都从这里设定。
 *
 * @author EasyShare
 */
@Service
@RequiredArgsConstructor
public class SpaceService {

    private final EsSpaceMapper spaceMapper;
    private final EsSpaceMemberMapper memberMapper;
    private final SpaceUsageService usageService;
    private final CapacityService capacityService;

    /**
     * 取个人空间，未开通返回 null。
     *
     * @param userId 用户 ID
     * @return 空间，或 null
     */
    public EsSpace findPersonal(Long userId) {
        return spaceMapper.selectOne(Wrappers.<EsSpace>lambdaQuery()
            .eq(EsSpace::getSpaceType, EsSpace.TYPE_PERSONAL)
            .eq(EsSpace::getOwnerId, userId));
    }

    /**
     * 取共享空间。DDL 预置，正常不会缺失。
     *
     * @return 共享空间
     */
    public EsSpace findShared() {
        EsSpace shared = spaceMapper.selectById(EsSpace.SHARED_SPACE_ID);
        if (shared == null) {
            throw new ServiceException("共享空间未初始化，请执行 deploy/ruoyi-db/easyshare-space.sql");
        }
        return shared;
    }

    /**
     * 列举全部个人空间，供管理页按账号显示配额。
     *
     * @return 个人空间列表
     */
    public List<EsSpace> listPersonal() {
        return spaceMapper.selectList(Wrappers.<EsSpace>lambdaQuery()
            .eq(EsSpace::getSpaceType, EsSpace.TYPE_PERSONAL));
    }

    /**
     * 列举全部空间（含共享），供容量总览统计已承诺总量。
     *
     * @return 全部空间
     */
    public List<EsSpace> listAll() {
        return spaceMapper.selectList(Wrappers.<EsSpace>lambdaQuery());
    }

    /**
     * 设定某账号的个人空间容量。没有空间行则开通，有则改容量——管理页的「开通」与
     * 「改配额」是同一个动作，不需要两个接口。
     *
     * @param userId     账号 ID
     * @param quotaBytes 配额字节数：0 收回（待开空间）、-1 不限
     */
    @Transactional(rollbackFor = Exception.class)
    public void setPersonalQuota(Long userId, long quotaBytes) {
        if (userId == null || userId <= 0) {
            throw new ServiceException("账号标识非法");
        }
        validateQuota(quotaBytes);
        EsSpace existing = findPersonal(userId);
        if (existing == null) {
            EsSpace space = new EsSpace();
            space.setSpaceType(EsSpace.TYPE_PERSONAL);
            space.setOwnerId(userId);
            space.setSpaceName("个人空间");
            space.setQuotaBytes(quotaBytes);
            space.setStatus("0");
            spaceMapper.insert(space);
            return;
        }
        existing.setQuotaBytes(quotaBytes);
        spaceMapper.updateById(existing);
    }

    /**
     * 设定共享空间容量。
     *
     * @param quotaBytes 配额字节数
     */
    public void setSharedQuota(long quotaBytes) {
        validateQuota(quotaBytes);
        EsSpace shared = findShared();
        shared.setQuotaBytes(quotaBytes);
        spaceMapper.updateById(shared);
    }

    /**
     * 授予或撤销某账号对共享空间的权限。
     *
     * @param userId     账号 ID
     * @param permission read 只读、write 读写、null/空 撤销
     */
    @Transactional(rollbackFor = Exception.class)
    public void grantShared(Long userId, String permission) {
        if (userId == null || userId <= 0) {
            throw new ServiceException("账号标识非法");
        }
        EsSpaceMember existing = memberMapper.selectOne(Wrappers.<EsSpaceMember>lambdaQuery()
            .eq(EsSpaceMember::getSpaceId, EsSpace.SHARED_SPACE_ID)
            .eq(EsSpaceMember::getUserId, userId));
        if (permission == null || permission.isBlank()) {
            if (existing != null) {
                memberMapper.deleteById(existing.getId());
            }
            return;
        }
        if (!EsSpaceMember.PERM_READ.equals(permission) && !EsSpaceMember.PERM_WRITE.equals(permission)) {
            throw new ServiceException("权限取值只能是 read 或 write");
        }
        if (existing == null) {
            EsSpaceMember member = new EsSpaceMember();
            member.setSpaceId(EsSpace.SHARED_SPACE_ID);
            member.setUserId(userId);
            member.setPermission(permission);
            memberMapper.insert(member);
            return;
        }
        existing.setPermission(permission);
        memberMapper.updateById(existing);
    }

    /**
     * 列举共享空间的成员授权。
     *
     * @return 成员列表
     */
    public List<EsSpaceMember> listSharedMembers() {
        return memberMapper.selectList(Wrappers.<EsSpaceMember>lambdaQuery()
            .eq(EsSpaceMember::getSpaceId, EsSpace.SHARED_SPACE_ID));
    }

    /**
     * 取某账号对共享空间的权限，未授权返回 null。
     *
     * @param userId 账号 ID
     * @return read / write / null
     */
    public String sharedPermissionOf(Long userId) {
        EsSpaceMember member = memberMapper.selectOne(Wrappers.<EsSpaceMember>lambdaQuery()
            .eq(EsSpaceMember::getSpaceId, EsSpace.SHARED_SPACE_ID)
            .eq(EsSpaceMember::getUserId, userId));
        return member == null ? null : member.getPermission();
    }

    /**
     * 校验个人空间可否再写入 size 字节，通过则返回该空间的对象键前缀。
     * <p>
     * 这是配额唯一能强制的时机：签名一旦签出，字节直传 RustFS 不经控制面。
     *
     * @param userId 登录用户 ID
     * @param size   本次要写入的字节数
     * @return 空间前缀
     */
    public String checkPersonalWritable(Long userId, long size) {
        EsSpace space = findPersonal(userId);
        if (space == null || space.getQuotaBytes() == null
            || space.getQuotaBytes() == EsSpace.QUOTA_UNSET) {
            throw new ServiceException("尚未分配个人空间，请联系管理员开通");
        }
        if (!"0".equals(space.getStatus())) {
            throw new ServiceException("个人空间已停用");
        }
        String prefix = DriveKeys.userPrefix(userId);
        assertFits(prefix, space.getQuotaBytes(), size);
        return prefix;
    }

    /**
     * 校验共享空间可否再写入 size 字节，通过则返回共享前缀。
     *
     * @param userId 登录用户 ID
     * @param size   本次要写入的字节数
     * @return 共享前缀
     */
    public String checkSharedWritable(Long userId, long size) {
        EsSpace shared = findShared();
        if (shared.getQuotaBytes() == null || shared.getQuotaBytes() == EsSpace.QUOTA_UNSET) {
            throw new ServiceException("共享空间尚未分配容量");
        }
        if (!EsSpaceMember.PERM_WRITE.equals(sharedPermissionOf(userId))) {
            throw new ServiceException("没有共享空间的写入权限");
        }
        String prefix = DriveKeys.sharedPrefix();
        assertFits(prefix, shared.getQuotaBytes(), size);
        return prefix;
    }

    /**
     * 校验某账号可否读共享空间，通过则返回共享前缀。
     *
     * @param userId 登录用户 ID
     * @return 共享前缀
     */
    public String checkSharedReadable(Long userId) {
        if (sharedPermissionOf(userId) == null) {
            throw new ServiceException("没有共享空间的访问权限");
        }
        return DriveKeys.sharedPrefix();
    }

    /**
     * 配额判定：已用 + 本次 <= 上限。
     */
    private void assertFits(String prefix, long quota, long size) {
        if (size < 0) {
            throw new ServiceException("文件大小非法");
        }
        // 池上限先判：磁盘真满时，用户删自己的文件也没用，得让他知道该找管理员
        assertPoolFits(size);
        if (quota == EsSpace.QUOTA_UNLIMITED) {
            return;
        }
        long used = usageService.usedBytes(prefix);
        if (used + size > quota) {
            throw new ServiceException(String.format(
                "你的空间已满：已用 %s，配额 %s，本次需 %s。请清理文件或联系管理员扩容",
                readable(used), readable(quota), readable(size)));
        }
    }

    /**
     * 池上限判定：全部空间的实际用量之和不得超过物理可用容量（扣除预留水位）。
     * <p>
     * 与逐空间配额是两件事，错误信息必须分得清——这是本判定的主要价值：
     * 「你的空间满了」用户自己删文件即可，「服务器存储不足」删自己的文件没有用。
     * 两者混成一句会让用户白忙。
     */
    private void assertPoolFits(long size) {
        long pool = capacityService.poolBytes();
        if (pool < 0) {
            // 未配置容量探测，或探测失败：不判池上限，行为与引入前一致
            return;
        }
        long totalUsed = usageService.usedBytesGrouped().values().stream()
            .mapToLong(Long::longValue).sum();
        if (totalUsed + size > pool) {
            throw new ServiceException(String.format(
                "服务器存储不足：已用 %s，可用 %s，本次需 %s。请联系管理员",
                readable(totalUsed), readable(pool), readable(size)));
        }
    }

    private static void validateQuota(long quotaBytes) {
        if (quotaBytes < EsSpace.QUOTA_UNLIMITED) {
            throw new ServiceException("配额取值非法");
        }
    }

    private static String readable(long bytes) {
        if (bytes < 1024) {
            return bytes + " B";
        }
        String[] units = {"KB", "MB", "GB", "TB"};
        double value = bytes / 1024.0;
        int unit = 0;
        while (value >= 1024 && unit < units.length - 1) {
            value /= 1024;
            unit++;
        }
        return String.format("%.2f %s", value, units[unit]);
    }
}
