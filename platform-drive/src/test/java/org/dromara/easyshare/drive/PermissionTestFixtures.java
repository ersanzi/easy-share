package org.dromara.easyshare.drive;

import org.dromara.easyshare.drive.domain.*;
import org.dromara.easyshare.drive.mapper.*;
import org.dromara.easyshare.drive.service.PermissionSubjectService;
import static org.mockito.Mockito.*;
import static org.mockito.ArgumentMatchers.*;

/** 旧行为测试的有效组织目录，拒绝分支须显式覆盖对应主体。 */
final class PermissionTestFixtures {
    static PermissionSubjectService directory(SysUserDeptMapper users, SysDeptRowMapper depts) {
        when(users.selectById(any())).thenAnswer(call -> {
            Long id = call.getArgument(0);
            if (id == null || id <= 0) return null;
            SysUserDept user = new SysUserDept(); user.setUserId(id);
            user.setStatus("0"); user.setDelFlag("0");
            return user;
        });
        when(depts.selectById(any())).thenAnswer(call -> {
            Long id = call.getArgument(0);
            if (id == null || id <= 0) return null;
            SysDeptRow dept = new SysDeptRow(); dept.setDeptId(id);
            dept.setStatus("0"); dept.setDelFlag("0");
            return dept;
        });
        return new PermissionSubjectService(users, depts);
    }
}
