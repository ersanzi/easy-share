package org.dromara.easyshare.drive.service;

import com.baomidou.mybatisplus.core.toolkit.Wrappers;
import lombok.RequiredArgsConstructor;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.domain.AdminMemberVo;
import org.dromara.easyshare.drive.domain.EsPermissionRole;
import org.dromara.easyshare.drive.domain.EsUserPermission;
import org.dromara.easyshare.drive.domain.PermissionVo;
import org.dromara.easyshare.drive.domain.RoleVo;
import org.dromara.easyshare.drive.domain.SysDeptRow;
import org.dromara.easyshare.drive.domain.SysUserDept;
import org.dromara.easyshare.drive.domain.UserRoleBinding;
import org.dromara.easyshare.drive.mapper.EsPermissionRoleMapper;
import org.dromara.easyshare.drive.mapper.EsUserPermissionMapper;
import org.dromara.easyshare.drive.mapper.EsUserRoleMapper;
import org.dromara.easyshare.drive.mapper.SysDeptRowMapper;
import org.dromara.easyshare.drive.mapper.SysUserDeptMapper;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Deque;
import java.util.HashMap;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.function.Function;
import java.util.stream.Collectors;

/**
 * 权限角色（权限组）与子管理员范围。
 * <p>
 * 角色解决「一批人用同一套功能开关」，子管理员解决「谁能进管理页、管到哪一层」。
 * 管理范围是角色上的档位表达式：本部门（{@code dept}）、本部门及下级
 * （{@code dept_children}，按 {@code sys_dept.parent_id} 内存 BFS）、指定部门
 * （{@code specified}，读 {@code scope_dept_ids}）、全部成员（{@code all}）。
 * 多个管理角色的范围取并集；部门数量级（几百以内）不值得引入递归 SQL，内存建树即可。
 *
 * @author EasyShare
 */
@Service
@RequiredArgsConstructor
public class PermissionRoleService {

    /** 子管理员：管全部成员 */
    public static final String SCOPE_ALL = EsPermissionRole.SCOPE_ALL;

    /** 子管理员：管本部门及下级部门成员 */
    public static final String SCOPE_DEPT_CHILDREN = EsPermissionRole.SCOPE_DEPT_CHILDREN;

    /** 子管理员：管指定部门成员（范围在角色的 scopeDeptIds 上） */
    public static final String SCOPE_SPECIFIED = EsPermissionRole.SCOPE_SPECIFIED;

    /** 子管理员：只管本部门成员 */
    public static final String SCOPE_DEPT = "dept";

    /** 普通成员：无管理入口 */
    public static final String SCOPE_NONE = "none";

    private final EsPermissionRoleMapper roleMapper;
    private final EsUserRoleMapper userRoleMapper;
    private final EsUserPermissionMapper permissionMapper;
    private final SysUserDeptMapper sysUserDeptMapper;
    private final SysDeptRowMapper sysDeptRowMapper;

    /**
     * 全部角色（管理页角色列表）。
     *
     * @return 角色及成员数
     */
    public List<RoleVo> list() {
        Map<Long, Long> counts = userRoleMapper.selectAll().stream()
            .collect(Collectors.groupingBy(UserRoleBinding::getRoleId, Collectors.counting()));
        return roleMapper.selectList(Wrappers.<EsPermissionRole>lambdaQuery()
                .orderByAsc(EsPermissionRole::getRoleId))
            .stream()
            .map(role -> RoleVo.of(role, counts.getOrDefault(role.getRoleId(), 0L)))
            .toList();
    }

