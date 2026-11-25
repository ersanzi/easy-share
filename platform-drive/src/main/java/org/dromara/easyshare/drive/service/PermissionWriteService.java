package org.dromara.easyshare.drive.service;

import tools.jackson.core.JacksonException;
import tools.jackson.databind.json.JsonMapper;
import lombok.RequiredArgsConstructor;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.mapper.EsPermissionWriteMapper;
import org.springframework.stereotype.Service;
import org.springframework.dao.PessimisticLockingFailureException;
import org.springframework.transaction.support.TransactionSynchronizationManager;
import java.util.ArrayList;
import java.util.TreeSet;

/** 权限变更的事务边界：先串行化并锁主体，再裁决，最后同事务写入审计。 */
@Service
@RequiredArgsConstructor
public class PermissionWriteService {
    private final EsPermissionWriteMapper mapper;
    private final PermissionSubjectService subjects;
    private final JsonMapper json;

    public void begin(Long... affectedUsers) {
        requireTransaction();
        try {
            if (!Integer.valueOf(1).equals(mapper.lockGuard()))
                throw new ServiceException("权限写保护尚未初始化，请联系管理员完成迁移");
            Long actorId = LoginHelper.getUserId();
            if (actorId == null || actorId <= 0) throw new ServiceException("登录账号无效");
            // 所有路径统一按组织树、账号升序取锁，防止交错替换和部门变更的检查/写入竞态。
            mapper.lockDepartments();
            TreeSet<Long> ids = new TreeSet<>();
            ids.add(actorId);
            for (Long id : affectedUsers) if (id != null && id > 0) ids.add(id);
            mapper.lockUsers(new ArrayList<>(ids));
            subjects.requireActiveUser(actorId);
        } catch (PessimisticLockingFailureException e) {
            throw new ServiceException("权限或组织信息正在更新，请刷新后重试");
        }
    }

    public void requireSuperAdmin() {
        if (!LoginHelper.isSuperAdmin(LoginHelper.getUserId()))
            throw new ServiceException("只有超级管理员可以修改角色或空间授权");
    }

    /** 只记录权限状态与标识；审计失败必须回滚授权，不能只打印日志后继续成功。 */
    public void audit(String action, Object targetId, Object before, Object after) {
        requireTransaction();
        try {
            if (mapper.insertAudit(LoginHelper.getUserId(), action, String.valueOf(targetId),
                json.writeValueAsString(before), json.writeValueAsString(after)) != 1)
                throw new ServiceException("权限审计写入失败");
        } catch (JacksonException e) {
            throw new ServiceException("权限审计序列化失败");
        }
    }

    private static void requireTransaction() {
        if (!TransactionSynchronizationManager.isActualTransactionActive())
            throw new ServiceException("权限变更必须在事务内执行");
    }
}
