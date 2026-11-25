package org.dromara.easyshare.drive;

import org.dromara.common.core.exception.ServiceException;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.mapper.*;
import org.dromara.easyshare.drive.service.*;
import org.junit.jupiter.api.*;
import org.mockito.MockedStatic;
import org.springframework.aop.framework.ProxyFactory;
import org.springframework.jdbc.datasource.DataSourceTransactionManager;
import org.springframework.transaction.annotation.AnnotationTransactionAttributeSource;
import org.springframework.transaction.interceptor.TransactionInterceptor;
import org.springframework.transaction.support.TransactionSynchronizationManager;
import tools.jackson.databind.json.JsonMapper;
import javax.sql.DataSource;
import java.sql.Connection;
import java.util.List;
import java.util.Map;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

/** 真实 Spring 事务代理驱动权限保存，数据库边界用 JDBC 替身验证提交与回滚。 */
class PermissionWriteServiceTest {
    private final EsPermissionWriteMapper mapper = mock(EsPermissionWriteMapper.class);
    private final SysUserDeptMapper users = mock(SysUserDeptMapper.class);
    private final SysDeptRowMapper depts = mock(SysDeptRowMapper.class);
    private PermissionSubjectService subjects;
    private PermissionWriteService writes;
    private MockedStatic<LoginHelper> login;

    @BeforeEach void setup() {
        subjects = PermissionTestFixtures.directory(users, depts);
        writes = new PermissionWriteService(mapper, subjects, new JsonMapper());
        login = mockStatic(LoginHelper.class);
        login.when(LoginHelper::getUserId).thenReturn(7L);
        when(mapper.lockGuard()).thenReturn(1);
        when(mapper.insertAudit(anyLong(), anyString(), anyString(), anyString(), anyString())).thenReturn(1);
    }

    @AfterEach void cleanup() {
        TransactionSynchronizationManager.clear();
        login.close();
    }

    @Test void writesOutsideTransactionAreRejectedBeforeTakingLocks() {
        assertThrows(ServiceException.class, () -> writes.begin(8L));
        assertThrows(ServiceException.class, () -> writes.audit("test", 8L, null, null));
        verifyNoInteractions(mapper);
    }

    @Test void missingMigrationFailsClosedBeforeSubjectChecks() {
        TransactionSynchronizationManager.setActualTransactionActive(true);
        when(mapper.lockGuard()).thenReturn(null);
        assertThrows(ServiceException.class, () -> writes.begin(8L));
        verify(mapper, never()).lockDepartments();
        verify(users, never()).selectById(7L);
    }

    @Test void busyAuthorizationTransactionReturnsRetryWithoutChangingPermissions() {
        TransactionSynchronizationManager.setActualTransactionActive(true);
        when(mapper.lockGuard()).thenThrow(new org.springframework.dao.CannotAcquireLockException("busy"));
        ServiceException error = assertThrows(ServiceException.class, () -> writes.begin(8L));
        assertTrue(error.getMessage().contains("刷新后重试"));
        verify(mapper, never()).lockDepartments();
        verify(mapper, never()).insertAudit(anyLong(), anyString(), anyString(), anyString(), anyString());
    }

    @Test void concurrentOrganizationChangeRollsBackAuthorizationTransaction() throws Exception {
        when(mapper.lockDepartments()).thenThrow(new org.springframework.dao.CannotAcquireLockException("busy"));
        Connection connection = runPermissionSave(false, false);
        verify(connection).rollback(); verify(connection, never()).commit();
        verify(mapper, never()).insertAudit(anyLong(), anyString(), anyString(), anyString(), anyString());
    }

    @Test void locksHaveOneOrderAndActorIsValidatedAfterLocking() {
        TransactionSynchronizationManager.setActualTransactionActive(true);
        writes.begin(8L, 3L, 8L, null, -1L);
        var order = inOrder(mapper, users);
        order.verify(mapper).lockGuard();
        order.verify(mapper).lockDepartments();
        order.verify(mapper).lockUsers(List.of(3L, 7L, 8L));
        order.verify(users).selectById(7L);
    }

    @Test void disabledOperatorCannotUseAnOldLoginSessionToWrite() {
        TransactionSynchronizationManager.setActualTransactionActive(true);
        var user = subjects.activeUser(7L); user.setStatus("1");
        when(users.selectById(7L)).thenReturn(user);
        assertThrows(ServiceException.class, () -> writes.begin(8L));
        verify(mapper, never()).insertAudit(anyLong(), anyString(), anyString(), anyString(), anyString());
    }

    @Test void superAdminGuardUsesCurrentAccountRatherThanSuppliedTarget() {
        assertThrows(ServiceException.class, writes::requireSuperAdmin);
        login.when(() -> LoginHelper.isSuperAdmin(7L)).thenReturn(true);
        assertDoesNotThrow(writes::requireSuperAdmin);
    }

    @Test void auditContainsActorAndStructuredBeforeAfterState() {
        TransactionSynchronizationManager.setActualTransactionActive(true);
        writes.audit("feature.save", 8L, Map.of("drive", true), Map.of("drive", false));
        verify(mapper).insertAudit(7L, "feature.save", "8", "{\"drive\":true}", "{\"drive\":false}");
    }

    @Test void failedAuditRollsBackActualSpringPermissionTransaction() throws Exception {
        when(mapper.insertAudit(anyLong(), anyString(), anyString(), anyString(), anyString())).thenReturn(0);
        Connection connection = runPermissionSave(false);
        verify(connection).rollback(); verify(connection, never()).commit();
    }

    @Test void databaseAuditExceptionAlsoRollsBackPermissionTransaction() throws Exception {
        when(mapper.insertAudit(anyLong(), anyString(), anyString(), anyString(), anyString()))
            .thenThrow(new org.springframework.dao.DataAccessResourceFailureException("审计库不可写"));
        Connection connection = runPermissionSave(false);
        verify(connection).rollback(); verify(connection, never()).commit();
    }

    @Test void successfulPermissionAndAuditCommitTogether() throws Exception {
        Connection connection = runPermissionSave(true);
        verify(connection).commit(); verify(connection, never()).rollback();
    }

    private Connection runPermissionSave(boolean success) throws Exception {
        return runPermissionSave(success, true);
    }

    private Connection runPermissionSave(boolean success, boolean reachedWrite) throws Exception {
        DataSource dataSource = mock(DataSource.class);
        Connection connection = mock(Connection.class);
        when(dataSource.getConnection()).thenReturn(connection);
        when(connection.getAutoCommit()).thenReturn(true);
        var permissions = mock(EsUserPermissionMapper.class);
        var bindings = mock(EsUserRoleMapper.class);
        FeaturePermissionService target = new FeaturePermissionService(permissions, bindings, subjects, writes);
        var proxyFactory = new ProxyFactory(target);
        proxyFactory.setProxyTargetClass(true);
        proxyFactory.addAdvice(new TransactionInterceptor(new DataSourceTransactionManager(dataSource),
            new AnnotationTransactionAttributeSource()));
        var proxy = (FeaturePermissionService) proxyFactory.getProxy();
        if (success) proxy.save(8L, false, false, false, false, false);
        else assertThrows(RuntimeException.class, () -> proxy.save(8L, false, false, false, false, false));
        if (reachedWrite) {
            verify(permissions).insert(any(org.dromara.easyshare.drive.domain.EsUserPermission.class));
            verify(mapper).insertAudit(eq(7L), eq("feature.save"), eq("8"), anyString(), anyString());
        } else {
            verifyNoInteractions(permissions);
        }
        return connection;
    }
}
