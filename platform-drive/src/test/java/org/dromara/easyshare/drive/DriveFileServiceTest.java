package org.dromara.easyshare.drive;

import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.mapper.EsFileMapper;
import org.dromara.easyshare.drive.service.DriveChangeLogService;
import org.dromara.easyshare.drive.service.DriveFileService;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;
import com.baomidou.mybatisplus.core.MybatisConfiguration;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.baomidou.mybatisplus.core.metadata.TableInfoHelper;
import org.apache.ibatis.builder.MapperBuilderAssistant;
import org.springframework.dao.DuplicateKeyException;

import java.time.Instant;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

/**
 * 文件目录层服务测试：登记幂等、并发回退、列表补账、fileId 越权防护。
 *
 * @author EasyShare
 */
class DriveFileServiceTest {

    private EsFileMapper mapper;
    private DriveChangeLogService changeLog;
    private DriveFileService service;

    @BeforeAll
    static void initMybatisPlusMetadata() {
        // 服务层用 LambdaUpdateWrapper/LambdaQueryWrapper 引用实体方法
        // （EsFile::getFilePath 等），MyBatis-Plus 需要「实体 → 列名」的 lambda 缓存
        // 才能解析它们。真实运行由 MyBatis 扫描 Mapper 时经 TableInfoHelper 建立，
        // 而这里是纯 mock 单测、没有 Spring/MyBatis 上下文，缓存为空——
        // 于是 restoreFromRecycle 构造 LambdaUpdateWrapper 时会抛
        // "can not find lambda cache for this entity"。
        //
        // 手工补一次初始化：构造一个最小的 MybatisConfiguration + MapperBuilderAssistant
        // 并调用 TableInfoHelper.initTableInfo，让 MP 把 @TableName/@TableId 等注解
        // 解析成表元数据与 lambda 缓存。
        MybatisConfiguration configuration = new MybatisConfiguration();
        MapperBuilderAssistant assistant =
            new MapperBuilderAssistant(configuration, "");
        TableInfoHelper.initTableInfo(assistant, EsFile.class);
    }

    @BeforeEach
    void setUp() {
        mapper = mock(EsFileMapper.class);
        changeLog = mock(DriveChangeLogService.class);
        service = new DriveFileService(mapper, changeLog);
    }

    private static EsFile row(long fileId, String path) {
        EsFile row = new EsFile();
        row.setFileId(fileId);
        row.setSpaceType(EsFile.TYPE_PERSONAL);
        row.setOwnerId(1L);
        row.setFilePath(path);
        row.setFileName(path.substring(path.lastIndexOf('/') + 1));
        row.setFileSize(1L);
        return row;
    }

    @Test
    void registerOnPresignInsertsNormalizedPath() {
        when(mapper.selectOne(any())).thenReturn(null);

        service.registerOnPresign(EsFile.TYPE_PERSONAL, 1L, 1L, "photos\\2024\\a.jpg", 42L);

        ArgumentCaptor<EsFile> captor = ArgumentCaptor.forClass(EsFile.class);
        verify(mapper).insert(captor.capture());
        EsFile inserted = captor.getValue();
        assertEquals("photos/2024/a.jpg", inserted.getFilePath());
        assertEquals("a.jpg", inserted.getFileName());
        assertEquals(1L, inserted.getOwnerId());
        assertEquals(42L, inserted.getFileSize());
    }

    @Test
    void registerOnPresignUpdatesSizeWhenDeclared() {
        EsFile existing = row(7L, "a.txt");
        existing.setFileSize(1L);
        existing.setVersionId(0L);
        when(mapper.selectOne(any())).thenReturn(existing);
        when(mapper.update(any(), any())).thenReturn(1); // CAS 命中

        service.registerOnPresign(EsFile.TYPE_PERSONAL, 1L, 1L, "a.txt", 99L);

        // 改造后走**条件更新**（LambdaUpdateWrapper），不再是 updateById：
        // 版本判定放进 UPDATE 的 WHERE 里，由数据库保证原子性，避免读-改-写互相覆盖。
        var captor = ArgumentCaptor.forClass(LambdaUpdateWrapper.class);
        verify(mapper).update(eq(null), captor.capture());
        String sql = ((LambdaUpdateWrapper<?>) captor.getValue()).getSqlSet();
        assertTrue(sql.contains("version_id"), "条件更新必须推进 version_id，实得: " + sql);
        assertTrue(sql.contains("file_size"), "申报大小 > 0 时必须更新 file_size，实得: " + sql);
        verify(mapper, never()).insert(any(EsFile.class));
    }

    @Test
    void registerOnPresignBumpsVersionOnOverwrite() {
        // 覆盖上传 = 版本单调递增；申报大小未知时也不阻挡版本推进
        EsFile existing = row(7L, "a.txt");
        existing.setFileSize(5L);
        existing.setVersionId(3L);
        when(mapper.selectOne(any())).thenReturn(existing);
        when(mapper.update(any(), any())).thenReturn(1); // CAS 命中

        service.registerOnPresign(EsFile.TYPE_PERSONAL, 1L, 1L, "a.txt", 0L);

        // 申报大小未知（0）时不得把已知大小冲成 0——SQL 里不该出现 file_size
        var captor = ArgumentCaptor.forClass(LambdaUpdateWrapper.class);
        verify(mapper).update(eq(null), captor.capture());
        String sql = ((LambdaUpdateWrapper<?>) captor.getValue()).getSqlSet();
        assertTrue(sql.contains("version_id"), "版本仍须推进，实得: " + sql);
        assertFalse(sql.contains("file_size"),
            "申报大小未知时不得写入 file_size（否则把已知大小冲成 0），实得: " + sql);
    }

