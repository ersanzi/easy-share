package org.dromara.easyshare.drive;

import org.dromara.common.core.exception.ServiceException;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.service.*;
import org.junit.jupiter.api.Test;
import java.util.Arrays;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

/** 公开控制器不能把漏传列表解释成撤销，也不能在取锁之前检查管理范围。 */
class PermissionControllerBoundaryTest {
    @Test void memberNullPayloadIsRejectedBeforeServiceMutation() {
        FileRoleService service = mock(FileRoleService.class);
        FileMemberController controller = new FileMemberController(service);
        try (var login = mockStatic(LoginHelper.class)) {
            login.when(LoginHelper::getUserId).thenReturn(7L);
            assertThrows(ServiceException.class, () -> controller.replace(42L, null));
            assertThrows(ServiceException.class, () -> controller.replace(42L, Arrays.asList((FileMemberController.MemberBo) null)));
            verifyNoInteractions(service);
        }
    }

    @Test void featureUpdateAcquiresLockBeforeRecheckingRangeAndNeverWritesAfterDenial() {
        var features = mock(FeaturePermissionService.class);
        var roles = mock(PermissionRoleService.class);
        var writes = mock(PermissionWriteService.class);
        var controller = new UserPermissionController(features, roles, writes);
        try (var login = mockStatic(LoginHelper.class)) {
            login.when(LoginHelper::getUserId).thenReturn(7L);
            assertThrows(ServiceException.class, () -> controller.save(new UserPermissionController.SaveBo(8L, true, true, true, true, true)));
            var order = inOrder(writes, roles);
            order.verify(writes).begin(8L);
            order.verify(roles).canManage(7L, 8L);
            verifyNoInteractions(features);
        }
    }
}
