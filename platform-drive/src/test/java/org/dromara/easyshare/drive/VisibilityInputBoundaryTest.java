package org.dromara.easyshare.drive;

import jakarta.validation.Validation;
import jakarta.validation.Validator;
import jakarta.validation.ValidatorFactory;
import jakarta.validation.constraints.NotNull;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.mapper.EsFileMapper;
import org.dromara.easyshare.drive.mapper.EsFileMemberMapper;
import org.dromara.easyshare.drive.mapper.SysDeptRowMapper;
import org.dromara.easyshare.drive.mapper.SysUserDeptMapper;
import org.dromara.easyshare.drive.service.DriveChangeLogService;
import org.dromara.easyshare.drive.service.DriveFileService;
import org.dromara.easyshare.drive.service.FileRoleService;
import org.dromara.easyshare.drive.service.PermissionWriteService;
import org.dromara.easyshare.drive.service.SpaceService;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
import tools.jackson.databind.json.JsonMapper;

import java.util.List;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

/** 可见范围写入必须区分缺字段、null 和明确的空数组，不能因漏传而扩大权限。 */
class VisibilityInputBoundaryTest {
    private static ValidatorFactory validators;
    private static Validator validator;
    private final JsonMapper json = new JsonMapper();
    private final EsFileMapper rows = mock(EsFileMapper.class);
    private final DriveChangeLogService changes = mock(DriveChangeLogService.class);
    private final SpaceService spaces = mock(SpaceService.class);
    private final PermissionWriteService writes = mock(PermissionWriteService.class);
    private final EsFileMemberMapper members = mock(EsFileMemberMapper.class);
    private final FileRoleService roles = new FileRoleService(members, rows, spaces,
        mock(SysUserDeptMapper.class), mock(SysDeptRowMapper.class), writes);
    private final DriveFileService files = new DriveFileService(rows, changes, roles);

    @BeforeAll
    static void startValidation() {
        validators = Validation.buildDefaultValidatorFactory();
        validator = validators.getValidator();
    }

    @AfterAll
    static void closeValidation() {
        validators.close();
    }

    @ParameterizedTest
    @ValueSource(strings = {
        "{\"path\":\"docs/secret.txt\",\"space\":\"shared\"}",
        "{\"path\":\"docs/secret.txt\",\"space\":\"shared\",\"visibleDepts\":null}"
    })
    void missingOrNullListFailsValidationAndCannotReachPermissionWrites(String payload) {
        var body = json.readValue(payload, DriveController.VisibilityBo.class);
        assertNull(body.visibleDepts());
        var violations = validator.validate(body);
        assertEquals(1, violations.size());
        var violation = violations.iterator().next();
        assertEquals("visibleDepts", violation.getPropertyPath().toString());
        assertEquals(NotNull.class, violation.getConstraintDescriptor().getAnnotation().annotationType());

        // 即使绕过 MVC 校验直接调用服务，也必须在任何查询、取锁和写入之前拒绝。
        assertThrows(ServiceException.class,
            () -> files.setRegisteredVisibleDepts(body.space(), 0L, 7L, body.path(), body.visibleDepts()));
        verifyNoInteractions(rows, changes, members, spaces, writes);
    }

    @Test
    void directPersonalCallAlsoRejectsNullBeforeReadingOrUpdatingFile() {
        assertThrows(ServiceException.class,
            () -> files.setRegisteredVisibleDepts("personal", 7L, 7L, "docs/secret.txt", null));
        verifyNoInteractions(rows, changes, members, spaces, writes);
    }

    @Test
    void explicitEmptyArrayClearsOnlyDepartmentRestrictionThroughAuthorizedWrite() {
        var body = json.readValue(
            "{\"path\":\"docs/secret.txt\",\"space\":\"shared\",\"visibleDepts\":[]}",
            DriveController.VisibilityBo.class);
        assertTrue(validator.validate(body).isEmpty());
        assertEquals(List.of(), body.visibleDepts());
        EsFile row = restrictedFile();
        assertNull(FileRoleService.ladder(row, 8L, 200L, "read", null, null));

        assertEquals("", files.setRegisteredVisibleDepts(body.space(), 0L, 7L, body.path(), body.visibleDepts()));
        assertEquals("", row.getVisibleDepts());
        verify(writes).begin(7L);
        verify(rows).lockForPermissionUpdate(31L);
        verify(rows).updateById(row);
        verify(writes).audit("file.visibility", 31L, "100", "");
        // 清除部门限制后仍继承空间/成员裁决，不能让已撤销空间访问权的账号重新可见。
        assertEquals("download", FileRoleService.ladder(row, 8L, 200L, "read", null, null));
        assertEquals("read", FileRoleService.ladder(row, 8L, 200L, "write", "read", null));
        assertNull(FileRoleService.ladder(row, 8L, 200L, null, null, null));
    }

    @Test
    void explicitEmptyArrayStillRequiresManagePermission() {
        EsFile row = restrictedFile();
        row.setUploadBy(9L);
        when(spaces.sharedPermissionOf(7L)).thenReturn("read");
        assertThrows(ServiceException.class,
            () -> files.setRegisteredVisibleDepts("shared", 0L, 7L, "docs/secret.txt", List.of()));
        assertEquals("100", row.getVisibleDepts());
        verify(rows, never()).updateById(any(EsFile.class));
        verify(writes, never()).audit(any(), any(), any(), any());
    }

    @Test
    void serializationHelperKeepsItsExistingOptionalInputContract() {
        assertEquals("", DriveFileService.serializeDepts(null));
        assertEquals("", DriveFileService.serializeDepts(List.of()));
        assertEquals("100,200", DriveFileService.serializeDepts(List.of(100L, 200L)));
    }

    private EsFile restrictedFile() {
        EsFile row = new EsFile();
        row.setFileId(31L);
        row.setSpaceType("shared");
        row.setOwnerId(0L);
        row.setUploadBy(7L);
        row.setState(EsFile.STATE_ACTIVE);
        row.setFilePath("docs/secret.txt");
        row.setVisibleDepts("100");
        when(rows.selectOne(any())).thenReturn(row);
        when(rows.selectById(31L)).thenReturn(row);
        when(rows.lockForPermissionUpdate(31L)).thenReturn(31L);
        when(spaces.deptIdOf(7L)).thenReturn(100L);
        when(spaces.sharedPermissionOf(7L)).thenReturn("write");
        return row;
    }
}