    @Test
    void registerOnPresignNullVersionStartsFromZero() {
        // 列上线前的存量行 version_id 为 NULL：视为 0，首次覆盖落到 1
        EsFile existing = row(7L, "a.txt");
        when(mapper.selectOne(any())).thenReturn(existing);
        when(mapper.update(any(), any())).thenReturn(1); // CAS 命中

        service.registerOnPresign(EsFile.TYPE_PERSONAL, 1L, 1L, "a.txt", 1L);

        var captor = ArgumentCaptor.forClass(LambdaUpdateWrapper.class);
        verify(mapper).update(eq(null), captor.capture());
        assertTrue(((LambdaUpdateWrapper<?>) captor.getValue()).getParamNameValuePairs().containsValue(1L),
            "NULL 版本应视为 0 并跳到 1");
    }
    // ── 切片 1：乐观并发（CAS）——消灭「静默覆盖」──────────────────
    //
    // 背景：2026-10-16 在真机端到端实测确认过静默覆盖——客户端 A 写入成功（PUT 200），
    // 客户端 B 随后写入成功，最终只剩 B 的内容，A 的内容无声消失。
    // 这组用例把那条路径钉住：版本不匹配必须**拒绝**，而不是覆盖。

    @Test
    void registerOnPresignRejectsStaleVersionInsteadOfOverwriting() {
        // 调用方看到的是版本 3，但当前已经是 5 —— 说明中途有人改过
        EsFile existing = row(7L, "a.txt");
        existing.setVersionId(5L);
        when(mapper.selectOne(any())).thenReturn(existing);

        ServiceException ex = assertThrows(ServiceException.class, () -> service.registerOnPresign(
            EsFile.TYPE_PERSONAL, 1L, 1L, "a.txt", 99L, 3L));

        assertTrue(ex.getMessage().contains("已被其他人修改"), "错误要说清是并发冲突: " + ex.getMessage());
        assertTrue(ex.getMessage().contains("5"), "错误要带上当前版本以便用户判断: " + ex.getMessage());
        // 关键：**一个字段都不许写**
        verify(mapper, never()).update(any(), any());
        verify(mapper, never()).insert(any(EsFile.class));
    }

    @Test
    void registerOnPresignAcceptsMatchingVersion() {
        // 调用方看到的版本与当前一致 → 放行，版本推进
        EsFile existing = row(7L, "a.txt");
        existing.setVersionId(3L);
        when(mapper.selectOne(any())).thenReturn(existing);
        when(mapper.update(any(), any())).thenReturn(1);

        service.registerOnPresign(EsFile.TYPE_PERSONAL, 1L, 1L, "a.txt", 99L, 3L);

        var captor = ArgumentCaptor.forClass(LambdaUpdateWrapper.class);
        verify(mapper).update(eq(null), captor.capture());
        LambdaUpdateWrapper<?> w = (LambdaUpdateWrapper<?>) captor.getValue();
        // WHERE 必须比对版本（原子性就在这）：SQL 片段应出现 version_id
        assertTrue(w.getSqlSegment().contains("version_id"),
            "条件更新的 WHERE 必须带 version_id，实得: " + w.getSqlSegment());
        // SET 里应把版本推进到 4
        assertTrue(w.getSqlSet().contains("version_id"), "SET 应包含 version_id: " + w.getSqlSet());
        assertTrue(w.getParamNameValuePairs().containsValue(4L),
            "版本应推进到 4，实得参数: " + w.getParamNameValuePairs());
    }


    @Test
    void registerOnPresignRejectsWhenRowVanishedButCallerSawOne() {
        // 调用方以为文件存在（带了版本 2），但目录层已经没有 active 行
        // （例如别人刚删掉）——这同样是冲突，不能当成「新建」静默放行。
        when(mapper.selectOne(any())).thenReturn(null);

        ServiceException ex = assertThrows(ServiceException.class, () -> service.registerOnPresign(
            EsFile.TYPE_PERSONAL, 1L, 1L, "a.txt", 99L, 2L));

        assertTrue(ex.getMessage().contains("已被删除"), "应提示文件已被删除: " + ex.getMessage());
        verify(mapper, never()).insert(any(EsFile.class));
    }

    @Test
    void registerOnPresignLosesRaceOnConditionalUpdate() {
        // 版本比对通过，但条件更新影响 0 行 —— 说明「读完之后、写之前」有人先改了。
        // 这一步是必须的：只靠先读后比仍有 TOCTOU 窗口，原子性由 UPDATE 的 WHERE 保证。
        EsFile existing = row(7L, "a.txt");
        existing.setVersionId(3L);
        when(mapper.selectOne(any())).thenReturn(existing);
        when(mapper.update(any(), any())).thenReturn(0); // 条件未命中

        ServiceException ex = assertThrows(ServiceException.class, () -> service.registerOnPresign(
            EsFile.TYPE_PERSONAL, 1L, 1L, "a.txt", 99L, 3L));

        assertTrue(ex.getMessage().contains("上传期间被其他人修改"),
            "应报「期间被改」而不是普通冲突: " + ex.getMessage());
    }