    /**
     * 新建或更新角色（{@code roleId} 为 null 即新建）。
     *
     * @param roleId       角色 ID，可空
     * @param roleName     角色名（全局唯一）
     * @param adminScope   none / dept / dept_children / specified / all
     * @param scopeDeptIds 指定部门 ID 列表，仅 adminScope=specified 时必填非空，其余档位忽略
     * @param knowledge    知识问答
     * @param meeting      会议听记
     * @param drive        网盘
     * @param shared       共享空间
     * @param plugin       插件中心
     * @return 保存后的角色
     */
    @Transactional(rollbackFor = Exception.class)
    public RoleVo save(Long roleId, String roleName, String adminScope, List<Long> scopeDeptIds,
                       boolean knowledge, boolean meeting, boolean drive, boolean shared, boolean plugin) {
        String name = roleName == null ? "" : roleName.trim();
        if (name.isEmpty()) {
            throw new ServiceException("角色名不能为空");
        }
        if (name.length() > 64) {
            throw new ServiceException("角色名最长 64 个字符");
        }
        String scope = normalizeScope(adminScope);
        String deptIdsCsv = serializeScopeDeptIds(scope, scopeDeptIds);
        EsPermissionRole duplicate = roleMapper.selectOne(Wrappers.<EsPermissionRole>lambdaQuery()
            .eq(EsPermissionRole::getRoleName, name).last("LIMIT 1"));
        if (duplicate != null && !duplicate.getRoleId().equals(roleId)) {
            throw new ServiceException("角色名「" + name + "」已存在");
        }

        EsPermissionRole role;
        if (roleId == null) {
            role = new EsPermissionRole();
        } else {
            role = roleMapper.selectById(roleId);
            if (role == null) {
                throw new ServiceException("角色不存在或已删除");
            }
        }
        role.setRoleName(name);
        role.setAdminScope(scope);
        role.setScopeDeptIds(deptIdsCsv);
        role.setKnowledge(knowledge);
        role.setMeeting(meeting);
        role.setDrive(drive);
        role.setShared(shared);
        role.setPlugin(plugin);
        if (roleId == null) {
            roleMapper.insert(role);
        } else {
            roleMapper.updateById(role);
        }
        long memberCount = userRoleMapper.selectUserIdsOfRole(role.getRoleId()).size();
        return RoleVo.of(role, memberCount);
    }

    /**
     * 删除角色。
     * <p>
     * 只挂这一个角色的成员会先落一行 {@code es_user_permission} 快照：
     * 否则删除后成员变成「无角色且缺行」，权限被放大成五项全开——
     * 管理员的直觉是「删角色=收回这批人的统一配置」，不该顺手把限制也删了。
     *
     * @param roleId 角色 ID
     */
    @Transactional(rollbackFor = Exception.class)
    public void delete(Long roleId) {
        EsPermissionRole role = roleId == null ? null : roleMapper.selectById(roleId);
        if (role == null) {
            return;
        }
        List<Long> userIds = userRoleMapper.selectUserIdsOfRole(roleId);
        userRoleMapper.deleteByRole(roleId);
        for (Long userId : userIds) {
            if (!userRoleMapper.selectRoleIdsOfUser(userId).isEmpty()) {
                continue;
            }
            EsUserPermission row = permissionMapper.selectById(userId);
            boolean insert = row == null;
            if (insert) {
                row = new EsUserPermission();
                row.setUserId(userId);
            }
            row.setKnowledge(!Boolean.FALSE.equals(role.getKnowledge()));
            row.setMeeting(!Boolean.FALSE.equals(role.getMeeting()));
            row.setDrive(!Boolean.FALSE.equals(role.getDrive()));
            row.setShared(!Boolean.FALSE.equals(role.getShared()));
            row.setPlugin(!Boolean.FALSE.equals(role.getPlugin()));
            if (insert) {
                permissionMapper.insert(row);
            } else {
                permissionMapper.updateById(row);
            }
        }
        roleMapper.deleteById(roleId);
    }

    /**
     * 整组替换某成员的角色（空列表 = 回到逐账号配置）。
     *
     * @param userId  账号 ID
     * @param roleIds 角色 ID 列表，可空
     */
    @Transactional(rollbackFor = Exception.class)
    public void assignRoles(Long userId, List<Long> roleIds) {
        SysUserDept user = userId == null ? null : sysUserDeptMapper.selectById(userId);
        if (user == null || "2".equals(user.getDelFlag())) {
            throw new ServiceException("账号不存在或已删除");
        }
        Set<Long> ids = new HashSet<>();
        if (roleIds != null) {
            roleIds.stream().filter(id -> id != null && id > 0).forEach(ids::add);
        }
        if (!ids.isEmpty() && roleMapper.selectBatchIds(ids).size() != ids.size()) {
            throw new ServiceException("存在已被删除的角色，请刷新后重试");
        }
        userRoleMapper.deleteByUser(userId);
        for (Long roleId : ids) {
            userRoleMapper.insertRow(userId, roleId);
        }
    }

