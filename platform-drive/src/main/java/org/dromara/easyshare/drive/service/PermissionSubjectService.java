package org.dromara.easyshare.drive.service;

import lombok.RequiredArgsConstructor;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.domain.SysDeptRow;
import org.dromara.easyshare.drive.domain.SysUserDept;
import org.dromara.easyshare.drive.mapper.SysDeptRowMapper;
import org.dromara.easyshare.drive.mapper.SysUserDeptMapper;
import org.springframework.stereotype.Service;

/** 权限主体的实时有效性；旧登录态和历史授权不能绕过停用、删除或部门失效。 */
@Service
@RequiredArgsConstructor
public class PermissionSubjectService {
    private final SysUserDeptMapper users;
    private final SysDeptRowMapper depts;

    public SysUserDept activeUser(Long userId) {
        if (userId == null || userId <= 0) return null;
        SysUserDept user = users.selectById(userId);
        if (user == null || !"0".equals(user.getStatus()) || !"0".equals(user.getDelFlag())) return null;
        Long deptId = user.getDeptId();
        return deptId == null || deptId == 0 || activeDept(deptId) ? user : null;
    }

    public SysUserDept requireActiveUser(Long userId) {
        SysUserDept user = activeUser(userId);
        if (user == null) throw new ServiceException("账号不存在、已停用或所属部门失效");
        return user;
    }

    /** 管理读不能只信任旧会话里的超管角色，也不应为读取获取权限写锁。 */
    public void requireActiveSuperAdmin() {
        Long actorId = LoginHelper.getUserId();
        requireActiveUser(actorId);
        if (!LoginHelper.isSuperAdmin(actorId)) {
            throw new ServiceException("只有超级管理员可以读取全部管理数据");
        }
    }

    public boolean activeDept(Long deptId) {
        if (deptId == null || deptId <= 0) return false;
        SysDeptRow dept = depts.selectById(deptId);
        return dept != null && "0".equals(dept.getStatus()) && "0".equals(dept.getDelFlag());
    }
}
