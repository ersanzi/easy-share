package org.dromara.easyshare.drive.domain;

import java.util.List;

/**
 * 当前登录账号的功能权限（{@code /easyshare/permission/mine} 的应答）。
 * <p>
 * 前五项与 {@link PermissionVo} 同形：知识服务 SSO 与桌面端都按这套扁平字段读。
 * 额外带上 {@code adminScope}（管理入口是否显示、以及是否只能管本部门）与角色名，
 * 供客户端展示「权限由角色决定」。
 *
 * @param userId     账号 ID
 * @param knowledge  知识问答
 * @param meeting    会议听记
 * @param drive      网盘
 * @param shared     共享空间
 * @param plugin     插件中心
 * @param adminScope all / dept / none
 * @param roles      角色名列表（无角色为空）
 * @author EasyShare
 */
public record MyPermissionVo(
    Long userId,
    boolean knowledge,
    boolean meeting,
    boolean drive,
    boolean shared,
    boolean plugin,
    String adminScope,
    List<String> roles
) {

    public static MyPermissionVo of(Long userId, PermissionVo flags, String adminScope, List<String> roles) {
        return new MyPermissionVo(
            userId,
            flags.knowledge(),
            flags.meeting(),
            flags.drive(),
            flags.shared(),
            flags.plugin(),
            adminScope,
            roles == null ? List.of() : List.copyOf(roles));
    }
}