    /**
     * 某账号的角色。
     *
     * @param userId 账号 ID
     * @return 角色列表
     */
    public List<EsPermissionRole> rolesOf(Long userId) {
        if (userId == null || userId <= 0) {
            return List.of();
        }
        return userRoleMapper.selectRolesOfUser(userId);
    }

    /**
     * 账号的管理范围档位（{@code /mine} 与管理页徽标用）：超管 all；
     * 其余取所有生效管理角色里档位最高的一个；全部角色都不生效则 none。
     * <p>
     * 注意这只是**展示用档位**——真实的成员裁剪一律走 {@link #rangeOf}（多角色并集），
     * 两者不保证一致（比如同时挂 dept 与 specified 时这里显示 dept_children，
     * 实际可见范围是并集）。
     *
     * @param userId 账号 ID
     * @return all / dept_children / dept / specified / none
     */
    public String adminScopeOf(Long userId) {
        if (userId == null || userId <= 0) {
            return SCOPE_NONE;
        }
        if (LoginHelper.isSuperAdmin(userId)) {
            return SCOPE_ALL;
        }
        Long ownDept = null;
        String best = SCOPE_NONE;
        for (EsPermissionRole role : userRoleMapper.selectRolesOfUser(userId)) {
            String scope = role.getAdminScope();
            if (scope == null || SCOPE_NONE.equals(scope)) {
                continue;
            }
            boolean valid = switch (scope) {
                // 没挂部门的账号无法界定「本部门」：宁可不给管理入口，也不能变成「所有人可见」
                case SCOPE_DEPT, SCOPE_DEPT_CHILDREN -> {
                    if (ownDept == null) {
                        ownDept = deptIdOf(userId);
                    }
                    yield ownDept != null && ownDept > 0;
                }
                case SCOPE_SPECIFIED -> parseScopeDeptIds(role.getScopeDeptIds()).isEmpty() == false;
                case SCOPE_ALL -> true;
                default -> false;
            };
            if (valid && scopeRank(scope) > scopeRank(best)) {
                best = scope;
            }
        }
        return best;
    }

    /**
     * 操作者能否管理目标成员。
     *
     * @param actorId  操作者账号 ID
     * @param targetId 目标账号 ID
     * @return 可管理为 true
     */
    public boolean canManage(Long actorId, Long targetId) {
        if (targetId == null || targetId <= 0) {
            return false;
        }
        MemberRange range = rangeOf(actorId);
        if (range == null) {
            return false;
        }
        if (range.all()) {
            return true;
        }
        Long targetDept = deptIdOf(targetId);
        return targetDept != null && range.deptIds().contains(targetDept);
    }

    /**
     * 管理页成员矩阵：按操作者范围裁剪（子管理员只看到范围内部门的成员）。
     *
     * @param actorId 操作者账号 ID
     * @return 成员行
     */
    public List<AdminMemberVo> membersOf(Long actorId) {
        MemberRange range = rangeOf(actorId);
        if (range == null) {
            throw new ServiceException("当前账号没有管理权限");
        }
        List<SysUserDept> users = sysUserDeptMapper.selectList(Wrappers.<SysUserDept>lambdaQuery()
            .eq(SysUserDept::getDelFlag, "0")
            .orderByAsc(SysUserDept::getDeptId)
            .orderByAsc(SysUserDept::getUserId));
        if (!range.all()) {
            users = users.stream()
                .filter(user -> user.getDeptId() != null && range.deptIds().contains(user.getDeptId()))
                .toList();
        }

        Map<Long, EsPermissionRole> rolesById = roleMapper.selectList(
                Wrappers.<EsPermissionRole>lambdaQuery())
            .stream()
            .collect(Collectors.toMap(EsPermissionRole::getRoleId, Function.identity()));
        Map<Long, List<Long>> roleIdsByUser = new LinkedHashMap<>();
        for (UserRoleBinding binding : userRoleMapper.selectAll()) {
            roleIdsByUser.computeIfAbsent(binding.getUserId(), key -> new ArrayList<>())
                .add(binding.getRoleId());
        }
        Map<Long, EsUserPermission> rows = permissionMapper.selectList(
                Wrappers.<EsUserPermission>lambdaQuery())
            .stream()
            .collect(Collectors.toMap(EsUserPermission::getUserId, Function.identity()));
        Map<Long, String> deptNames = sysDeptRowMapper.selectList(
                Wrappers.<SysDeptRow>lambdaQuery())
            .stream()
            .collect(Collectors.toMap(SysDeptRow::getDeptId, SysDeptRow::getDeptName, (a, b) -> a));

        List<AdminMemberVo> members = new ArrayList<>(users.size());
        for (SysUserDept user : users) {
            List<Long> roleIds = roleIdsByUser.getOrDefault(user.getUserId(), List.of());
            List<EsPermissionRole> roles = roleIds.stream()
                .map(rolesById::get)
                .filter(java.util.Objects::nonNull)
                .toList();
            PermissionVo flags = FeaturePermissionService.effective(
                user.getUserId(), roles, rows.get(user.getUserId()));
            members.add(new AdminMemberVo(
                user.getUserId(),
                user.getUserName(),
                user.getNickName(),
                user.getDeptId(),
                deptNames.getOrDefault(user.getDeptId(), ""),
                user.getStatus(),
                flags.knowledge(),
                flags.meeting(),
                flags.drive(),
                flags.shared(),
                flags.plugin(),
                List.copyOf(roleIds),
                roles.stream().map(EsPermissionRole::getRoleName).toList(),
                scopeOfMember(user.getUserId(), roles, user.getDeptId())));
        }
        return members;
    }

