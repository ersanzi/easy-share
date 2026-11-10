package org.dromara.easyshare.drive;

import cn.dev33.satoken.annotation.SaCheckLogin;
import cn.dev33.satoken.annotation.SaCheckRole;
import jakarta.validation.constraints.NotNull;
import lombok.RequiredArgsConstructor;
import org.dromara.common.core.domain.R;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.domain.AdminMemberVo;
import org.dromara.easyshare.drive.domain.EsPermissionRole;
import org.dromara.easyshare.drive.domain.MyPermissionVo;
import org.dromara.easyshare.drive.domain.PermissionVo;
import org.dromara.easyshare.drive.domain.RoleVo;
import org.dromara.easyshare.drive.service.FeaturePermissionService;
import org.dromara.easyshare.drive.service.PermissionRoleService;
import org.springframework.validation.annotation.Validated;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PutMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

import java.util.List;

/**
 * 成员功能权限的读写入口。
 * <p>
 * 读分两个粒度：{@code /mine} 任何登录用户都能读自己那份（客户端据此隐藏入口）；
 * {@code /admin/members} 供管理页画矩阵——超管看全部成员，子管理员按角色上的
 * 范围档位裁剪（本部门 / 含下级 / 指定部门 / 全部）。
 * <p>
 * 写：{@code PUT /admin}（逐账号矩阵）按操作者范围校验；角色本身跨部门，
 * 只允许超管增删改与分配。
 * <p>
 * 这些接口本身不受权限矩阵管辖——否则管理员关掉自己的某项后就再也进不来改回去。
 *
 * @author EasyShare
 */
@Validated
@SaCheckLogin
@RestController
@RequiredArgsConstructor
@RequestMapping("/easyshare/permission")
public class UserPermissionController {

    private final FeaturePermissionService permissionService;
    private final PermissionRoleService roleService;

    /**
     * 当前登录账号的功能权限与管理范围。
     *
     * @return 生效权限 + adminScope + 角色名
     */
    @GetMapping("/mine")
    public R<MyPermissionVo> mine() {
        Long userId = LoginHelper.getUserId();
        List<String> roles = roleService.rolesOf(userId).stream()
            .map(EsPermissionRole::getRoleName)
            .toList();
        return R.ok(MyPermissionVo.of(userId, permissionService.of(userId),
            roleService.adminScopeOf(userId), roles));
    }

    /**
     * 全部显式配置行（旧版管理页接口，仅超管）。
     * <p>
     * 保留给尚未升级的桌面端；新版本改读 {@code /admin/members}，
     * 那份数据带角色与生效值，且支持子管理员范围裁剪。
     *
     * @return 权限快照列表
     */
    @SaCheckRole("superadmin")
    @GetMapping("/admin/list")
    public R<List<PermissionVo>> adminList() {
        return R.ok(permissionService.listExplicit());
    }

    /**
     * 管理页成员矩阵：身份 + 生效权限 + 所属角色，按操作者范围裁剪。
     *
     * @return 成员行
     */
    @GetMapping("/admin/members")
    public R<List<AdminMemberVo>> members() {
        return R.ok(roleService.membersOf(LoginHelper.getUserId()));
    }

    /**
     * 全部权限角色（仅超管）。
     *
     * @return 角色及成员数
     */
    @SaCheckRole("superadmin")
    @GetMapping("/admin/roles")
    public R<List<RoleVo>> roles() {
        return R.ok(roleService.list());
    }

    /**
     * 新建或更新权限角色（仅超管）。
     *
     * @param body 角色入参
     * @return 保存后的角色
     */
    @SaCheckRole("superadmin")
    @PutMapping("/admin/roles")
    public R<RoleVo> saveRole(@Validated @RequestBody RoleBo body) {
        return R.ok(roleService.save(parseRoleId(body.roleId()), body.roleName(), body.adminScope(),
            body.scopeDeptIds(), body.knowledge(), body.meeting(), body.drive(),
            body.shared(), body.plugin()));
    }

    /**
     * 删除权限角色（仅超管）。只挂该角色的成员会落一行逐账号快照，不会因删除而放宽成「全开」。
     *
     * @param roleId 角色 ID
     * @return 空响应
     */
    @SaCheckRole("superadmin")
    @DeleteMapping("/admin/roles/{roleId}")
    public R<Void> deleteRole(@PathVariable Long roleId) {
        roleService.delete(roleId);
        return R.ok();
    }

    /**
     * 整组替换某成员的角色（仅超管）；空列表 = 回到逐账号配置。
     *
     * @param userId 账号 ID
     * @param body   角色 ID 列表
     * @return 空响应
     */
    @SaCheckRole("superadmin")
    @PutMapping("/admin/members/{userId}/roles")
    public R<Void> assignRoles(@PathVariable Long userId, @RequestBody AssignBo body) {
        roleService.assignRoles(userId, body.roleIds());
        return R.ok();
    }

    /**
     * 保存某账号的逐账号权限矩阵。
     * <p>
     * 超管可改任何人；子管理员只能改本部门成员，且目标挂有角色时由服务层拒绝
     * （角色优先，写逐账号行不会生效，静默成功会误导管理员）。
     *
     * @param body 账号与五项开关
     * @return 空响应
     */
    @PutMapping("/admin")
    public R<Void> save(@Validated @RequestBody SaveBo body) {
        if (!roleService.canManage(LoginHelper.getUserId(), body.userId())) {
            throw new ServiceException("只能修改本部门成员的功能权限");
        }
        permissionService.save(body.userId(), body.knowledge(), body.meeting(),
            body.drive(), body.shared(), body.plugin());
        return R.ok();
    }

    private static Long parseRoleId(String roleId) {
        if (roleId == null || roleId.isBlank()) {
            return null;
        }
        try {
            return Long.valueOf(roleId.trim());
        } catch (NumberFormatException e) {
            throw new ServiceException("角色标识非法");
        }
    }

    /**
     * 逐账号矩阵保存请求。布尔项用基本类型：客户端漏传按关闭处理，
     * 宁可少给能力也不误开。
     *
     * @param userId    账号 ID
     * @param knowledge 知识问答
     * @param meeting   会议听记
     * @param drive     网盘
     * @param shared    共享空间
     * @param plugin    插件中心
     */
    public record SaveBo(
        @NotNull Long userId,
        boolean knowledge,
        boolean meeting,
        boolean drive,
        boolean shared,
        boolean plugin
    ) {
    }

    /**
     * 角色保存请求。roleId 用字符串：新建时客户端传空串，直接映射 Long 会解析失败。
     *
     * @param roleId       角色 ID，空=新建
     * @param roleName     角色名
     * @param adminScope   none / dept / dept_children / specified / all
     * @param scopeDeptIds 指定部门 ID 列表，仅 adminScope=specified 时必填非空
     * @param knowledge    知识问答
     * @param meeting      会议听记
     * @param drive        网盘
     * @param shared       共享空间
     * @param plugin       插件中心
     */
    public record RoleBo(
        String roleId,
        String roleName,
        String adminScope,
        List<Long> scopeDeptIds,
        boolean knowledge,
        boolean meeting,
        boolean drive,
        boolean shared,
        boolean plugin
    ) {
    }

    /**
     * 成员角色分配请求。
     *
     * @param roleIds 角色 ID 列表；空列表=移除全部角色
     */
    public record AssignBo(List<Long> roleIds) {
    }
}
