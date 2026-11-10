package org.dromara.easyshare.drive.domain;

import lombok.Data;

/**
 * 文档级成员角色行（{@code es_file_member} 的投影）。
 * <p>
 * 复合主键（file_id + member_type + member_id）与 {@link UserRoleBinding} 同款处理：
 * 实体只做投影，读写走 {@link org.dromara.easyshare.drive.mapper.EsFileMemberMapper}
 * 的显式 SQL。角色档位见 {@link org.dromara.easyshare.drive.service.FileRoleService} 常量。
 *
 * @author EasyShare
 */
@Data
public class EsFileMember {

    /** 主体类型：账号 */
    public static final String TYPE_USER = "user";

    /** 主体类型：部门 */
    public static final String TYPE_DEPT = "dept";

    private Long fileId;

    /** user / dept */
    private String memberType;

    /** sys_user.user_id 或 sys_dept.dept_id */
    private Long memberId;

    /** manage / edit / download / read */
    private String role;
}