    /** 单个成员的管理范围（管理页显示「子管理员」徽标用，展示值；真实裁剪见 {@link #rangeOf}） */
    private static String scopeOfMember(Long userId, List<EsPermissionRole> roles, Long deptId) {
        if (LoginHelper.isSuperAdmin(userId)) {
            return SCOPE_ALL;
        }
        String best = SCOPE_NONE;
        for (EsPermissionRole role : roles) {
            String scope = role.getAdminScope();
            if (scope == null || SCOPE_NONE.equals(scope)) {
                continue;
            }
            boolean valid = switch (scope) {
                case SCOPE_DEPT, SCOPE_DEPT_CHILDREN -> deptId != null && deptId > 0;
                case SCOPE_SPECIFIED -> role.getScopeDeptIds() != null && !role.getScopeDeptIds().isBlank();
                case SCOPE_ALL -> true;
                default -> false;
            };
            if (valid && scopeRank(scope) > scopeRank(best)) {
                best = scope;
            }
        }
        return best;
    }

    /** 管理范围解析结果：{@code all=true} 不限部门；否则按部门集合裁剪成员 */
    private record MemberRange(boolean all, Set<Long> deptIds) {

        static final MemberRange ALL = new MemberRange(true, Set.of());
    }

    /**
     * 操作者的**真实管理范围**（多管理角色取并集）：null = 无管理权限。
     * {@code /mine} 的入口显隐、{@code canManage} 的写校验、{@code membersOf} 的矩阵裁剪
     * 三处共用这一个解析，保证「列表显示的值」和「实际拦截的值」不分叉。
     */
    private MemberRange rangeOf(Long actorId) {
        if (actorId == null || actorId <= 0) {
            return null;
        }
        if (LoginHelper.isSuperAdmin(actorId)) {
            return MemberRange.ALL;
        }
        List<EsPermissionRole> roles = userRoleMapper.selectRolesOfUser(actorId);
        if (roles.isEmpty()) {
            return null;
        }
        Set<Long> deptIds = new HashSet<>();
        boolean all = false;
        boolean anyValid = false;
        for (EsPermissionRole role : roles) {
            String scope = role.getAdminScope();
            if (scope == null || SCOPE_NONE.equals(scope)) {
                continue;
            }
            switch (scope) {
                case SCOPE_ALL -> all = true;
                case SCOPE_SPECIFIED -> {
                    Set<Long> ids = parseScopeDeptIds(role.getScopeDeptIds());
                    if (!ids.isEmpty()) {
                        deptIds.addAll(ids);
                        anyValid = true;
                    }
                }
                case SCOPE_DEPT, SCOPE_DEPT_CHILDREN -> {
                    Long ownDept = deptIdOf(actorId);
                    if (ownDept == null || ownDept <= 0) {
                        // 范围无法界定，该角色不生效；继续看其余角色
                        continue;
                    }
                    deptIds.add(ownDept);
                    if (SCOPE_DEPT_CHILDREN.equals(scope)) {
                        deptIds.addAll(descendantDeptIds(ownDept));
                    }
                    anyValid = true;
                }
                // 未知档位当普通权限组处理，不授予管理权
                default -> {
                }
            }
            if (all) {
                break;
            }
        }
        if (all) {
            return MemberRange.ALL;
        }
        return anyValid ? new MemberRange(false, deptIds) : null;
    }

