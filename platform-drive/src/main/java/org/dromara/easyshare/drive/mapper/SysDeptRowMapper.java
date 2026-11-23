package org.dromara.easyshare.drive.mapper;

import org.dromara.common.mybatis.core.mapper.BaseMapperPlus;
import org.dromara.easyshare.drive.domain.SysDeptRow;

/**
 * 系统部门只读投影 Mapper（管理页部门下拉数据源）。
 *
 * @author EasyShare
 */
public interface SysDeptRowMapper extends BaseMapperPlus<SysDeptRow, SysDeptRow> {
    /** 搜索共享空间显式授权的有效部门，不列举全公司组织树。 */
    @org.apache.ibatis.annotations.Select("""
        SELECT d.dept_id, d.parent_id, d.dept_name, d.status, d.del_flag FROM sys_dept d
        WHERE d.status = '0' AND d.del_flag = '0'
          AND LOCATE(LOWER(#{keyword}), LOWER(d.dept_name)) > 0
          AND EXISTS (SELECT 1 FROM es_space_member m WHERE m.space_id = 1
            AND m.member_type = 'dept' AND m.user_id = d.dept_id AND m.permission IN ('read', 'write'))
        ORDER BY d.dept_id LIMIT #{limit}
        """)
    java.util.List<SysDeptRow> searchSharedCandidates(@org.apache.ibatis.annotations.Param("keyword") String keyword,
        @org.apache.ibatis.annotations.Param("limit") int limit);
}