    @Test
    void registerOnPresignWithoutExpectedVersionKeepsOldBehavior() {
        // 不传 expectedVersion = 不做校验（旧调用方/新建场景），行为与改造前一致。
        // 这条防的是「为了加 CAS 把既有路径全变成冲突」。
        EsFile existing = row(7L, "a.txt");
        existing.setVersionId(7L);
        when(mapper.selectOne(any())).thenReturn(existing);
        when(mapper.update(any(), any())).thenReturn(1);

        service.registerOnPresign(EsFile.TYPE_PERSONAL, 1L, 1L, "a.txt", 99L);

        verify(mapper).update(eq(null), any(LambdaUpdateWrapper.class));
    }

    @Test
    void registerOnPresignAllowsFirstWriteWithExpectedZero() {
        // expectedVersion = 0 表示「我认为这是首次登记」。
        // 若目录层确实没有行 → 放行新建（不能把它当冲突）。
        when(mapper.selectOne(any())).thenReturn(null);

        service.registerOnPresign(EsFile.TYPE_PERSONAL, 1L, 1L, "new.txt", 10L, 0L);

        verify(mapper).insert(any(EsFile.class));
    }

    @Test
    void registerOnPresignRejectsSecondFirstWriteOnSamePath() {
        // 这条是**真机实测逼出来的**：CAS 第一版把新行的 version_id 留成默认 0，
        // 于是「我期望版本 0（我认为文件还不存在）」与「文件已存在且处于版本 0」
        // 碰撞——两个客户端都带 expectedVersion=0 连续首次写入时，
        // 第二个不会被拒（实测：A code=200、B code=200）。
        //
        // 修法：首次登记直接写 version_id = 1，语义唯一——0 = 不存在，n>=1 = 第 n 次写入。
        EsFile existing = row(7L, "a.txt");
        existing.setVersionId(1L); // 首次登记后就是第 1 版
        when(mapper.selectOne(any())).thenReturn(existing);

        ServiceException ex = assertThrows(ServiceException.class, () -> service.registerOnPresign(
            EsFile.TYPE_PERSONAL, 1L, 1L, "a.txt", 10L, 0L));

        assertTrue(ex.getMessage().contains("已被其他人修改"),
            "第二次「首次写入」必须被拒，实得: " + ex.getMessage());
        verify(mapper, never()).update(any(), any());
    }

    @Test
    void insertSafelyMarksFirstVersionAsOne() {
        // 首次登记即第 1 版——上一条的前提，单独钉住，
        // 免得有人把这一行当冗余删掉，CAS 又退化成碰撞。
        when(mapper.selectOne(any())).thenReturn(null);

        service.registerOnPresign(EsFile.TYPE_PERSONAL, 1L, 1L, "new.txt", 10L, 0L);

        ArgumentCaptor<EsFile> captor = ArgumentCaptor.forClass(EsFile.class);
        verify(mapper).insert(captor.capture());
        assertEquals(1L, captor.getValue().getVersionId(),
            "首次登记必须写 version_id=1，否则 0 会与「已存在且版本 0」碰撞");
    }

    @Test
    void insertRaceFallsBackToExistingRow() {
        // insertSafely 直接插入、撞唯一键后补查已存在行——唯一的 selectOne 发生在兜底路径
        when(mapper.selectOne(any())).thenReturn(row(7L, "a.txt"));
        when(mapper.insert(any(EsFile.class))).thenThrow(new DuplicateKeyException("uk_es_file_path"));

        when(mapper.selectList(any())).thenReturn(List.of());
        Long result = service.list(EsFile.TYPE_PERSONAL, 1L, 1L,
            List.of(new DriveObject("a.txt", 1L, Instant.now()))).fileIds().get("a.txt");

        assertEquals(7L, result);
    }

    @Test
    void reconcileMapsExistingAndInsertsMissing() {
        when(mapper.selectList(any())).thenReturn(List.of(row(7L, "a.txt")));
        when(mapper.insert(any(EsFile.class))).thenAnswer(invocation -> {
            EsFile inserted = invocation.getArgument(0, EsFile.class);
            inserted.setFileId(999L);
            return 1;
        });

        Map<String, Long> result = service.list(EsFile.TYPE_PERSONAL, 1L, 1L, List.of(
            new DriveObject("a.txt", 1L, Instant.now()),
            new DriveObject("photos/b.jpg", 2L, Instant.now()))).fileIds();

        assertEquals(7L, result.get("a.txt"));
        assertEquals(999L, result.get("photos/b.jpg"));
        ArgumentCaptor<EsFile> captor = ArgumentCaptor.forClass(EsFile.class);
        verify(mapper).insert(captor.capture());
        assertEquals("photos/b.jpg", captor.getValue().getFilePath());
        assertEquals("b.jpg", captor.getValue().getFileName());
    }

    @Test
    void resolveByFileIdChecksOwnership() {
        // 他人（ownerId=2）的行：个人空间 ownerId=1 的请求必须被拒
        EsFile foreign = row(7L, "secret.txt");
        foreign.setOwnerId(2L);
        when(mapper.selectById(7L)).thenReturn(foreign);

        ServiceException ex = assertThrows(ServiceException.class,
            () -> service.resolveDeletePath(EsFile.TYPE_PERSONAL, 1L, 7L, null));
        assertEquals("文件不存在或不属于当前空间", ex.getMessage());

        EsFile mine = row(7L, "mine.txt");
        mine.setState(EsFile.STATE_ACTIVE);
        when(mapper.selectById(7L)).thenReturn(mine);
        assertEquals("mine.txt", service.resolveDeletePath(EsFile.TYPE_PERSONAL, 1L, 7L, null));
    }

