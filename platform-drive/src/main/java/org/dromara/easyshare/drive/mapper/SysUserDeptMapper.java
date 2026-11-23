package org.dromara.easyshare.drive.mapper;

import org.dromara.common.mybatis.core.mapper.BaseMapperPlus;
import org.dromara.easyshare.drive.domain.SysUserDept;

/**
 * 系统用户只读投影 Mapper（部门归属查询）。
 *
 * @author EasyShare
 */
public interface SysUserDeptMapper extends BaseMapperPlus<SysUserDept, SysUserDept> {
    /** 仅搜索同一共享空间的账号；关键词作为值绑定，不展开为通配符。 */
    @org.apache.ibatis.annotations.Select("""
        SELECT u.user_id, u.dept_id, u.user_name, u.nick_name, u.status, u.del_flag
        FROM sys_user u
        WHERE u.status = '0' AND u.del_flag = '0'
          AND (u.dept_id IS NULL OR u.dept_id = 0 OR EXISTS (
            SELECT 1 FROM sys_dept d WHERE d.dept_id = u.dept_id AND d.status = '0' AND d.del_flag = '0'))
          AND (LOCATE(LOWER(#{keyword}), LOWER(u.user_name)) > 0
            OR LOCATE(LOWER(#{keyword}), LOWER(u.nick_name)) > 0)
          AND EXISTS (SELECT 1 FROM es_space_member m WHERE m.space_id = 1
            AND m.permission IN ('read', 'write')
            AND ((m.member_type = 'user' AND m.user_id = u.user_id)
              OR (m.member_type = 'dept' AND m.user_id = u.dept_id)))
        ORDER BY u.user_id LIMIT #{limit}
        """)
    java.util.List<SysUserDept> searchSharedCandidates(@org.apache.ibatis.annotations.Param("keyword") String keyword,
        @org.apache.ibatis.annotations.Param("limit") int limit);
}
