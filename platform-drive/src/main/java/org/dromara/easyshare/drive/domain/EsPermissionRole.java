package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;
import lombok.EqualsAndHashCode;
import org.dromara.common.mybatis.core.domain.BaseEntity;

/**
 * 权限角色（权限组）：五项功能开关挂在角色上，成员继承角色并集。
 * <p>
 * {@code adminScope != none} 的角色同时是「子管理员」授权：成员可以打开管理页的权限矩阵，
 * 管理范围按档位裁剪——本部门 / 本部门及下级 / 指定部门（{@code scopeDeptIds}）/ 全部成员。
 * 普通权限组为 {@code none}。多个管理角色的范围取并集。
 * <p>
 * 字段与 {@code deploy/ruoyi-db/easyshare-permission-role.sql} 一一对应。
 *
 * @author EasyShare
 */
@Data
@EqualsAndHashCode(callSuper = true)
@TableName("es_permission_role")
public class EsPermissionRole extends BaseEntity {

    /** 普通权限组：只决定功能开关 */
    public static final String SCOPE_NONE = "none";

    /** 子管理员：可管理本部门成员的功能权限 */
    public static final String SCOPE_DEPT = "dept";

    /** 子管理员：可管理本部门及下级部门成员的功能权限 */
    public static final String SCOPE_DEPT_CHILDREN = "dept_children";

    /** 子管理员：可管理指定部门（{@link #scopeDeptIds}）成员的功能权限 */
    public static final String SCOPE_SPECIFIED = "specified";

    /** 子管理员：可管理全部成员的功能权限（角色定义仍只有超管能改） */
    public static final String SCOPE_ALL = "all";

    @TableId(value = "role_id")
    private Long roleId;

    /** 角色名，全局唯一 */
    private String roleName;

    /** none / dept / dept_children / specified / all，见本类常量 */
    private String adminScope;

    /** 指定部门范围：逗号分隔的 sys_dept.dept_id，仅 adminScope=specified 时生效 */
    private String scopeDeptIds;

    /** 知识问答：知识库查询与入库 */
    private Boolean knowledge;

    /** 会议听记：录音转写与纪要 */
    private Boolean meeting;

    /** 网盘：个人空间文件读写 */
    private Boolean drive;

    /** 共享空间：需同时开启 drive */
    private Boolean shared;

    /** 插件中心：扩展的安装与使用 */
    private Boolean plugin;
}
