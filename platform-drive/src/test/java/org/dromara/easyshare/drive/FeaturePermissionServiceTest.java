package org.dromara.easyshare.drive;

import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.domain.EsPermissionRole;
import org.dromara.easyshare.drive.domain.EsUserPermission;
import org.dromara.easyshare.drive.domain.PermissionVo;
import org.dromara.easyshare.drive.mapper.EsUserPermissionMapper;
import org.dromara.easyshare.drive.mapper.EsUserRoleMapper;
import org.dromara.easyshare.drive.service.FeaturePermissionService;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;

import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

/**
 * 功能权限矩阵测试：角色并集优先、缺角色看逐账号行、再缺行全开、
 * 共享空间依赖网盘、upsert 语义、角色决定时拒绝写逐账号行。
 *
 * @author EasyShare
 */
class FeaturePermissionServiceTest {

    private EsUserPermissionMapper mapper;
    private EsUserRoleMapper userRoleMapper;
    private FeaturePermissionService service;

    @BeforeEach
    void setUp() {
        mapper = mock(EsUserPermissionMapper.class);
        userRoleMapper = mock(EsUserRoleMapper.class);
        service = new FeaturePermissionService(mapper, userRoleMapper);
    }

    private static EsPermissionRole role(boolean knowledge, boolean meeting, boolean drive,
                                         boolean shared, boolean plugin) {
        EsPermissionRole role = new EsPermissionRole();
        role.setRoleId(9L);
        role.setRoleName("车间");
        role.setAdminScope(EsPermissionRole.SCOPE_NONE);
        role.setKnowledge(knowledge);
        role.setMeeting(meeting);
        role.setDrive(drive);
        role.setShared(shared);
        role.setPlugin(plugin);
        return role;
    }

    private static EsUserPermission row(boolean knowledge, boolean meeting, boolean drive,
                                        boolean shared, boolean plugin) {
        EsUserPermission row = new EsUserPermission();
        row.setUserId(1L);
        row.setKnowledge(knowledge);
        row.setMeeting(meeting);
        row.setDrive(drive);
        row.setShared(shared);
        row.setPlugin(plugin);
        return row;
    }

    @Test
    void missingRowMeansEverythingOn() {
        when(mapper.selectById(1L)).thenReturn(null);

        assertTrue(service.enabled(1L, FeaturePermissionService.KNOWLEDGE));
        assertTrue(service.enabled(1L, FeaturePermissionService.MEETING));
        assertTrue(service.enabled(1L, FeaturePermissionService.DRIVE));
        assertTrue(service.enabled(1L, FeaturePermissionService.SHARED));
        assertTrue(service.enabled(1L, FeaturePermissionService.PLUGIN));

        PermissionVo vo = service.of(1L);
        assertTrue(vo.knowledge() && vo.meeting() && vo.drive() && vo.shared() && vo.plugin());
    }

    @Test
    void explicitRowTurnsFeaturesOff() {
        when(mapper.selectById(1L)).thenReturn(
            row(false, true, false, true, false));

        assertFalse(service.enabled(1L, FeaturePermissionService.KNOWLEDGE));
        assertTrue(service.enabled(1L, FeaturePermissionService.MEETING));
        assertFalse(service.enabled(1L, FeaturePermissionService.DRIVE));
        assertTrue(service.enabled(1L, FeaturePermissionService.SHARED));
        assertFalse(service.enabled(1L, FeaturePermissionService.PLUGIN));
    }

    @Test
    void sharedSpaceNeedsDriveToo() {
        // 只关 drive：shared 列仍为 true，但共享空间整体不可用
        when(mapper.selectById(1L)).thenReturn(row(true, true, false, true, true));
        assertFalse(service.sharedFeatureEnabled(1L));

        when(mapper.selectById(1L)).thenReturn(row(true, true, true, false, true));
        assertFalse(service.sharedFeatureEnabled(1L));

        when(mapper.selectById(1L)).thenReturn(row(true, true, true, true, true));
        assertTrue(service.sharedFeatureEnabled(1L));
    }

    @Test
    void invalidUserIdIsDenied() {
        assertFalse(service.enabled(null, FeaturePermissionService.DRIVE));
        assertFalse(service.enabled(0L, FeaturePermissionService.DRIVE));
    }

    @Test
    void unknownFeatureIsRejectedLoudly() {
        when(mapper.selectById(1L)).thenReturn(null);
        assertThrows(ServiceException.class, () -> service.enabled(1L, "mail"));
    }

    @Test
    void saveInsertsWhenNoRow() {
        when(mapper.selectById(7L)).thenReturn(null);

        service.save(7L, true, false, true, false, true);

        ArgumentCaptor<EsUserPermission> captor = ArgumentCaptor.forClass(EsUserPermission.class);
        verify(mapper).insert(captor.capture());
        EsUserPermission saved = captor.getValue();
        assertEquals(7L, saved.getUserId());
        assertTrue(saved.getKnowledge());
        assertFalse(saved.getMeeting());
        assertTrue(saved.getDrive());
        assertFalse(saved.getShared());
        assertTrue(saved.getPlugin());
    }

    @Test
    void saveUpdatesExistingRow() {
        EsUserPermission existing = row(true, true, true, true, true);
        existing.setUserId(7L);
        when(mapper.selectById(7L)).thenReturn(existing);

        service.save(7L, false, false, false, false, false);

        verify(mapper).updateById(existing);
        assertFalse(existing.getKnowledge());
        assertFalse(existing.getDrive());
    }

    @Test
    void listExplicitMapsRows() {
        when(mapper.selectList(any())).thenReturn(List.of(row(false, true, true, true, true)));

        List<PermissionVo> list = service.listExplicit();

        assertEquals(1, list.size());
        assertFalse(list.get(0).knowledge());
        assertTrue(list.get(0).meeting());
    }

    @Test
    void roleUnionOverridesPerUserRow() {
        // 两个角色的并集：knowledge 来自第一个，meeting/shared 来自第二个，plugin 两个都没开
        when(userRoleMapper.selectRolesOfUser(1L)).thenReturn(List.of(
            role(true, false, true, false, false),
            role(false, true, true, true, false)));

        PermissionVo vo = service.of(1L);

        assertTrue(vo.knowledge());
        assertTrue(vo.meeting());
        assertTrue(vo.drive());
        assertTrue(vo.shared());
        assertFalse(vo.plugin());
        assertFalse(service.enabled(1L, FeaturePermissionService.PLUGIN));
        // 有角色时逐账号行不参与裁决、也不该再被查询
        verify(mapper, never()).selectById(1L);
    }

    @Test
    void saveRejectsRoleDrivenUser() {
        when(userRoleMapper.selectRoleIdsOfUser(1L)).thenReturn(List.of(9L));

        assertThrows(ServiceException.class,
            () -> service.save(1L, true, true, true, true, true));
        verify(mapper, never()).insert(any(EsUserPermission.class));
    }
}