    @Test
    void resolveByFileIdRejectsRecycledRow() {
        // 回收站行不能被"再删一次"（彻底删除走 purge 链路）
        EsFile recycled = row(7L, "old.txt");
        recycled.setState(EsFile.STATE_RECYCLED);
        when(mapper.selectById(7L)).thenReturn(recycled);

        assertThrows(ServiceException.class,
            () -> service.resolveDeletePath(EsFile.TYPE_PERSONAL, 1L, 7L, null));
    }

    @Test
    void resolveByPathNormalizesWhenFileIdAbsent() {
        assertEquals("photos/a.jpg",
            service.resolveDeletePath(EsFile.TYPE_PERSONAL, 1L, null, "photos\\a.jpg"));
        verify(mapper, never()).selectById(any());
    }

    @Test
    void deleteRegisteredRemovesBySpaceAndPath() {
        service.deleteRegistered(EsFile.TYPE_SHARED, EsFile.SHARED_OWNER, "docs/x.pdf");

        ArgumentCaptor<com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper<EsFile>> captor =
            ArgumentCaptor.forClass(com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper.class);
        verify(mapper).delete(captor.capture());
        // Wrapper 条件无法直接断言内部值，落点在"确实按空间+路径删除"不抛错即可
        assertNull(captor.getValue().getEntity());
    }

@Test
void setVisibilityChecksOperatorOwnership() {
    // 共享空间目录行：owner=0、uploadBy=2（上传者本人可设，他人被拒）
    EsFile sharedRow = new EsFile();
    sharedRow.setFileId(31L);
    sharedRow.setSpaceType(EsFile.TYPE_SHARED);
    sharedRow.setOwnerId(0L);
    sharedRow.setUploadBy(2L);
    sharedRow.setFilePath("docs/x.pdf");
    sharedRow.setFileName("x.pdf");
    sharedRow.setFileSize(1L);
    when(mapper.selectOne(any())).thenReturn(sharedRow, sharedRow);

    assertEquals("3,7", service.setRegisteredVisibleDepts(
        EsFile.TYPE_SHARED, 0L, 2L, "docs/x.pdf", java.util.List.of(3L, 7L)));
    assertEquals("3,7", sharedRow.getVisibleDepts());

    ServiceException ex = assertThrows(ServiceException.class,
        () -> service.setRegisteredVisibleDepts(
            EsFile.TYPE_SHARED, 0L, 3L, "docs/x.pdf", java.util.List.of()));
    assertEquals("只有文件的上传者可以调整可见范围", ex.getMessage());
}

@Test
void sharedVisibilityFilterHidesFromOtherDepartments() {
    EsFile limited = new EsFile();
    limited.setSpaceType(EsFile.TYPE_SHARED);
    limited.setOwnerId(0L);
    limited.setUploadBy(2L);
    limited.setFilePath("secret.pdf");
    limited.setFileName("secret.pdf");
    limited.setVisibleDepts("3,7");

    List<DriveObject> objects = List.of(
        new DriveObject("open.txt", 1L, java.time.Instant.now()),
        new DriveObject("secret.pdf", 2L, java.time.Instant.now()));
    java.util.Map<String, EsFile> rows = java.util.Map.of("secret.pdf", limited);

    // 部门 3（命中）可见两行；部门 8 只见 open.txt；上传者本人恒可见
    assertEquals(2, service.filterSharedVisible(objects, rows, 3L, 9L).size());
    assertEquals(1, service.filterSharedVisible(objects, rows, 8L, 9L).size());
    assertEquals(2, service.filterSharedVisible(objects, rows, 8L, 2L).size());
}

// ── 回收站 + 版本（P1）─────────────────────────────────────

@Test
void markRecycledSnapshotsOriginPath() {
    EsFile active = row(7L, "docs/x.pdf");
    active.setState(EsFile.STATE_ACTIVE);
    when(mapper.selectOne(any())).thenReturn(active);

    EsFile result = service.markRecycled(EsFile.TYPE_PERSONAL, 1L, "docs/x.pdf");

    assertEquals(EsFile.STATE_RECYCLED, result.getState());
    assertEquals("docs/x.pdf", result.getOriginPath());
    assertEquals("docs/x.pdf", result.getFilePath());
    org.junit.jupiter.api.Assertions.assertNotNull(result.getDeletedAt());
    // 两次 update：先把存量 NULL state 落成 active，再置 recycled（部分索引不收 NULL）
    verify(mapper, org.mockito.Mockito.times(2)).updateById(active);
}

@Test
void markRecycledMissingRowReturnsNull() {
    when(mapper.selectOne(any())).thenReturn(null);
    assertNull(service.markRecycled(EsFile.TYPE_PERSONAL, 1L, "ghost.txt"));
    verify(mapper, never()).updateById(any(EsFile.class));
}

@Test
void restoreResetsToOrigin() {
    EsFile recycled = row(7L, "docs/x.pdf");
    recycled.setState(EsFile.STATE_RECYCLED);
    recycled.setOriginPath("docs/x.pdf");
    recycled.setDeletedAt(java.time.LocalDateTime.now());
    when(mapper.selectById(7L)).thenReturn(recycled);
    when(mapper.selectOne(any())).thenReturn(null); // 原位置无占用

    EsFile restored = service.restoreFromRecycle(EsFile.TYPE_PERSONAL, 1L, 7L);

    assertEquals(EsFile.STATE_ACTIVE, restored.getState());
    assertEquals("docs/x.pdf", restored.getFilePath());
    assertNull(restored.getDeletedAt());
    assertNull(restored.getOriginPath());

    // 实现用的是 update(entity=null, LambdaUpdateWrapper) 而不是 updateById：
    // MyBatis-Plus 默认忽略 null 字段，setDeletedAt(null)/setOriginPath(null)
    // 不会落库，必须用 UpdateWrapper 显式 SET 列 = NULL。见 DriveFileService 该处注释。
    //
    // ⚠️ 本断言原先写的是 verify(mapper).updateById(restored)，与实现不符——
    // 因为测试从未被真正执行（KI-8），所以这个不一致一直没被发现。
    verify(mapper).update(eq(null), any(LambdaUpdateWrapper.class));
    verify(mapper, never()).updateById(any(EsFile.class));
}

@Test
void restoreConflictWhenActiveOccupiesOrigin() {
    EsFile recycled = row(7L, "docs/x.pdf");
    recycled.setState(EsFile.STATE_RECYCLED);
    recycled.setOriginPath("docs/x.pdf");
    when(mapper.selectById(7L)).thenReturn(recycled);
    when(mapper.selectOne(any())).thenReturn(row(9L, "docs/x.pdf")); // 同名 active 占位

    ServiceException ex = assertThrows(ServiceException.class,
        () -> service.restoreFromRecycle(EsFile.TYPE_PERSONAL, 1L, 7L));
    assertEquals("原位置已有同名文件，请先处理现有文件后再还原", ex.getMessage());
    verify(mapper, never()).updateById(any(EsFile.class));
}

@Test
void restoreRejectsNonRecycledOrForeignRow() {
    EsFile active = row(7L, "a.txt");
    active.setState(EsFile.STATE_ACTIVE);
    when(mapper.selectById(7L)).thenReturn(active);
    assertThrows(ServiceException.class,
        () -> service.restoreFromRecycle(EsFile.TYPE_PERSONAL, 1L, 7L));

    EsFile foreign = row(8L, "b.txt");
    foreign.setState(EsFile.STATE_RECYCLED);
    foreign.setOwnerId(2L);
    when(mapper.selectById(8L)).thenReturn(foreign);
    assertThrows(ServiceException.class,
        () -> service.restoreFromRecycle(EsFile.TYPE_PERSONAL, 1L, 8L));
}

@Test
void purgeSkipsObjectDeletionWhenPathIsReoccupied() {
    // KI-7 回归：**删完又重新上传同名文件**时，彻底删除回收站旧行
    // 绝不能删对象——那个对象键现在装的是新上传的内容。
    //
    // 对象键由路径派生（DriveKeys.keyAt），所以「同路径有 active 行」
    // 就等于「那个键归别人了」。本用例断言服务层给出的信号是 deletable=false，
    // 控制器据此跳过 storage.deleteAt。
    EsFile recycled = row(7L, "a.txt");
    recycled.setState(EsFile.STATE_RECYCLED);
    recycled.setOriginPath("a.txt");
    when(mapper.selectById(7L)).thenReturn(recycled);
    // 同路径还存在别的行（用户删完又传了新的）—— exists 返回 true
    when(mapper.exists(any())).thenReturn(true);

    DriveFileService.PurgeResult purged = service.purgeFromRecycle(EsFile.TYPE_PERSONAL, 1L, 7L);

    // 行仍被摘除（用户清理回收站的意图要达成）
    verify(mapper).deleteById(7L);
    // **但明确告知调用方：对象不能删**（它属于同路径的新文件）
    assertFalse(purged.deletable(),
        "路径已被新文件占用时，必须禁止删对象——否则会删掉用户当前的数据");
}

@Test
void purgeRefusesDeleteWhenAnotherRecycledRowSharesPath() {
    // KI-7 的**第二个洞**：只查 active 是不够的。
    //
    // 唯一索引只约束 active（部分索引 WHERE state='active'），所以
    // 「两条 recycled 行同路径」是合法状态：
    //   传 a.txt → 删 → 重传 → 再删，此刻两条行都叫 a.txt 且都是 recycled。
    //
    // 此时删掉第一条并把对象删了，第二条还在指着那个键 ——
    // 用户对它点「还原」会得到一个对象已不存在的幽灵文件。
    //
    // 本用例断言：只要还有**任何**别的行（含 recycled）引用该路径，
    // 就不许删对象。
    EsFile first = row(7L, "a.txt");
    first.setState(EsFile.STATE_RECYCLED);
    when(mapper.selectById(7L)).thenReturn(first);
    when(mapper.exists(any())).thenReturn(true); // 另一条 recycled 行还在

    DriveFileService.PurgeResult purged = service.purgeFromRecycle(EsFile.TYPE_PERSONAL, 1L, 7L);

    assertFalse(purged.deletable(),
        "还有别的行（哪怕也是 recycled）指向同一路径时，不能删对象");
    verify(mapper).deleteById(7L);
}

@Test
void purgeReleasesObjectWhenLastReferenceGoes() {
    // 承接上一条：两条 recycled 行同路径时，第一条不许删对象；
    // **第二条（最后一条引用）必须允许删**——否则对象永久泄漏，回收站永不释放空间。
    //
    // 语义等价于引用计数：键由多少行指着就有多少债主，最后一个走了键才可释放。
    //
    // ① 第一条 purge：exists 说"还有别人" → 不删对象
    EsFile first = row(7L, "a.txt");
    first.setState(EsFile.STATE_RECYCLED);
    when(mapper.selectById(7L)).thenReturn(first);
    when(mapper.exists(any())).thenReturn(true);

    DriveFileService.PurgeResult p1 = service.purgeFromRecycle(EsFile.TYPE_PERSONAL, 1L, 7L);
    assertFalse(p1.deletable(), "还有别的行引用该键时不能删对象");

    // ② 第二条 purge：此时只剩它自己 → 允许删
    EsFile second = row(8L, "a.txt");
    second.setState(EsFile.STATE_RECYCLED);
    when(mapper.selectById(8L)).thenReturn(second);
    when(mapper.exists(any())).thenReturn(false);

    DriveFileService.PurgeResult p2 = service.purgeFromRecycle(EsFile.TYPE_PERSONAL, 1L, 8L);
    assertTrue(p2.deletable(), "最后一条引用消失时必须允许删对象，否则空间永不释放");

    verify(mapper).deleteById(7L);
    verify(mapper).deleteById(8L);
}

@Test
void purgeAllowsObjectDeletionWhenPathIsFree() {
    // 正常路径：删完没有重新上传，对象的键仍属于这条回收站行 → 可安全删除
    EsFile recycled = row(7L, "solo.txt");
    recycled.setState(EsFile.STATE_RECYCLED);
    when(mapper.selectById(7L)).thenReturn(recycled);
    when(mapper.exists(any())).thenReturn(false); // 无别的行引用该路径

    DriveFileService.PurgeResult purged = service.purgeFromRecycle(EsFile.TYPE_PERSONAL, 1L, 7L);

    assertTrue(purged.deletable(), "路径无占用时应当允许删对象（否则回收站永不释放空间）");
    verify(mapper).deleteById(7L);
}

@Test
void purgeDeletesRecycledRow() {
    EsFile recycled = row(7L, "docs/x.pdf");
    recycled.setState(EsFile.STATE_RECYCLED);
    when(mapper.selectById(7L)).thenReturn(recycled);

    DriveFileService.PurgeResult purged = service.purgeFromRecycle(EsFile.TYPE_PERSONAL, 1L, 7L);

    assertEquals("docs/x.pdf", purged.row().getFilePath());
    // 无同路径 active 行 → 可以安全删对象
    assertTrue(purged.deletable(), "路径无占用时应允许删对象");
    verify(mapper).deleteById(7L);
}

@Test
void purgeRejectsActiveRow() {
    // active 行必须先经软删，不能绕过回收站直删
    EsFile active = row(7L, "a.txt");
    active.setState(EsFile.STATE_ACTIVE);
    when(mapper.selectById(7L)).thenReturn(active);

    assertThrows(ServiceException.class,
        () -> service.purgeFromRecycle(EsFile.TYPE_PERSONAL, 1L, 7L));
    verify(mapper, never()).deleteById(any(Long.class));
}


// ── 列表可见性：软删必须真的从列表消失（KI-10）──────────────────
//
// 背景：软删只改目录行、不动对象（回收站与在用文件共用同一个对象键）。因此
// **存储列举结果里仍然有这条对象**——列表若不按 state 过滤，
// 「删除」在用户眼里等于没发生，而且列表回填会为它新建一条 active 行：
//   · 回收站还原 → 被列表自己制造的同名 active 行挡住，永远报冲突；
//   · 彻底删除 → 引用计数认为「还有别的行指着这个键」，对象永不释放。
// 即：漏掉一个读侧过滤，会把回收站整条链路污染成不可用。

@Test
void reconcileDoesNotResurrectSoftDeletedPath() {
    EsFile active = row(7L, "keep.txt");
    active.setState(EsFile.STATE_ACTIVE);
    EsFile recycled = row(8L, "gone.txt");
    recycled.setState(EsFile.STATE_RECYCLED);
    when(mapper.selectList(any())).thenReturn(List.of(active, recycled));

    // 走生产唯一入口：存储列举里两条对象都在（软删不动 RustFS 对象）
    DriveFileService.Listing listing = service.list(EsFile.TYPE_PERSONAL, 1L, 1L, List.of(
        new DriveObject("keep.txt", 1L, Instant.now()),
        new DriveObject("gone.txt", 2L, Instant.now())));

    assertEquals(1, listing.objects().size(), "回收站里的对象不得出现在列表里");
    assertEquals("keep.txt", listing.objects().get(0).path());
    assertEquals(7L, listing.fileIds().get("keep.txt"));
    // 关键：**绝不 insert**。insert 一条 active 行就等于列表自己把删除撤销了，
    // 并连带堵死还原（原位置被这行占住）与彻底删除（引用计数认为还有债主）。
    verify(mapper, never()).insert(any(EsFile.class));
}

@Test
void listViewHidesDeletedPathsButKeepsActiveOnes() {
    EsFile active = row(7L, "keep.txt");
    active.setState(EsFile.STATE_ACTIVE);
    EsFile recycled = row(8L, "gone.txt");
    recycled.setState(EsFile.STATE_RECYCLED);
    when(mapper.selectList(any())).thenReturn(List.of(active, recycled));

    DriveFileService.Listing listing = service.list(EsFile.TYPE_PERSONAL, 1L, 1L, List.of(
        new DriveObject("keep.txt", 1L, Instant.now()),
        new DriveObject("gone.txt", 2L, Instant.now())));

    assertEquals(List.of("keep.txt"), listing.objects().stream().map(DriveObject::path).toList());
    // 藏掉的行不进 active 索引：否则共享空间可见性过滤会拿它当在用文件
    assertFalse(listing.activeByPath().containsKey("gone.txt"));
    assertEquals(7L, listing.activeByPath().get("keep.txt").getFileId());
    // 藏掉的行也不回填 fileId（保持与"历史脏数据"一致的可空语义）
    assertNull(listing.fileIds().get("gone.txt"));
}

@Test
void listViewKeepsPathVisibleWhenActiveRowSharesItWithRecycle() {
    // 「删了又重新上传同名文件」：两条行同路径，对象键此刻属于 active 那份。
    // 列表必须照常显示它——不能因为回收站里也有同路径就把它一起藏掉。
    EsFile active = row(9L, "a.txt");
    active.setState(EsFile.STATE_ACTIVE);
    EsFile recycled = row(7L, "a.txt");
    recycled.setState(EsFile.STATE_RECYCLED);
    when(mapper.selectList(any())).thenReturn(List.of(recycled, active));

    // 走生产唯一入口：对象键此刻属于重新上传的那份，必须照常显示
    DriveFileService.Listing listing = service.list(EsFile.TYPE_PERSONAL, 1L, 1L,
        List.of(new DriveObject("a.txt", 3L, Instant.now())));

    assertEquals(1, listing.objects().size(), "同路径有 active 行时不得被回收站行藏掉");
    assertEquals(9L, listing.fileIds().get("a.txt"), "回填必须命中 active 行的 fileId");
    assertEquals(9L, listing.activeByPath().get("a.txt").getFileId());
    verify(mapper, never()).insert(any(EsFile.class));
}

@Test
void listViewTreatsNullStateAsActive() {
    // 列上线前的存量行 state 为 NULL：必须按 active 处理，否则升级后整盘文件集体消失
    EsFile legacy = row(7L, "old.txt");
    legacy.setState(null);
    when(mapper.selectList(any())).thenReturn(List.of(legacy));

    DriveFileService.Listing listing = service.list(EsFile.TYPE_PERSONAL, 1L, 1L,
        List.of(new DriveObject("old.txt", 1L, Instant.now())));

    assertEquals(1, listing.objects().size(), "存量 NULL 行必须可见");
    assertEquals(7L, listing.fileIds().get("old.txt"));
    verify(mapper, never()).insert(any(EsFile.class));
}

@Test
void listViewHidesUnknownStatesByDefault() {
    // 白名单口径：只有 active / NULL 算在用，其余状态默认不可见。
    // 这样将来新增状态（这里是 purged）会自动落到安全的一侧，
    // 不必等有人记得回来改这个 if。
    EsFile active = row(7L, "keep.txt");
    active.setState(EsFile.STATE_ACTIVE);
    EsFile purged = row(8L, "purged.txt");
    purged.setState(EsFile.STATE_PURGED);
    EsFile weird = row(9L, "weird.txt");
    weird.setState("some_future_state");
    when(mapper.selectList(any())).thenReturn(List.of(active, purged, weird));

    DriveFileService.Listing listing = service.list(EsFile.TYPE_PERSONAL, 1L, 1L, List.of(
        new DriveObject("keep.txt", 1L, Instant.now()),
        new DriveObject("purged.txt", 2L, Instant.now()),
        new DriveObject("weird.txt", 3L, Instant.now())));

    assertEquals(1, listing.objects().size(), "只有 active 行对用户可见");
    assertEquals("keep.txt", listing.objects().get(0).path());
    assertFalse(listing.activeByPath().containsKey("purged.txt"));
    assertFalse(listing.activeByPath().containsKey("weird.txt"));
    assertNull(listing.fileIds().get("purged.txt"), "藏掉的行不回填 fileId");
    assertNull(listing.fileIds().get("weird.txt"), "藏掉的行不回填 fileId");
    verify(mapper, never()).insert(any(EsFile.class));
}

// ── 下载授权：列表过滤 ≠ 拿不到（同一个列表口径必须也管下载）────────

@Test
void assertDownloadableRejectsSoftDeletedPath() {
    // 软删只改目录层、对象仍留在 RustFS：下载接口若不查 state，
    // 「删掉」的文件照样能凭路径签出 URL 拿走。
    // 点查语义：①没有 active 行（selectOne 返回 null）
    //        ②但该路径存在任意行（exists=true，即回收站行）→ 判为已删除
    when(mapper.selectOne(any())).thenReturn(null);
    when(mapper.exists(any())).thenReturn(true);

    ServiceException ex = assertThrows(ServiceException.class, () -> service.assertDownloadable(
        EsFile.TYPE_PERSONAL, 1L, "gone.txt", null, 1L));
    assertEquals("文件不存在或已删除", ex.getMessage());
}

@Test
void assertDownloadableAllowsUnregisteredLegacyObject() {
    // 目录层没有行（存量对象在惰性补账之前）：不拦。
    // 否则升级后那批还没被列举过的存量文件会集体下载不了——
    // 这与列表「未登记按可见处理」是同一口径。
    when(mapper.selectOne(any())).thenReturn(null);
    when(mapper.exists(any())).thenReturn(false);

    service.assertDownloadable(EsFile.TYPE_PERSONAL, 1L, "legacy.txt", null, 1L);
}

@Test
void assertDownloadableUsesPointQueriesNotFullSpaceScan() {
    // 下载是热路径：每开一个文件、每次 Seek 回退重开流都会再签一次
    // （internal/spacedav 的 readFile.reopen），拖一次视频进度条就是几十次。
    // 若这里扫全空间，代价随空间文件数线性增长——必须走点查。
    EsFile active = row(7L, "keep.txt");
    active.setState(EsFile.STATE_ACTIVE);
    when(mapper.selectOne(any())).thenReturn(active);

    service.assertDownloadable(EsFile.TYPE_PERSONAL, 1L, "keep.txt", null, 1L);

    verify(mapper, never()).selectList(any());
    verify(mapper, never()).exists(any()); // 命中 active 后无需第二次点查
}

@Test
void assertDownloadableAllowsActivePath() {
    EsFile active = row(7L, "keep.txt");
    active.setState(EsFile.STATE_ACTIVE);
    when(mapper.selectList(any())).thenReturn(List.of(active));

    service.assertDownloadable(EsFile.TYPE_PERSONAL, 1L, "keep.txt", null, 1L);
}

@Test
void assertDownloadableRejectsSharedFileHiddenFromViewerDept() {
    // 共享空间文档级可见性：visible_depts 只放开部门 3、7。
    // 部门 8 的用户即便知道路径，也不能签出下载 URL。
    EsFile limited = new EsFile();
    limited.setFileId(31L);
    limited.setSpaceType(EsFile.TYPE_SHARED);
    limited.setOwnerId(0L);
    limited.setUploadBy(2L);
    limited.setFilePath("secret.pdf");
    limited.setFileName("secret.pdf");
    limited.setVisibleDepts("3,7");
    limited.setState(EsFile.STATE_ACTIVE);
    when(mapper.selectOne(any())).thenReturn(limited);

    // 部门 8、非上传者 → 拒绝
    assertThrows(ServiceException.class, () -> service.assertDownloadable(
        EsFile.TYPE_SHARED, 0L, "secret.pdf", 8L, 9L));

    // 部门 3 命中 → 放行
    service.assertDownloadable(EsFile.TYPE_SHARED, 0L, "secret.pdf", 3L, 9L);
    // 上传者本人恒可见 → 放行
    service.assertDownloadable(EsFile.TYPE_SHARED, 0L, "secret.pdf", 8L, 2L);
}

@Test
void assertDownloadableAllowsUnrestrictedSharedFile() {
    EsFile open = new EsFile();
    open.setFileId(30L);
    open.setSpaceType(EsFile.TYPE_SHARED);
    open.setOwnerId(0L);
    open.setUploadBy(2L);
    open.setFilePath("open.txt");
    open.setFileName("open.txt");
    open.setVisibleDepts("");
    open.setState(EsFile.STATE_ACTIVE);
    when(mapper.selectOne(any())).thenReturn(open);

    service.assertDownloadable(EsFile.TYPE_SHARED, 0L, "open.txt", 8L, 9L);
}

// ── 改名 / 移动（切片 4）：身份不变、目标冲突不覆盖 ────────────

@Test
void moveRegisteredKeepsFileIdAndUpdatesPath() {
    // 改名的核心承诺：**fileId 不变**。分享链接、任务引用、知识索引绑的都是它，
    // 一旦换号，那些引用全断（旧实现「上传新键 + 删旧键」正是如此）。
    EsFile row = row(7L, "docs/old.pdf");
    row.setState(EsFile.STATE_ACTIVE);
    when(mapper.selectOne(any())).thenReturn(row, (EsFile) null); // 源命中、目标空

    EsFile moved = service.moveRegistered(EsFile.TYPE_PERSONAL, 1L, "docs/old.pdf", "docs/new.pdf");

    assertEquals(7L, moved.getFileId(), "改名后 fileId 必须不变");
    assertEquals("docs/new.pdf", moved.getFilePath());
    assertEquals("new.pdf", moved.getFileName(), "文件名列要跟着路径更新");
    verify(mapper).updateById(row);
}

@Test
void moveRegisteredRefusesOccupiedTarget() {
    // 目标已有 active 行：拒绝，不覆盖（与还原冲突同一取向，破坏性选择交还用户）
    EsFile source = row(7L, "a.txt");
    source.setState(EsFile.STATE_ACTIVE);
    EsFile occupied = row(9L, "b.txt");
    occupied.setState(EsFile.STATE_ACTIVE);
    when(mapper.selectOne(any())).thenReturn(source, occupied);

    ServiceException ex = assertThrows(ServiceException.class, () ->
        service.moveRegistered(EsFile.TYPE_PERSONAL, 1L, "a.txt", "b.txt"));
    assertEquals("目标位置已有同名文件，未覆盖", ex.getMessage());
    verify(mapper, never()).updateById(any(EsFile.class));
}

@Test
void moveRegisteredReturnsNullForUnregisteredPath() {
    // 历史脏数据/未登记：没有行可改，返回 null 让调用方照常走对象层移动
    when(mapper.selectOne(any())).thenReturn(null);

    assertNull(service.moveRegistered(EsFile.TYPE_PERSONAL, 1L, "ghost.txt", "moved.txt"));
    verify(mapper, never()).updateById(any(EsFile.class));
}

}
