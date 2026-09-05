package org.dromara.easyshare.drive;

import cn.dev33.satoken.annotation.SaCheckLogin;
import cn.dev33.satoken.annotation.SaCheckRole;
import jakarta.validation.constraints.NotBlank;
import jakarta.validation.constraints.NotNull;
import lombok.RequiredArgsConstructor;
import org.dromara.common.core.domain.R;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.domain.CapacityVo;
import org.dromara.easyshare.drive.domain.EsSpace;
import org.dromara.easyshare.drive.domain.EsSpaceMember;
import org.dromara.easyshare.drive.domain.SpaceVo;
import org.dromara.easyshare.drive.service.CapacityService;
import org.dromara.easyshare.drive.service.SpaceService;
import org.dromara.easyshare.drive.service.SpaceUsageService;
import org.springframework.validation.annotation.Validated;
import org.springframework.web.bind.annotation.*;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

/**
 * 空间管理接口。
 * <p>
 * 分两段：
 * <ul>
 *   <li>{@code /mine} —— 任何登录账号可读自己的空间，客户端用来显示配额与「待开空间」；</li>
 *   <li>{@code /admin/**} —— 只有超级管理员能设定容量与授权，由 Sa-Token 角色注解拒绝，
 *       不依赖客户端的入口显隐。</li>
 * </ul>
 *
 * @author EasyShare
 */
@Validated
@SaCheckLogin
@RestController
@RequiredArgsConstructor
@RequestMapping("/easyshare/space")
public class SpaceController {

    private final SpaceService spaceService;
    private final SpaceUsageService usageService;
    private final CapacityService capacityService;
    private final DriveProperties properties;
    private final org.dromara.easyshare.drive.mapper.SysUserDeptMapper userDeptMapper;
    private final org.dromara.easyshare.drive.mapper.SysDeptRowMapper deptRowMapper;

    /**
     * 当前登录账号可见的空间：个人空间 + 有权限时的共享空间。
     *
     * @return 空间列表
     */
    @GetMapping("/mine")
    public R<List<SpaceVo>> mine() {
        Long userId = LoginHelper.getUserId();
        List<SpaceVo> result = new ArrayList<>();

        EsSpace personal = spaceService.findPersonal(userId);
        if (personal == null) {
            result.add(SpaceVo.unset(userId));
        } else {
            result.add(SpaceVo.of(personal,
                usageService.usedBytes(DriveKeys.userPrefix(userId)), "owner"));
        }

        // 共享空间只在被授权后出现在列表里；未授权的账号根本看不到这一项。
        String permission = spaceService.sharedPermissionOf(userId);
        if (permission != null) {
            result.add(SpaceVo.of(spaceService.findShared(),
                usageService.usedBytes(DriveKeys.sharedPrefix()), permission));
        }
        return R.ok(result);
    }

    /**
     * 全部空间与用量，供管理页一屏显示。用量走单次全桶分组，不逐账号 list。
     *
     * @return 空间列表（共享在前）
     */
    @SaCheckRole("superadmin")
    @GetMapping("/admin/list")
    public R<List<SpaceVo>> adminList() {
        Map<String, Long> usage = usageService.usedBytesGrouped();
        List<SpaceVo> result = new ArrayList<>();
        EsSpace shared = spaceService.findShared();
        result.add(SpaceVo.of(shared,
            usage.getOrDefault(DriveKeys.sharedPrefix(), 0L), EsSpaceMember.PERM_WRITE));
        spaceService.listPersonal().forEach(space -> result.add(SpaceVo.of(space,
            usage.getOrDefault(DriveKeys.userPrefix(space.getOwnerId()), 0L), "owner")));
        return R.ok(result);
    }

    /**
     * 容量总览：物理可用、已承诺、实际已用。管理员据此判断超配情况。
     *
     * @return 容量总览
     */
    @SaCheckRole("superadmin")
    @GetMapping("/admin/capacity")
    public R<CapacityVo> capacity() {
        long committed = 0L;
        int unlimited = 0;
        for (EsSpace space : spaceService.listAll()) {
            Long quota = space.getQuotaBytes();
            if (quota == null || quota == EsSpace.QUOTA_UNSET) {
                continue;
            }
            if (quota == EsSpace.QUOTA_UNLIMITED) {
                // 「不限」无法计入承诺总量，单独计数让管理员知道这个数字不完整
                unlimited++;
                continue;
            }
            committed += quota;
        }
        long used = usageService.usedBytesGrouped().values().stream()
            .mapToLong(Long::longValue).sum();
        return R.ok(new CapacityVo(
            capacityService.enabled(),
            capacityService.usableBytes(),
            capacityService.poolBytes(),
            properties.getReservedBytes().toBytes(),
            committed,
            used,
            unlimited));
    }

