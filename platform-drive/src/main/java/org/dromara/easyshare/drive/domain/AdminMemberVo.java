package org.dromara.easyshare.drive.domain;

import java.util.List;

/**
 * 权限管理页的成员行：身份 + 生效功能权限 + 所属角色。
 * <p>
 * 五项开关是**生效值**（角色并集 / 逐账号行 / 缺行全开），不是存储值；
 * {@code roleIds} 非空时客户端把开关显示为只读——要改就改角色。
 *
 * @param userId     账号 ID
 * @param userName   登录名
 * @param nickName   昵称
 * @param deptId     部门 ID
 * @param deptName   部门名（无部门为空串）
 * @param status     0 正常、1 停用（RuoYi 口径）
 * @param knowledge  知识问答
 * @param meeting    会议听记
 * @param drive      网盘
 * @param shared     共享空间
 * @param plugin     插件中心
 * @param roleIds    已分配角色 ID
 * @param roleNames  已分配角色名（与 roleIds 同序）
 * @param adminScope all=超管；dept=子管理员（本部门）；none=普通成员
 * @author EasyShare
 */
public record AdminMemberVo(
    Long userId,
    String userName,
    String nickName,
    Long deptId,
    String deptName,
    String status,
    boolean knowledge,
    boolean meeting,
    boolean drive,
    boolean shared,
    boolean plugin,
    List<Long> roleIds,
    List<String> roleNames,
    String adminScope
) {
}
