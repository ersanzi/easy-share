package org.dromara.easyshare.drive.service;

import com.baomidou.mybatisplus.core.toolkit.Wrappers;
import lombok.RequiredArgsConstructor;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.domain.EsPermissionRole;
import org.dromara.easyshare.drive.domain.EsUserPermission;
import org.dromara.easyshare.drive.domain.PermissionVo;
import org.dromara.easyshare.drive.mapper.EsUserPermissionMapper;
import org.dromara.easyshare.drive.mapper.EsUserRoleMapper;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.util.List;

/**
 * 成员功能权限的唯一裁决点。
 * <p>
 * 所有强制拦截（网盘请求、知识服务问答、会议听记）最终都走 {@link #enabled}：
 * 一处口径，才不会有「这个入口关了、那个入口还开着」的裂缝。
 * <p>
 * 裁决顺序（2026-10-27 起）：
 * <ol>
 *   <li>账号挂了权限角色 → 角色五项取并集（{@link PermissionVo#union}）；</li>
 *   <li>没有角色 → 沿用逐账号矩阵 {@code es_user_permission}；</li>
 *   <li>再没有行 → 五项全开，保证新账号开箱可用。</li>
 * </ol>
 * 逐账号行在用户有角色时不参与裁决，但**不删除**——移除角色后回到原有单独配置。
 *
 * @author EasyShare
 */
@Service
@RequiredArgsConstructor
public class FeaturePermissionService {

    public static final String KNOWLEDGE = "knowledge";
    public static final String MEETING = "meeting";
    public static final String DRIVE = "drive";
    public static final String SHARED = "shared";
    public static final String PLUGIN = "plugin";

    private final EsUserPermissionMapper mapper;
    private final EsUserRoleMapper userRoleMapper;

    /**
     * 判断账号是否启用某功能。
     *
     * @param userId  账号 ID
     * @param feature 功能键，见本类常量
     * @return 启用为 true；账号 ID 非法返回 false（宁拒绝勿放行）
     */
    public boolean enabled(Long userId, String feature) {
        PermissionVo snapshot = snapshot(userId);
        return switch (feature) {
            case KNOWLEDGE -> snapshot.knowledge();
            case MEETING -> snapshot.meeting();
            case DRIVE -> snapshot.drive();
            case SHARED -> snapshot.shared();
            case PLUGIN -> snapshot.plugin();
            default -> throw new ServiceException("未知功能项：" + feature);
        };
    }

    /**
     * 共享空间是否可用：drive 与 shared 同时开启才算。
     * <p>
     * 共享空间是网盘的一个空间，没有网盘就没有共享——管理员只需关 drive 一项，
     * 不需要记得再关 shared。一次取快照，避免两个开关查两遍。
     *
     * @param userId 账号 ID
     * @return 可用为 true
     */
    public boolean sharedFeatureEnabled(Long userId) {
        PermissionVo snapshot = snapshot(userId);
        return snapshot.drive() && snapshot.shared();
    }

    /**
     * 取某账号的权限快照。
     *
     * @param userId 账号 ID
     * @return 快照
     */
    public PermissionVo of(Long userId) {
        if (userId == null || userId <= 0) {
            throw new ServiceException("账号标识非法");
        }
        return snapshot(userId);
    }

    /**
     * 生效权限的纯函数：角色并集 → 逐账号行 → 全开。
     * <p>
     * 管理页批量组装成员列表时也调这里：规则只有一处实现，
     * 不会出现「矩阵上看到的」与「真正拦截的」不一致。
     *
     * @param userId 账号 ID
     * @param roles  已查出的角色（可为空）
     * @param row    已查出的逐账号行（可为 null）
     * @return 生效快照
     */
    public static PermissionVo effective(Long userId, List<EsPermissionRole> roles, EsUserPermission row) {
        if (roles != null && !roles.isEmpty()) {
            return PermissionVo.union(userId, roles);
        }
        return row == null ? PermissionVo.all(userId) : PermissionVo.of(row);
    }

    /**
     * 全部显式配置行，供管理页一次拉齐；没有行的账号由客户端按默认全开渲染。
     *
     * @return 权限快照列表
     */
    public List<PermissionVo> listExplicit() {
        return mapper.selectList(Wrappers.<EsUserPermission>lambdaQuery()).stream()
            .map(PermissionVo::of)
            .toList();
    }

    /**
     * 保存某账号的逐账号权限矩阵（有行改、无行建）。
     * <p>
     * 账号有角色时拒绝：此时逐账号行不参与裁决，静默写入只会让管理员以为改成功了。
     *
     * @param userId    账号 ID
     * @param knowledge 知识问答
     * @param meeting   会议听记
     * @param drive     网盘
     * @param shared    共享空间
     * @param plugin    插件中心
     */
    @Transactional(rollbackFor = Exception.class)
    public void save(Long userId, boolean knowledge, boolean meeting, boolean drive,
                     boolean shared, boolean plugin) {
        if (userId == null || userId <= 0) {
            throw new ServiceException("账号标识非法");
        }
        if (!userRoleMapper.selectRoleIdsOfUser(userId).isEmpty()) {
            throw new ServiceException("该成员的功能权限由角色决定，请调整角色分配或先移除角色");
        }
        EsUserPermission existing = mapper.selectById(userId);
        if (existing == null) {
            EsUserPermission row = new EsUserPermission();
            row.setUserId(userId);
            apply(row, knowledge, meeting, drive, shared, plugin);
            mapper.insert(row);
            return;
        }
        apply(existing, knowledge, meeting, drive, shared, plugin);
        mapper.updateById(existing);
    }

    /** 取权限快照；非法 ID 视为五项全关（只可能来自内部调用，正常登录态到不了这里） */
    private PermissionVo snapshot(Long userId) {
        if (userId == null || userId <= 0) {
            return new PermissionVo(0L, false, false, false, false, false);
        }
        List<EsPermissionRole> roles = userRoleMapper.selectRolesOfUser(userId);
        // 有角色时逐账号行不参与裁决，省掉这次查询
        EsUserPermission row = roles.isEmpty() ? mapper.selectById(userId) : null;
        return effective(userId, roles, row);
    }

    private static void apply(EsUserPermission row, boolean knowledge, boolean meeting,
                              boolean drive, boolean shared, boolean plugin) {
        row.setKnowledge(knowledge);
        row.setMeeting(meeting);
        row.setDrive(drive);
        row.setShared(shared);
        row.setPlugin(plugin);
    }
}