    /**
     * 共享空间的成员授权，供管理页显示（部门级权限片 1：主体含账号与部门两类）。
     * name 为显示名（账号=昵称/登录名，部门=部门名），解析失败留空由前端兜底。
     *
     * @return 授权主体列表
     */
    @SaCheckRole("superadmin")
    @GetMapping("/admin/shared-members")
    public R<List<MemberVo>> sharedMembers() {
        List<MemberVo> result = new ArrayList<>();
        for (org.dromara.easyshare.drive.domain.EsSpaceMember member : spaceService.listSharedMembers()) {
            String name = "";
            if (org.dromara.easyshare.drive.domain.EsSpaceMember.TYPE_DEPT.equals(member.getMemberType())) {
                var dept = deptRowMapper.selectById(member.getUserId());
                name = dept == null ? "" : dept.getDeptName();
            } else {
                var user = userDeptMapper.selectById(member.getUserId());
                if (user != null) {
                    name = user.getNickName() == null || user.getNickName().isBlank()
                        ? user.getUserName() : user.getNickName();
                }
            }
            result.add(new MemberVo(member.getMemberType(), String.valueOf(member.getUserId()),
                member.getPermission(), name));
        }
        return R.ok(result);
    }

    /**
     * 部门下拉数据源（启用中未删除）。
     */
    @SaCheckRole("superadmin")
    @GetMapping("/admin/depts")
    public R<List<DeptVo>> depts() {
        List<DeptVo> result = new ArrayList<>();
        spaceService.listDepts().forEach(dept ->
            result.add(new DeptVo(String.valueOf(dept.getDeptId()), dept.getDeptName())));
        return R.ok(result);
    }

    /**
     * 设定某账号的个人空间容量。没有空间行则开通，有则改配额。
     *
     * @param body 账号 ID 与配额
     * @return 操作结果
     */
    @SaCheckRole("superadmin")
    @PostMapping("/admin/personal-quota")
    public R<Void> setPersonalQuota(@Validated @RequestBody QuotaBo body) {
        Long owner = parseId(body.userId());
        spaceService.setPersonalQuota(owner, body.quotaBytes());
        // 配额变了，用量缓存里的判定基线也该重算
        usageService.invalidate(DriveKeys.userPrefix(owner));
        return R.ok();
    }

    /**
     * 设定共享空间容量。
     *
     * @param body 配额
     * @return 操作结果
     */
    @SaCheckRole("superadmin")
    @PostMapping("/admin/shared-quota")
    public R<Void> setSharedQuota(@Validated @RequestBody SharedQuotaBo body) {
        spaceService.setSharedQuota(body.quotaBytes());
        usageService.invalidate(DriveKeys.sharedPrefix());
        return R.ok();
    }

    /**
     * 授予或撤销某主体（账号/部门）对共享空间的权限。
     *
     * @param body 主体类型/主体 ID 与权限（permission 传空即撤销）
     * @return 操作结果
     */
    @SaCheckRole("superadmin")
    @PostMapping("/admin/shared-grant")
    public R<Void> grantShared(@Validated @RequestBody GrantBo body) {
        spaceService.grantSharedTo(body.memberType() == null || body.memberType().isBlank()
            ? org.dromara.easyshare.drive.domain.EsSpaceMember.TYPE_USER
            : body.memberType(), parseId(body.userId()), body.permission());
        return R.ok();
    }

    /**
     * 解析前端传来的雪花 ID 字符串。只放行纯数字，避免脏值进到 SQL 条件里。
     */
    private static Long parseId(String raw) {
        if (raw == null || raw.isBlank()) {
            throw new org.dromara.common.core.exception.ServiceException("账号标识不能为空");
        }
        for (int i = 0; i < raw.length(); i++) {
            if (raw.charAt(i) < '0' || raw.charAt(i) > '9') {
                throw new org.dromara.common.core.exception.ServiceException("账号标识非法");
            }
        }
        try {
            return Long.parseLong(raw);
        } catch (NumberFormatException e) {
            throw new org.dromara.common.core.exception.ServiceException("账号标识非法");
        }
    }

    /**
     * 个人配额请求体。
     *
     * @param userId     账号 ID（雪花 ID 字符串）
     * @param quotaBytes 配额字节数：0 收回、-1 不限
     */
    public record QuotaBo(
        @NotBlank(message = "账号标识不能为空") String userId,
        @NotNull(message = "配额不能为空") Long quotaBytes) {
    }

    /**
     * 共享配额请求体。
     *
     * @param quotaBytes 配额字节数
     */
    public record SharedQuotaBo(@NotNull(message = "配额不能为空") Long quotaBytes) {
    }

    /**
     * 共享授权请求体。
     *
     * @param userId     账号 ID
     * @param permission read / write，空字符串表示撤销
     */
    public record MemberVo(String memberType, String memberId, String permission, String name) {
    }

    public record DeptVo(String deptId, String deptName) {
    }

    public record GrantBo(
        @NotBlank(message = "主体标识不能为空") String userId,
        String memberType,
        String permission) {
    }
}
