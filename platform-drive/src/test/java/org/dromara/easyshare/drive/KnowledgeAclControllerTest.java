package org.dromara.easyshare.drive;

import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.domain.*;
import org.dromara.easyshare.drive.mapper.*;
import org.dromara.easyshare.drive.service.*;
import org.junit.jupiter.api.*;
import org.mockito.MockedStatic;
import java.util.List;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

/** 公开知识授权接口：真实服务协作，只替换登录、数据库与对象存储边界。 */
class KnowledgeAclControllerTest {
    EsFileMapper files;
    EsFileMemberMapper members;
    EsSpaceMemberMapper spaceMembers;
    EsUserPermissionMapper permissions;
    DriveStorage storage;
    KnowledgeAclController controller;
    MockedStatic<LoginHelper> login;
    EsFile row;

    @BeforeEach void setup() {
        files = mock(EsFileMapper.class);
        members = mock(EsFileMemberMapper.class);
        spaceMembers = mock(EsSpaceMemberMapper.class);
        permissions = mock(EsUserPermissionMapper.class);
        var users = mock(SysUserDeptMapper.class);
        var depts = mock(SysDeptRowMapper.class);
        var roles = mock(EsUserRoleMapper.class);
        when(roles.selectRolesOfUser(anyLong())).thenReturn(List.of());
        var features = new FeaturePermissionService(permissions, roles);
        var spacesMapper = mock(EsSpaceMapper.class);
        var shared = new EsSpace(); shared.setStatus("0"); shared.setQuotaBytes(-1L);
        when(spacesMapper.selectById(1L)).thenReturn(shared);
        var spaces = new SpaceService(spacesMapper, spaceMembers, mock(SpaceUsageService.class),
            mock(CapacityService.class), users, depts, features);
        var fileRoles = new FileRoleService(members, files, spaces, users, depts);
        storage = mock(DriveStorage.class);
        var properties = new DriveProperties(); properties.setBucket("easyshare");
        controller = new KnowledgeAclController(new KnowledgeAclService(files, spaces, fileRoles, features, storage, properties));
        login = mockStatic(LoginHelper.class); login.when(LoginHelper::getUserId).thenReturn(2L);
        row = new EsFile(); row.setFileId(42L); row.setVersionId(3L); row.setSpaceType("shared");
        row.setOwnerId(0L); row.setUploadBy(1L); row.setState("active"); row.setFilePath("policy.txt"); row.setFileName("policy.txt");
        when(files.selectByIds(anyCollection())).thenReturn(List.of(row));
        when(files.selectById(42L)).thenReturn(row);
        when(members.selectRole(42L, "user", 2L)).thenReturn("read");
        var membership = new EsSpaceMember(); membership.setPermission("read");
        when(spaceMembers.selectOne(any())).thenReturn(membership);
    }
    @AfterEach void teardown() { login.close(); }
    @Test void bindsOnlyRegisteredAuthorizedCurrentVersionAndRealObjectDigest() {
        when(members.selectRole(42L, "user", 2L)).thenReturn("download");
        when(files.selectOne(any())).thenReturn(row);
        when(storage.metadataAt("shared/", "policy.txt")).thenReturn(new DriveStorage.ObjectMetadata(6, "etag"));
        when(storage.sha256At("shared/", "policy.txt")).thenReturn("a".repeat(64));
        var binding = controller.bind(new KnowledgeAclController.BindBo("42", "3", null, null)).getData();
        assertNotNull(binding);
        assertEquals("42", binding.fileId()); assertEquals("3", binding.versionId());
        assertEquals("shared/policy.txt", binding.objectKey()); assertEquals("easyshare", binding.bucket());
        assertEquals("a".repeat(64), binding.sourceSha256()); assertEquals(6L, binding.sourceBytes());
    }

    @Test void staleVersionCannotBindOrReadStorage() {
        when(files.selectOne(any())).thenReturn(row);
        when(members.selectRole(42L, "user", 2L)).thenReturn("download");
        assertThrows(org.dromara.common.core.exception.ServiceException.class,
            () -> controller.bind(new KnowledgeAclController.BindBo("42", "2", null, null)));
        verifyNoInteractions(storage);
    }

    @Test void legacyObjectKeyBindsOnlyItsExactNamespaceRegisteredRow() {
        when(members.selectRole(42L, "user", 2L)).thenReturn("read");
        when(files.selectOne(any())).thenReturn(row);
        when(storage.metadataAt("shared/", "policy.txt")).thenReturn(new DriveStorage.ObjectMetadata(6, "etag"));
        when(storage.sha256At("shared/", "policy.txt")).thenReturn("a".repeat(64));
        var binding = controller.bind(new KnowledgeAclController.BindBo(null, null, "shared/policy.txt", "read")).getData();
        assertEquals("42", binding.fileId());
        assertThrows(org.dromara.common.core.exception.ServiceException.class,
            () -> controller.bind(new KnowledgeAclController.BindBo(null, null, "users/2/policy.txt", "read")));
    }

    @Test void readMemberCanReadButCannotDownload() {
        var access = controller.access(new KnowledgeAclController.AccessBo(List.of("42"))).getData().getFirst();
        assertTrue(access.read()); assertFalse(access.download()); assertEquals("42", access.fileId());
    }
}