    /** 本部门的全部下级部门（不含本部门）：按 parent_id 内存 BFS，部门数量级在几百以内 */
    private Set<Long> descendantDeptIds(Long rootDeptId) {
        List<SysDeptRow> depts = sysDeptRowMapper.selectList(Wrappers.<SysDeptRow>lambdaQuery()
            .ne(SysDeptRow::getDelFlag, "2"));
        Map<Long, List<Long>> childrenOf = new HashMap<>();
        for (SysDeptRow dept : depts) {
            if (dept.getParentId() != null) {
                childrenOf.computeIfAbsent(dept.getParentId(), key -> new ArrayList<>()).add(dept.getDeptId());
            }
        }
        Set<Long> visited = new HashSet<>();
        Deque<Long> queue = new ArrayDeque<>();
        queue.add(rootDeptId);
        while (!queue.isEmpty()) {
            Long current = queue.poll();
            for (Long child : childrenOf.getOrDefault(current, List.of())) {
                // add 返回 false 说明已在集合里，天然防环、防重复入队
                if (visited.add(child)) {
                    queue.add(child);
                }
            }
        }
        return visited;
    }

    /** 逗号分隔的部门 ID 解析成集合；空串/非法片段忽略（范围解析要宽容，宁少勿错） */
    private static Set<Long> parseScopeDeptIds(String csv) {
        if (csv == null || csv.isBlank()) {
            return Set.of();
        }
        Set<Long> ids = new HashSet<>();
        for (String part : csv.split(",")) {
            String trimmed = part.trim();
            if (trimmed.matches("\\d+")) {
                ids.add(Long.valueOf(trimmed));
            }
        }
        return ids;
    }

    /**
     * 按档位归一化部门列表：仅 specified 允许携带（去重、过滤非法值、必须非空），
     * 其余档位一律清空——范围表达式只有一处真相。
     */
    private static String serializeScopeDeptIds(String scope, List<Long> scopeDeptIds) {
        if (!SCOPE_SPECIFIED.equals(scope)) {
            return null;
        }
        Set<Long> ids = new LinkedHashSet<>();
        if (scopeDeptIds != null) {
            for (Long id : scopeDeptIds) {
                if (id != null && id > 0) {
                    ids.add(id);
                }
            }
        }
        if (ids.isEmpty()) {
            throw new ServiceException("指定部门范围至少要勾选一个部门");
        }
        return ids.stream().map(String::valueOf)
            .collect(Collectors.joining(","));
    }

    /** 展示用档位排序：all > dept_children > dept > specified（仅用于挑「最高」档显示） */
    private static int scopeRank(String scope) {
        return switch (scope) {
            case SCOPE_ALL -> 4;
            case SCOPE_DEPT_CHILDREN -> 3;
            case SCOPE_DEPT -> 2;
            case SCOPE_SPECIFIED -> 1;
            default -> 0;
        };
    }

    private Long deptIdOf(Long userId) {
        if (userId == null || userId <= 0) {
            return null;
        }
        SysUserDept user = sysUserDeptMapper.selectById(userId);
        return user == null ? null : user.getDeptId();
    }

    private static String normalizeScope(String adminScope) {
        if (adminScope == null || adminScope.isBlank() || EsPermissionRole.SCOPE_NONE.equals(adminScope)) {
            return EsPermissionRole.SCOPE_NONE;
        }
        if (EsPermissionRole.SCOPE_DEPT.equals(adminScope)
            || SCOPE_DEPT_CHILDREN.equals(adminScope)
            || SCOPE_SPECIFIED.equals(adminScope)
            || SCOPE_ALL.equals(adminScope)) {
            return adminScope;
        }
        throw new ServiceException("未知的管理范围：" + adminScope);
    }
}
