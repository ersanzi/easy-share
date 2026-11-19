package org.dromara.easyshare.drive;

import com.baomidou.mybatisplus.core.MybatisConfiguration;
import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.baomidou.mybatisplus.core.metadata.TableInfoHelper;
import org.apache.ibatis.builder.MapperBuilderAssistant;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.domain.EsFileChangeLog;
import org.dromara.easyshare.drive.domain.EsRecycleBatch;
import org.dromara.easyshare.drive.mapper.EsFileMapper;
import org.dromara.easyshare.drive.mapper.EsRecycleBatchMapper;
import org.dromara.easyshare.drive.service.DriveChangeLogService;
import org.dromara.easyshare.drive.service.DriveFileService;
import org.dromara.easyshare.drive.service.DriveRecycleService;
import org.dromara.easyshare.drive.service.FileRoleService;
import org.dromara.easyshare.drive.service.RecycleBatchLedger;
import org.dromara.easyshare.drive.service.SpaceUsageService;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;
import org.mockito.InOrder;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

/** 独立回收正文的失败关闭、稳定身份、批次顺序与空间边界；不启动 Spring/存储。 */
class DriveRecycleServiceTest {
    private static final String SPACE = EsFile.TYPE_PERSONAL;
    private static final Long OWNER = 1L;
    private static final String PREFIX = DriveKeys.userPrefix(OWNER);
    private static final String OPERATION = "delete-op-1";
    private static final byte[] BODY = "body-v3-data".getBytes(java.nio.charset.StandardCharsets.UTF_8);
    private static final byte[] OTHER_BODY = "body-v4-data".getBytes(java.nio.charset.StandardCharsets.UTF_8);
    private static final String CONTENT_SHA = sha256(BODY);
    private static final String OTHER_SHA = sha256(OTHER_BODY);
    private static final DriveStorage.ObjectMetadata CONTENT = new DriveStorage.ObjectMetadata(BODY.length, "etag-v3");

    private EsFileMapper files;
    private EsRecycleBatchMapper batches;
    private DriveFileService fileService;
    private DriveStorage storage;
    private DriveChangeLogService changes;
    private FileRoleService roles;
    private SpaceUsageService usage;
    private RecycleBatchLedger ledger;
    private RecycleBatchLedger codec;
    private tools.jackson.databind.json.JsonMapper json;
    private Map<String, EsRecycleBatch> savedBatches;
    private List<EsRecycleBatch> checkpoints;
    private DriveRecycleService service;

    @BeforeAll
    static void initMybatisPlusMetadata() {
        // 与 DriveFileServiceTest 一致：纯 Mockito 环境也需要两个实体的 lambda 列名缓存。
        MybatisConfiguration configuration = new MybatisConfiguration();
        MapperBuilderAssistant assistant = new MapperBuilderAssistant(configuration, "");
        TableInfoHelper.initTableInfo(assistant, EsFile.class);
        TableInfoHelper.initTableInfo(assistant, EsRecycleBatch.class);
    }

    @BeforeEach
    void setUp() {
        files = mock(EsFileMapper.class);
        batches = mock(EsRecycleBatchMapper.class);
        fileService = mock(DriveFileService.class);
        storage = mock(DriveStorage.class);
        changes = mock(DriveChangeLogService.class);
        roles = mock(FileRoleService.class);
        usage = mock(SpaceUsageService.class);
        ledger = mock(RecycleBatchLedger.class);
        json = tools.jackson.databind.json.JsonMapper.builder().findAndAddModules().build();
        codec = new RecycleBatchLedger(batches, json);
        savedBatches = new java.util.HashMap<>();
        checkpoints = new ArrayList<>();
        when(ledger.encodeMembers(anyList())).thenAnswer(invocation -> codec.encodeMembers(invocation.getArgument(0)));
        when(ledger.decodeMembers(nullable(String.class)))
            .thenAnswer(invocation -> codec.decodeMembers(invocation.getArgument(0)));
        // REQUIRES_NEW 的持久操作账用深拷贝模拟，不能保存随后被 service 改写的对象引用。
        when(batches.insert(any(EsRecycleBatch.class))).thenAnswer(invocation -> {
            EsRecycleBatch batch = invocation.getArgument(0);
            assertFalse(savedBatches.containsKey(batch.getBatchId()));
            savedBatches.put(batch.getBatchId(), copyBatch(batch));
            return 1;
        });
        when(batches.updateById(any(EsRecycleBatch.class))).thenAnswer(invocation -> {
            EsRecycleBatch batch = invocation.getArgument(0);
            savedBatches.put(batch.getBatchId(), copyBatch(batch));
            return 1;
        });
        when(batches.selectById(anyString())).thenAnswer(invocation -> copyBatch(savedBatches.get(invocation.getArgument(0))));
        when(batches.selectOne(any())).thenAnswer(invocation -> {
            LambdaQueryWrapper<?> query = invocation.getArgument(0);
            query.getSqlSegment();
            Map<String, Object> parameters = query.getParamNameValuePairs();
            return savedBatches.values().stream().filter(batch -> parameters.containsValue(batch.getSpaceType())
                && parameters.containsValue(batch.getOwnerId()) && parameters.containsValue(batch.getOperatorId())
                && parameters.containsValue(batch.getOperationId())).findFirst().map(this::copyBatch).orElse(null);
        });
        when(ledger.reserve(any(EsRecycleBatch.class))).thenAnswer(invocation -> {
            EsRecycleBatch batch = invocation.getArgument(0);
            assertEquals(1, batches.insert(batch));
            return batch;
        });
        doAnswer(invocation -> {
            EsRecycleBatch batch = invocation.getArgument(0);
            assertEquals(1, batches.updateById(batch));
            checkpoints.add(copyBatch(batch));
            return null;
        }).when(ledger).checkpoint(any(EsRecycleBatch.class));
        service = new DriveRecycleService(files, batches, fileService, storage, changes, roles, usage, ledger);
        when(files.updateById(any(EsFile.class))).thenReturn(1);
        when(files.update(any(), any())).thenReturn(1);
        when(storage.sha256At(eq(PREFIX), anyString())).thenAnswer(invocation -> sha256(BODY));
    }

    @Test
    void recycleKeepsFileIdAndOnlySoftDeletesAfterCopyAndBothHeadChecks() {
        EsFile file = row(7L, "docs/a.txt", false);
        prepareDelete(file);
        when(storage.metadataAt(eq(PREFIX), anyString())).thenReturn(CONTENT);
        doAnswer(invocation -> {
            assertEquals(EsFile.STATE_ACTIVE, file.getState(), "copy 时不能已经软删");
            verify(files, never()).updateById(any(EsFile.class));
            return null;
        }).when(storage).copyAt(eq(PREFIX), eq(file.getFilePath()), anyString());

        DriveRecycleService.Result result = service.recycle(SPACE, OWNER, OWNER, 7L,
            "docs/a.txt", 3L, OPERATION, false);
        String snapshot = DriveRecycleKeys.storagePathOf(result.batchId(), 7L, 3L);
        InOrder order = inOrder(storage, files, batches, changes, ledger, fileService);
        order.verify(batches).lockSpace(SPACE, OWNER);
        order.verify(batches).selectOne(any());
        order.verify(fileService).resolveDeletePath(SPACE, OWNER, 7L, "docs/a.txt");
        order.verify(fileService).lockActiveForPresign(SPACE, OWNER, "docs/a.txt");
        order.verify(ledger).reserve(any(EsRecycleBatch.class));
        order.verify(batches).insert(any(EsRecycleBatch.class));
        order.verify(storage).metadataAt(PREFIX, "docs/a.txt");
        order.verify(storage).sha256At(PREFIX, "docs/a.txt");
        order.verify(ledger).checkpoint(any(EsRecycleBatch.class));
        order.verify(batches).updateById(any(EsRecycleBatch.class));
        order.verify(storage).copyAt(PREFIX, "docs/a.txt", snapshot);
        order.verify(storage).metadataAt(PREFIX, snapshot);
        order.verify(storage).metadataAt(PREFIX, "docs/a.txt");
        order.verify(storage).sha256At(PREFIX, snapshot);
        order.verify(storage).sha256At(PREFIX, "docs/a.txt");
        order.verify(ledger).checkpoint(any(EsRecycleBatch.class));
        order.verify(batches).updateById(any(EsRecycleBatch.class));
        order.verify(files).updateById(file);
        order.verify(changes).record(SPACE, OWNER, EsFileChangeLog.EVENT_DELETED,
            file, 7L, "docs/a.txt", null);
        order.verify(batches).updateById(any(EsRecycleBatch.class));
        assertEquals("7", result.fileId());
        assertEquals("recycled", result.status());
        assertEquals(1, result.memberCount());
        assertEquals(7L, file.getFileId());
        assertEquals(3L, file.getVersionId());
        assertEquals("docs/a.txt", file.getFilePath());
        assertEquals("docs/a.txt", file.getOriginPath());
        assertEquals(EsFile.STATE_RECYCLED, file.getState());
        assertEquals(snapshot, file.getRecycleStoragePath());
        assertEquals(3L, file.getRecycleSnapshotVersion());
        assertEquals(12L, file.getRecycleSnapshotSize());
        assertEquals("etag-v3", file.getRecycleSnapshotEtag());
        assertEquals("ready", file.getRecycleSnapshotState());
        assertTrue(file.getRecycleRoot());
        assertNotNull(file.getDeletedAt());
        assertEquals(2, checkpoints.size());
        EsFile beforeCopy = codec.decodeMembers(checkpoints.getFirst().getMembersJson()).getFirst();
        assertEquals(snapshot, beforeCopy.getRecycleStoragePath());
        assertEquals(CONTENT_SHA, beforeCopy.getRecycleSnapshotSha256());
        assertNull(beforeCopy.getRecycleSnapshotState(), "copy 前独立保存内容身份，不可假称 ready");
        EsRecycleBatch checkpoint = checkpoints.getLast();
        assertEquals("preparing", checkpoint.getStatus());
        EsFile frozen = codec.decodeMembers(checkpoint.getMembersJson()).getFirst();
        assertEquals(7L, frozen.getFileId());
        assertEquals(EsFile.STATE_ACTIVE, frozen.getState(), "独立操作账 checkpoint 必须早于目录状态改写");
        assertEquals(snapshot, frozen.getRecycleStoragePath());
        assertEquals("ready", frozen.getRecycleSnapshotState());
        assertEquals(CONTENT.etag(), frozen.getRecycleSnapshotEtag());
        assertEquals(CONTENT.size(), frozen.getRecycleSnapshotSize());
        assertEquals(CONTENT_SHA, frozen.getRecycleSnapshotSha256());
        assertEquals(CONTENT_SHA, file.getRecycleSnapshotSha256());
        assertEquals("recycled", savedBatches.get(result.batchId()).getStatus());
        verify(files, never()).insert(any(EsFile.class));
        verify(files, never()).deleteById(any(java.io.Serializable.class));
        verify(storage, never()).deleteAt(anyString(), anyString());
        verify(usage).invalidate(PREFIX);
    }

    @Test
    void missingBodyCopyFailureOrHeadMismatchNeverAuthorizesSoftDelete() {
        // 源前后 ETag 改变仍必须失败；跨对象 ETag 不同不等于正文不同，另测 size/SHA。
        for (String failure : List.of("missing", "size", "etag", "copy", "saved", "source")) {
            setUp();
            EsFile file = row(7L, "docs/a.txt", false);
            prepareDelete(file);
            DriveStorage.ObjectMetadata changed = new DriveStorage.ObjectMetadata(12L, "other-etag");
            when(storage.metadataAt(eq(PREFIX), anyString())).thenAnswer(invocation ->
                "saved".equals(failure) ? new DriveStorage.ObjectMetadata(13L, "other-etag") : CONTENT);
            when(storage.metadataAt(PREFIX, file.getFilePath())).thenReturn(CONTENT);
            if ("missing".equals(failure)) {
                when(storage.metadataAt(PREFIX, file.getFilePath())).thenReturn(null);
            } else if ("size".equals(failure)) {
                when(storage.metadataAt(PREFIX, file.getFilePath()))
                    .thenReturn(new DriveStorage.ObjectMetadata(13L, "etag-v3"));
            } else if ("etag".equals(failure)) {
                when(storage.metadataAt(PREFIX, file.getFilePath()))
                    .thenReturn(new DriveStorage.ObjectMetadata(12L, null));
            } else if ("source".equals(failure)) {
                when(storage.metadataAt(PREFIX, file.getFilePath())).thenReturn(CONTENT, changed);
            } else if ("copy".equals(failure)) {
                doThrow(new ServiceException("copy failed")).when(storage)
                    .copyAt(eq(PREFIX), eq(file.getFilePath()), anyString());
            }
            assertThrows(ServiceException.class, () -> service.recycle(SPACE, OWNER, OWNER,
                7L, file.getFilePath(), 3L, OPERATION, false), failure);
            assertEquals(EsFile.STATE_ACTIVE, file.getState(), failure);
            assertNull(file.getDeletedAt(), failure);
            verify(files, never()).updateById(any(EsFile.class));
            verify(files, never()).update(any(), any());
            boolean rejectedBeforeHash = List.of("missing", "size", "etag").contains(failure);
            int checkpointCount = rejectedBeforeHash ? 0 : 1;
            verify(batches, times(checkpointCount)).updateById(any(EsRecycleBatch.class));
            verify(ledger).reserve(any(EsRecycleBatch.class));
            verify(ledger, times(checkpointCount)).checkpoint(any(EsRecycleBatch.class));
            assertEquals(1, savedBatches.size(), failure);
            EsRecycleBatch persisted = savedBatches.values().iterator().next();
            assertEquals("preparing", persisted.getStatus(), failure);
            EsFile frozen = codec.decodeMembers(persisted.getMembersJson()).getFirst();
            assertEquals(7L, frozen.getFileId(), failure);
            if (!rejectedBeforeHash) {
                assertEquals(CONTENT_SHA, frozen.getRecycleSnapshotSha256(), failure);
                assertEquals(DriveRecycleKeys.storagePathOf(persisted.getBatchId(), 7L, 3L), frozen.getRecycleStoragePath(), failure);
                assertNull(frozen.getRecycleSnapshotState(), failure);
            }
            verify(storage, never()).deleteAt(anyString(), anyString());
            verifyNoInteractions(changes, usage);
            if (List.of("missing", "size", "etag").contains(failure)) {
                verify(storage, never()).copyAt(anyString(), anyString(), anyString());
            }
        }
    }

    @Test
    void staleExpectedVersionRejectsBeforeCreatingBatchOrCopying() {
        EsFile file = row(7L, "docs/a.txt", false);
        prepareDelete(file);
        assertThrows(ServiceException.class, () -> service.recycle(SPACE, OWNER, OWNER,
            7L, file.getFilePath(), 2L, OPERATION, false));
        assertEquals(EsFile.STATE_ACTIVE, file.getState());
        verify(batches, never()).insert(any(EsRecycleBatch.class));
        verify(files, never()).updateById(any(EsFile.class));
        verifyNoInteractions(storage, changes, usage, ledger);
    }

    @Test
    void repeatedOperationOnlyReturnsForTheSameIdentityVersionAndTarget() {
        EsRecycleBatch previous = batch("batch-1", 7L, "docs/a.txt", false, 1);
        doReturn(previous).when(batches).selectOne(any());
        DriveRecycleService.Result repeated = service.recycle(SPACE, OWNER, OWNER,
            7L, "docs\\a.txt", 3L, OPERATION, false);
        assertEquals("batch-1", repeated.batchId());
        assertEquals("7", repeated.fileId());
        assertEquals("recycled", repeated.status());
        assertThrows(ServiceException.class, () -> service.recycle(SPACE, OWNER, OWNER,
            8L, "docs/a.txt", 3L, OPERATION, false));
        assertThrows(ServiceException.class, () -> service.recycle(SPACE, OWNER, OWNER,
            7L, "docs/other.txt", 3L, OPERATION, false));
        assertThrows(ServiceException.class, () -> service.recycle(SPACE, OWNER, OWNER,
            7L, "docs/a.txt", 4L, OPERATION, false));
        assertEquals(repeated, service.operation(SPACE, OWNER, OWNER, OPERATION));
        var query = ArgumentCaptor.forClass(LambdaQueryWrapper.class);
        verify(batches, times(5)).selectOne(query.capture());
        for (Object captured : query.getAllValues()) {
            LambdaQueryWrapper<?> wrapper = (LambdaQueryWrapper<?>) captured;
            String sql = wrapper.getSqlSegment();
            assertTrue(sql.contains("space_type") && sql.contains("owner_id")
                && sql.contains("operator_id") && sql.contains("operation_id"), sql);
            assertTrue(wrapper.getParamNameValuePairs().containsValue(SPACE));
            assertTrue(wrapper.getParamNameValuePairs().containsValue(OWNER));
            assertTrue(wrapper.getParamNameValuePairs().containsValue(OPERATION));
        }
        verify(batches, never()).insert(any(EsRecycleBatch.class));
        verify(batches, never()).updateById(any(EsRecycleBatch.class));
        verifyNoInteractions(fileService, files, storage, changes, usage, ledger);
        verify(batches, times(4)).lockSpace(SPACE, OWNER);
    }

    @Test
    void recursiveBatchDeletesChildrenFirstAndRestoresParentsFirstWithoutNewIds() {
        EsFile root = row(7L, "docs", true);
        EsFile folder = row(8L, "docs/sub", true);
        EsFile leaf = row(9L, "docs/sub/a.txt", false);
        EsFile unrelated = row(10L, "docs-other/a.txt", false);
        EsFile oldDeletion = recycledRow(11L, "docs/old.txt", false, "old-batch", true);
        prepareDelete(root);
        when(files.selectList(any())).thenReturn(List.of(root, leaf, folder, unrelated, oldDeletion));
        Map<String, DriveStorage.ObjectMetadata> objects = new java.util.HashMap<>();
        objects.put(leaf.getFilePath(), CONTENT);
        when(storage.metadataAt(eq(PREFIX), anyString()))
            .thenAnswer(invocation -> objects.get(invocation.getArgument(1)));
        doAnswer(invocation -> {
            String from = invocation.getArgument(1);
            String to = invocation.getArgument(2);
            assertNotNull(objects.get(from));
            objects.put(to, objects.get(from));
            return null;
        }).when(storage).copyAt(eq(PREFIX), anyString(), anyString());
        when(files.updateById(any(EsFile.class))).thenAnswer(invocation -> {
            assertTrue(objects.containsKey(leaf.getRecycleStoragePath()), "所有正文须先核验才可软删任何成员");
            return 1;
        });

        assertThrows(ServiceException.class, () -> service.recycle(SPACE, OWNER, OWNER,
            7L, "docs", 3L, OPERATION, false));
        verifyNoInteractions(storage);
        verify(batches, never()).insert(any(EsRecycleBatch.class));
        DriveRecycleService.Result deleted = service.recycle(SPACE, OWNER, OWNER,
            7L, "docs", 3L, OPERATION, true);
        assertEquals(3, deleted.memberCount());
        assertTrue(deleted.directory());
        String snapshot = DriveRecycleKeys.storagePathOf(deleted.batchId(), 9L, 3L);
        InOrder order = inOrder(storage, files, changes);
        order.verify(storage).copyAt(PREFIX, leaf.getFilePath(), snapshot);
        order.verify(storage).metadataAt(PREFIX, snapshot);
        order.verify(storage).metadataAt(PREFIX, leaf.getFilePath());
        order.verify(storage).sha256At(PREFIX, snapshot);
        order.verify(storage).sha256At(PREFIX, leaf.getFilePath());
        order.verify(files).updateById(leaf);
        order.verify(files).updateById(folder);
        order.verify(files).updateById(root);
        for (EsFile member : List.of(root, folder, leaf)) {
            assertEquals(deleted.batchId(), member.getRecycleBatchId());
            assertEquals(OPERATION, member.getRecycleOperationId());
            assertEquals(EsFile.STATE_RECYCLED, member.getState());
        }
        assertTrue(root.getRecycleRoot());
        assertFalse(folder.getRecycleRoot());
        assertFalse(leaf.getRecycleRoot());
        assertNull(root.getRecycleStoragePath());
        assertNull(folder.getRecycleStoragePath());
        assertEquals(EsFile.STATE_ACTIVE, unrelated.getState());
        assertEquals("old-batch", oldDeletion.getRecycleBatchId());
        verify(files, never()).updateById(unrelated);
        verify(files, never()).updateById(oldDeletion);

        var capturedBatch = ArgumentCaptor.forClass(EsRecycleBatch.class);
        verify(batches).insert(capturedBatch.capture());
        when(files.selectById(7L)).thenReturn(root);
        assertEquals(deleted.batchId(), capturedBatch.getValue().getBatchId());
        when(files.selectList(any())).thenAnswer(invocation -> new ArrayList<>(List.of(leaf, folder, root)));
        objects.remove(leaf.getFilePath());
        DriveRecycleService.Result restored = service.restore(SPACE, OWNER, OWNER, 7L);
        assertEquals("restored", restored.status());
        assertEquals(deleted.batchId(), restored.batchId());
        assertEquals("7", restored.fileId());
        var updates = ArgumentCaptor.forClass(LambdaUpdateWrapper.class);
        order.verify(storage).copyAt(PREFIX, snapshot, leaf.getFilePath());
        order.verify(files).update(isNull(), updates.capture());
        order.verify(changes).record(SPACE, OWNER, EsFileChangeLog.EVENT_RESTORED, root, 7L, "docs", null);
        order.verify(files).update(isNull(), updates.capture());
        order.verify(changes).record(SPACE, OWNER, EsFileChangeLog.EVENT_RESTORED, folder, 8L, "docs/sub", null);
        order.verify(files).update(isNull(), updates.capture());
        order.verify(changes).record(SPACE, OWNER, EsFileChangeLog.EVENT_RESTORED, leaf, 9L, "docs/sub/a.txt", null);
        assertEquals(List.of(7L, 8L, 9L), updates.getAllValues().stream()
            .map(wrapper -> updateFileId((LambdaUpdateWrapper<?>) wrapper)).toList());
        assertEquals(List.of(7L, 8L, 9L), List.of(root.getFileId(), folder.getFileId(), leaf.getFileId()));
        assertTrue(List.of(root, folder, leaf).stream().allMatch(member -> EsFile.STATE_ACTIVE.equals(member.getState())));
        assertEquals(CONTENT, objects.get(leaf.getFilePath()));
        assertEquals(CONTENT, objects.get(snapshot), "还原也必须保留独立快照");
        verify(files, never()).insert(any(EsFile.class));
        verify(storage, never()).deleteAt(anyString(), anyString());
        verify(usage, times(2)).invalidate(PREFIX);
        verify(ledger, times(5)).checkpoint(any(EsRecycleBatch.class));
        assertEquals(List.of("preparing", "preparing", "preparing", "preparing", "restoring"),
            checkpoints.stream().map(EsRecycleBatch::getStatus).toList());
        assertTrue(checkpoints.stream().allMatch(checkpoint -> codec.decodeMembers(checkpoint.getMembersJson()).size() == 3));
        assertEquals("restored", savedBatches.get(deleted.batchId()).getStatus());
    }

    @Test
    void aConflictOnAnyRestoreMemberPreventsAllCopiesAndAllStateWrites() {
        for (String conflictPath : List.of("docs", "docs/z.txt")) {
            setUp();
            EsFile root = recycledRow(7L, "docs", true, "batch-1", true);
            EsFile first = recycledRow(8L, "docs/a.txt", false, "batch-1", false);
            EsFile last = recycledRow(9L, "docs/z.txt", false, "batch-1", false);
            EsRecycleBatch batch = batch("batch-1", 7L, "docs", true, 3);
            prepareRestore(root, batch, List.of(first, root, last));
            when(storage.metadataAt(eq(PREFIX), anyString())).thenAnswer(invocation -> {
                String path = invocation.getArgument(1);
                return path.startsWith(DriveRecycleKeys.STORAGE_DIR) ? CONTENT : null;
            });
            when(fileService.activeAt(SPACE, OWNER, conflictPath))
                .thenReturn(row(99L, conflictPath, false));
            assertThrows(ServiceException.class, () -> service.restore(SPACE, OWNER, OWNER, 7L));
            verify(storage, never()).copyAt(anyString(), anyString(), anyString());
            verify(storage, never()).deleteAt(anyString(), anyString());
            verify(files, never()).update(any(), any());
            verify(files, never()).updateById(any(EsFile.class));
            verify(batches, never()).updateById(any(EsRecycleBatch.class));
            assertEquals("recycled", batch.getStatus());
            assertTrue(List.of(root, first, last).stream()
                .allMatch(member -> EsFile.STATE_RECYCLED.equals(member.getState())));
            verifyNoInteractions(changes, usage, ledger);
        }
    }

    @Test
    void purgeMultipartOriginUsesFrozenShaNotCopiedEtagAndKeepsChangedBody() {
        for (boolean changed : new boolean[]{false, true}) {
            setUp();
            EsFile file = recycledRow(7L, "docs/a.txt", false, "batch-1", true);
            EsRecycleBatch batch = batch("batch-1", 7L, file.getFilePath(), false, 1);
            prepareRestore(file, batch, List.of(file));
            file.setRecycleSnapshotEtag("single-copy-etag");
            when(fileService.purgeFromRecycle(SPACE, OWNER, 7L))
                .thenReturn(new DriveFileService.PurgeResult(file, true));
            DriveStorage.ObjectMetadata original = new DriveStorage.ObjectMetadata(BODY.length, "multipart-etag-2");
            when(storage.metadataAt(PREFIX, file.getFilePath())).thenReturn(original);
            byte[] originBody = changed ? OTHER_BODY : BODY;
            when(storage.sha256At(PREFIX, file.getFilePath())).thenReturn(sha256(originBody));
            assertEquals("purged", service.purge(SPACE, OWNER, OWNER, 7L).status());
            verify(storage).deleteAt(PREFIX, file.getRecycleStoragePath());
            verify(storage, times(changed ? 0 : 1)).deleteAt(PREFIX, file.getFilePath());
            verify(storage).sha256At(PREFIX, file.getFilePath());
            verify(storage, never()).copyAt(anyString(), anyString(), anyString());
        }
    }

    @Test
    void purgeStopsDeletingOriginWhenHeadChangesDuringHashVerification() {
        EsFile file = recycledRow(7L, "docs/a.txt", false, "batch-1", true);
        EsRecycleBatch batch = batch("batch-1", 7L, file.getFilePath(), false, 1);
        prepareRestore(file, batch, List.of(file));
        when(fileService.purgeFromRecycle(SPACE, OWNER, 7L))
            .thenReturn(new DriveFileService.PurgeResult(file, true));
        when(storage.metadataAt(PREFIX, file.getFilePath()))
            .thenReturn(new DriveStorage.ObjectMetadata(BODY.length, "old-etag"),
                new DriveStorage.ObjectMetadata(BODY.length, "changed-etag"));
        assertEquals("purged", service.purge(SPACE, OWNER, OWNER, 7L).status());
        verify(storage).deleteAt(PREFIX, file.getRecycleStoragePath());
        verify(storage, never()).deleteAt(PREFIX, file.getFilePath());
    }

    @Test
    void snapshotOwnershipIsExactForRestoreAndPurgeAndNeverDeletesANewNamesake() {
        for (String candidate : new String[]{null, ".recycle/other-batch/7/v3", ".recycle/batch-1/8/v3",
            ".recycle/batch-1/7/v4", "users/2/.recycle/batch-1/7/v3", ".recycle//batch-1/7/v3",
            ".recycle/batch-1/7/../7/v3", ".recycle/batch-1/7/v3/"}) {
            for (boolean restore : new boolean[]{true, false}) {
                setUp();
                EsFile file = recycledRow(7L, "docs/a.txt", false, "batch-1", true);
                file.setRecycleStoragePath(candidate);
                EsRecycleBatch batch = batch("batch-1", 7L, file.getFilePath(), false, 1);
                prepareRestore(file, batch, List.of(file));
                assertThrows(ServiceException.class, () -> {
                    if (restore) { service.restore(SPACE, OWNER, OWNER, 7L); }
                    else { service.purge(SPACE, OWNER, OWNER, 7L); }
                }, String.valueOf(candidate));
                verifyNoInteractions(storage, changes, usage, ledger);
                verify(fileService, never()).purgeFromRecycle(anyString(), anyLong(), anyLong());
                verify(files, never()).update(any(), any());
                verify(files, never()).updateById(any(EsFile.class));
                verify(files, never()).deleteById(any(java.io.Serializable.class));
                verify(batches, never()).updateById(any(EsRecycleBatch.class));
            }
        }
        setUp();
        EsFile file = recycledRow(7L, "docs/a.txt", false, "batch-1", true);
        EsRecycleBatch batch = batch("batch-1", 7L, file.getFilePath(), false, 1);
        prepareRestore(file, batch, List.of(file));
        // 同名 active 已重新上传：目录层明确返回当前原路径仍被引用，不能隐含清理它。
        when(fileService.purgeFromRecycle(SPACE, OWNER, 7L))
            .thenReturn(new DriveFileService.PurgeResult(file, false));
        assertEquals("purged", service.purge(SPACE, OWNER, OWNER, 7L).status());
        verify(storage).deleteAt(PREFIX, file.getRecycleStoragePath());
        verify(storage, never()).deleteAt(PREFIX, file.getFilePath());
        verify(storage, never()).deleteDirectory(anyString(), anyString());
        verify(storage, never()).copyAt(anyString(), anyString(), anyString());
        assertEquals(List.of("purging"), checkpoints.stream().map(EsRecycleBatch::getStatus).toList());
        EsFile frozen = codec.decodeMembers(checkpoints.getFirst().getMembersJson()).getFirst();
        assertEquals(7L, frozen.getFileId());
        assertEquals(file.getRecycleStoragePath(), frozen.getRecycleStoragePath());
        assertEquals("purged", savedBatches.get("batch-1").getStatus());
    }

    @Test
    void rejectsCrossUserCrossSpaceAndImplicitCurrentRootWithoutStorageSideEffects() {
        List<Runnable> crossUserCalls = List.of(
            () -> service.recycle(SPACE, 2L, OWNER, 7L, "docs", 3L, OPERATION, true),
            () -> service.restore(SPACE, 2L, OWNER, 7L),
            () -> service.purge(SPACE, 2L, OWNER, 7L),
            () -> service.operation(SPACE, 2L, OWNER, OPERATION));
        for (Runnable call : crossUserCalls) { assertThrows(ServiceException.class, call::run); }
        verifyNoInteractions(files, batches, fileService, storage, changes, usage);

        for (String boundary : List.of("row-owner", "row-space", "batch-owner", "batch-space")) {
            setUp();
            EsFile selected = recycledRow(7L, "docs/a.txt", false, "batch-1", true);
            EsRecycleBatch batch = batch("batch-1", 7L, selected.getFilePath(), false, 1);
            if ("row-owner".equals(boundary)) { selected.setOwnerId(2L); }
            if ("row-space".equals(boundary)) { selected.setSpaceType(EsFile.TYPE_SHARED); }
            if ("batch-owner".equals(boundary)) { batch.setOwnerId(2L); }
            if ("batch-space".equals(boundary)) { batch.setSpaceType(EsFile.TYPE_SHARED); }
            prepareRestore(selected, batch, List.of(selected));
            assertThrows(ServiceException.class, () -> service.restore(SPACE, OWNER, OWNER, 7L), boundary);
            assertThrows(ServiceException.class, () -> service.purge(SPACE, OWNER, OWNER, 7L), boundary);
            verifyNoInteractions(storage, changes, usage);
            verify(files, never()).update(any(), any());
            verify(batches, never()).updateById(any(EsRecycleBatch.class));
        }

        setUp();
        // 这两条边界保留真实目录 helper，防止 mock 自己的异常掩盖真实 fileId/路径检查。
        fileService = spy(new DriveFileService(files, changes, roles));
        service = new DriveRecycleService(files, batches, fileService, storage, changes, roles, usage, ledger);
        EsFile foreign = row(7L, "docs/a.txt", false);
        foreign.setOwnerId(2L);
        when(files.selectById(7L)).thenReturn(foreign);
        assertThrows(ServiceException.class, () -> service.recycle(SPACE, OWNER, OWNER,
            7L, "docs/a.txt", 3L, OPERATION, false));
        for (String implicitRoot : new String[]{null, "", " ", "/", "///", ".", ".."}) {
            assertThrows(ServiceException.class, () -> service.recycle(SPACE, OWNER, OWNER,
                null, implicitRoot, null, OPERATION, true), String.valueOf(implicitRoot));
        }
        verify(fileService, never()).lockActiveForPresign(anyString(), anyLong(), anyString());
        verify(batches, never()).insert(any(EsRecycleBatch.class));
        verify(files, never()).updateById(any(EsFile.class));
        verify(files, never()).update(any(), any());
        verifyNoInteractions(storage, changes, usage);
    }

    @Test
    void copyFailureKeepsPreparingLedgerAndRetryReusesIdentitiesAndReadySnapshots() {
        EsFile root = row(7L, "docs", true);
        EsFile first = row(8L, "docs/a.txt", false);
        EsFile second = row(9L, "docs/b.txt", false);
        prepareDelete(root);
        when(files.selectList(any())).thenReturn(List.of(root, first, second));
        Map<String, DriveStorage.ObjectMetadata> objects = new java.util.HashMap<>();
        objects.put(first.getFilePath(), CONTENT);
        objects.put(second.getFilePath(), CONTENT);
        when(storage.metadataAt(eq(PREFIX), anyString()))
            .thenAnswer(invocation -> objects.get(invocation.getArgument(1)));
        java.util.concurrent.atomic.AtomicBoolean failSecondCopy = new java.util.concurrent.atomic.AtomicBoolean(true);
        doAnswer(invocation -> {
            String from = invocation.getArgument(1);
            if (second.getFilePath().equals(from) && failSecondCopy.getAndSet(false)) {
                throw new ServiceException("copy failed after first member checkpoint");
            }
            assertNotNull(objects.get(from));
            objects.put(invocation.getArgument(2), objects.get(from));
            return null;
        }).when(storage).copyAt(eq(PREFIX), anyString(), anyString());

        assertThrows(ServiceException.class, () -> service.recycle(SPACE, OWNER, OWNER,
            7L, "docs", 3L, OPERATION, true));
        assertEquals(1, savedBatches.size());
        EsRecycleBatch preparing = copyBatch(savedBatches.values().iterator().next());
        assertEquals("preparing", preparing.getStatus());
        assertEquals(OPERATION, preparing.getOperationId());
        assertEquals(7L, preparing.getRootFileId());
        List<EsFile> frozen = codec.decodeMembers(preparing.getMembersJson());
        assertEquals(List.of(7L, 8L, 9L), frozen.stream().map(EsFile::getFileId).toList());
        assertEquals(3, frozen.size(), "失败操作账必须保留全部成员，不能只存已复制成员");
        String firstSnapshot = DriveRecycleKeys.storagePathOf(preparing.getBatchId(), 8L, 3L);
        String secondSnapshot = DriveRecycleKeys.storagePathOf(preparing.getBatchId(), 9L, 3L);
        assertEquals(firstSnapshot, frozen.get(1).getRecycleStoragePath());
        assertEquals("ready", frozen.get(1).getRecycleSnapshotState());
        assertEquals(CONTENT.etag(), frozen.get(1).getRecycleSnapshotEtag());
        assertEquals(CONTENT.size(), frozen.get(1).getRecycleSnapshotSize());
        assertNull(frozen.get(2).getRecycleSnapshotState());
        assertEquals(secondSnapshot, frozen.get(2).getRecycleStoragePath(), "copy 失败也必须先冻结键");
        assertEquals(CONTENT_SHA, frozen.get(2).getRecycleSnapshotSha256(), "copy 失败也必须先冻结 SHA");
        assertEquals(CONTENT_SHA, frozen.get(1).getRecycleSnapshotSha256());
        assertEquals(4, checkpoints.size());
        assertTrue(checkpoints.stream().allMatch(checkpoint -> "preparing".equals(checkpoint.getStatus())));
        assertEquals(CONTENT, objects.get(firstSnapshot));
        assertTrue(List.of(root, first, second).stream().allMatch(row -> EsFile.STATE_ACTIVE.equals(row.getState())));
        verify(files, never()).updateById(any(EsFile.class));
        verify(files, never()).update(any(), any());
        verifyNoInteractions(changes, usage);
        DriveRecycleService.Result pending = service.operation(SPACE, OWNER, OWNER, OPERATION);
        assertEquals(preparing.getBatchId(), pending.batchId());
        assertEquals("preparing", pending.status());

        // 模拟目录事务回滚后重新查库：不能沿用上次调用的可变实体冒充持久成员记录。
        EsFile reloadedRoot = row(7L, "docs", true);
        doReturn(reloadedRoot).when(fileService).lockActiveForPresign(SPACE, OWNER, "docs");
        when(files.selectList(any())).thenReturn(List.of(reloadedRoot,
            row(8L, "docs/a.txt", false), row(9L, "docs/b.txt", false)));
        DriveRecycleService.Result retried = service.recycle(SPACE, OWNER, OWNER,
            7L, "docs", 3L, OPERATION, true);
        assertEquals(preparing.getBatchId(), retried.batchId());
        assertEquals(OPERATION, retried.operationId());
        assertEquals("7", retried.fileId());
        assertEquals("recycled", retried.status());
        verify(ledger, times(1)).reserve(any(EsRecycleBatch.class));
        verify(batches, times(1)).insert(any(EsRecycleBatch.class));
        verify(ledger).decodeMembers(preparing.getMembersJson());
        verify(storage, times(1)).copyAt(PREFIX, first.getFilePath(), firstSnapshot);
        verify(storage, times(2)).copyAt(PREFIX, second.getFilePath(), secondSnapshot);
        verify(storage, times(4)).metadataAt(PREFIX, first.getFilePath());
        verify(storage, times(2)).metadataAt(PREFIX, firstSnapshot);
        verify(storage, times(3)).metadataAt(PREFIX, second.getFilePath());
        // 源 HEAD 每次尝试必须核对；失败 copy 不会读取未确认的目标。
        verify(storage, times(1)).metadataAt(PREFIX, secondSnapshot);
        var updated = ArgumentCaptor.forClass(EsFile.class);
        verify(files, times(3)).updateById(updated.capture());
        assertEquals(List.of(8L, 9L, 7L), updated.getAllValues().stream().map(EsFile::getFileId).toList());
        assertTrue(updated.getAllValues().stream().allMatch(row -> preparing.getBatchId().equals(row.getRecycleBatchId())
            && row.getVersionId() == 3L && EsFile.STATE_RECYCLED.equals(row.getState())));
        assertEquals(secondSnapshot, updated.getAllValues().get(1).getRecycleStoragePath());
        assertEquals(9, checkpoints.size());
        assertTrue(checkpoints.stream().allMatch(checkpoint -> codec.decodeMembers(checkpoint.getMembersJson()).size() == 3));
        assertEquals(CONTENT, objects.get(firstSnapshot));
        assertEquals(CONTENT, objects.get(secondSnapshot));
        assertEquals("recycled", savedBatches.get(preparing.getBatchId()).getStatus());
        verify(files, never()).insert(any(EsFile.class));
        verify(storage, never()).deleteAt(anyString(), anyString());
    }

    @Test
    void failedRestorePersistsRestoringMembersAndRetryKeepsSnapshotsAndFileIds() {
        EsFile root = recycledRow(7L, "docs", true, "batch-1", true);
        EsFile first = recycledRow(8L, "docs/a.txt", false, "batch-1", false);
        EsFile second = recycledRow(9L, "docs/b.txt", false, "batch-1", false);
        EsRecycleBatch batch = batch("batch-1", 7L, "docs", true, 3);
        prepareRestore(root, batch, List.of(root, first, second));
        String membersJson = batch.getMembersJson();
        Map<String, DriveStorage.ObjectMetadata> objects = new java.util.HashMap<>();
        objects.put(first.getRecycleStoragePath(), CONTENT);
        objects.put(second.getRecycleStoragePath(), CONTENT);
        when(storage.metadataAt(eq(PREFIX), anyString()))
            .thenAnswer(invocation -> objects.get(invocation.getArgument(1)));
        java.util.concurrent.atomic.AtomicBoolean failSecondCopy = new java.util.concurrent.atomic.AtomicBoolean(true);
        doAnswer(invocation -> {
            String from = invocation.getArgument(1);
            assertEquals("restoring", savedBatches.get("batch-1").getStatus(), "copy 前必须独立提交恢复操作账");
            assertEquals(membersJson, savedBatches.get("batch-1").getMembersJson());
            if (second.getRecycleStoragePath().equals(from) && failSecondCopy.getAndSet(false)) {
                throw new ServiceException("restore copy failed");
            }
            assertEquals(CONTENT, objects.get(from));
            objects.put(invocation.getArgument(2), objects.get(from));
            return null;
        }).when(storage).copyAt(eq(PREFIX), anyString(), anyString());

        assertThrows(ServiceException.class, () -> service.restore(SPACE, OWNER, OWNER, 7L));
        InOrder order = inOrder(batches, ledger, storage, files);
        order.verify(batches).lockSpace(SPACE, OWNER);
        order.verify(files).selectById(7L);
        order.verify(batches).selectById("batch-1");
        order.verify(storage).metadataAt(PREFIX, first.getRecycleStoragePath());
        order.verify(storage).sha256At(PREFIX, first.getRecycleStoragePath());
        order.verify(storage).metadataAt(PREFIX, first.getOriginPath());
        order.verify(storage).metadataAt(PREFIX, second.getRecycleStoragePath());
        order.verify(storage).sha256At(PREFIX, second.getRecycleStoragePath());
        order.verify(storage).metadataAt(PREFIX, second.getOriginPath());
        order.verify(ledger).checkpoint(any(EsRecycleBatch.class));
        order.verify(batches).updateById(any(EsRecycleBatch.class));
        order.verify(storage).copyAt(PREFIX, first.getRecycleStoragePath(), first.getOriginPath());
        order.verify(storage).metadataAt(PREFIX, first.getOriginPath());
        order.verify(storage).sha256At(PREFIX, first.getOriginPath());
        order.verify(storage).copyAt(PREFIX, second.getRecycleStoragePath(), second.getOriginPath());
        assertEquals(1, checkpoints.size());
        EsRecycleBatch restoring = copyBatch(savedBatches.get("batch-1"));
        assertEquals("restoring", restoring.getStatus());
        assertEquals(OPERATION, restoring.getOperationId());
        assertEquals(7L, restoring.getRootFileId());
        assertEquals(membersJson, restoring.getMembersJson());
        List<EsFile> frozen = codec.decodeMembers(restoring.getMembersJson());
        assertEquals(List.of(7L, 8L, 9L), frozen.stream().map(EsFile::getFileId).toList());
        assertEquals(first.getRecycleStoragePath(), frozen.get(1).getRecycleStoragePath());
        assertEquals(second.getRecycleStoragePath(), frozen.get(2).getRecycleStoragePath());
        assertEquals(CONTENT.etag(), frozen.get(2).getRecycleSnapshotEtag());
        assertEquals(CONTENT.size(), frozen.get(2).getRecycleSnapshotSize());
        assertEquals(CONTENT, objects.get(first.getFilePath()));
        assertNull(objects.get(second.getFilePath()));
        assertEquals(CONTENT, objects.get(first.getRecycleStoragePath()));
        assertEquals(CONTENT, objects.get(second.getRecycleStoragePath()));
        assertTrue(List.of(root, first, second).stream().allMatch(row -> EsFile.STATE_RECYCLED.equals(row.getState())));
        verify(files, never()).update(any(), any());
        verify(files, never()).updateById(any(EsFile.class));
        verifyNoInteractions(changes, usage);
        assertEquals("restoring", service.operation(SPACE, OWNER, OWNER, OPERATION).status());

        EsFile reloadedRoot = recycledRow(7L, "docs", true, "batch-1", true);
        EsFile reloadedFirst = recycledRow(8L, "docs/a.txt", false, "batch-1", false);
        EsFile reloadedSecond = recycledRow(9L, "docs/b.txt", false, "batch-1", false);
        when(files.selectById(7L)).thenReturn(reloadedRoot);
        when(files.selectList(any())).thenAnswer(invocation -> new ArrayList<>(List.of(reloadedSecond, reloadedFirst, reloadedRoot)));
        DriveRecycleService.Result result = service.restore(SPACE, OWNER, OWNER, 7L);
        assertEquals("batch-1", result.batchId());
        assertEquals(OPERATION, result.operationId());
        assertEquals("7", result.fileId());
        assertEquals("restored", result.status());
        verify(ledger, times(2)).checkpoint(any(EsRecycleBatch.class));
        assertEquals(List.of("restoring", "restoring"), checkpoints.stream().map(EsRecycleBatch::getStatus).toList());
        assertTrue(checkpoints.stream().allMatch(checkpoint -> membersJson.equals(checkpoint.getMembersJson())));
        verify(ledger, never()).reserve(any(EsRecycleBatch.class));
        verify(batches, never()).insert(any(EsRecycleBatch.class));
        verify(storage, times(2)).copyAt(PREFIX, first.getRecycleStoragePath(), first.getFilePath());
        verify(storage, times(2)).copyAt(PREFIX, second.getRecycleStoragePath(), second.getFilePath());
        verify(storage, times(2)).metadataAt(PREFIX, first.getRecycleStoragePath());
        verify(storage, times(4)).metadataAt(PREFIX, first.getFilePath());
        verify(storage, times(2)).metadataAt(PREFIX, second.getRecycleStoragePath());
        verify(storage, times(3)).metadataAt(PREFIX, second.getFilePath());
        verify(storage, times(2)).sha256At(PREFIX, first.getRecycleStoragePath());
        verify(storage, times(3)).sha256At(PREFIX, first.getFilePath());
        verify(storage, times(2)).sha256At(PREFIX, second.getRecycleStoragePath());
        verify(storage, times(1)).sha256At(PREFIX, second.getFilePath());
        var updates = ArgumentCaptor.forClass(LambdaUpdateWrapper.class);
        verify(files, times(3)).update(isNull(), updates.capture());
        assertEquals(List.of(7L, 9L, 8L), updates.getAllValues().stream()
            .map(wrapper -> updateFileId((LambdaUpdateWrapper<?>) wrapper)).toList());
        assertTrue(List.of(reloadedRoot, reloadedFirst, reloadedSecond).stream()
            .allMatch(row -> EsFile.STATE_ACTIVE.equals(row.getState()) && row.getVersionId() == 3L));
        assertEquals(CONTENT, objects.get(first.getFilePath()));
        assertEquals(CONTENT, objects.get(second.getFilePath()));
        assertEquals(CONTENT, objects.get(first.getRecycleStoragePath()));
        assertEquals(CONTENT, objects.get(second.getRecycleStoragePath()));
        assertEquals("restored", savedBatches.get("batch-1").getStatus());
        verify(files, never()).insert(any(EsFile.class));
        verify(storage, never()).deleteAt(anyString(), anyString());
        verify(usage).invalidate(PREFIX);
    }

    @Test
    void multipartDifferentEtagsWithMatchingShaRecyclesAndRestoresWithoutChangingIdentity() {
        EsFile file = row(7L, "docs/a.txt", false);
        prepareDelete(file);
        Map<String, byte[]> bodies = new java.util.HashMap<>();
        Map<String, DriveStorage.ObjectMetadata> metadata = new java.util.HashMap<>();
        bodies.put(file.getFilePath(), BODY.clone());
        metadata.put(file.getFilePath(), new DriveStorage.ObjectMetadata(BODY.length, "multipart-etag-2"));
        when(storage.metadataAt(eq(PREFIX), anyString())).thenAnswer(i -> metadata.get(i.getArgument(1)));
        when(storage.sha256At(eq(PREFIX), anyString())).thenAnswer(i -> sha256(bodies.get(i.getArgument(1))));
        doAnswer(i -> {
            String from = i.getArgument(1), to = i.getArgument(2);
            byte[] body = bodies.get(from).clone();
            bodies.put(to, body);
            metadata.put(to, new DriveStorage.ObjectMetadata(body.length,
                to.startsWith(DriveRecycleKeys.STORAGE_DIR) ? "single-copy-etag" : "restored-copy-etag"));
            return null;
        }).when(storage).copyAt(eq(PREFIX), anyString(), anyString());
        DriveRecycleService.Result deleted = service.recycle(SPACE, OWNER, OWNER,
            7L, file.getFilePath(), 3L, OPERATION, false);
        String snapshot = DriveRecycleKeys.storagePathOf(deleted.batchId(), 7L, 3L);
        assertEquals("recycled", deleted.status());
        assertEquals("single-copy-etag", file.getRecycleSnapshotEtag());
        assertEquals(CONTENT_SHA, file.getRecycleSnapshotSha256());
        assertNotEquals(metadata.get(file.getFilePath()).etag(), metadata.get(snapshot).etag());
        assertArrayEquals(BODY, bodies.get(snapshot));

        // 存储原位置已有相同正文但不同 ETag；不能把 multipart ETag 当内容摘要拒绝还原。
        metadata.put(file.getFilePath(), new DriveStorage.ObjectMetadata(BODY.length, "same-body-new-etag-4"));
        when(files.selectById(7L)).thenReturn(file);
        when(files.selectList(any())).thenAnswer(i -> new ArrayList<>(List.of(file)));
        DriveRecycleService.Result restored = service.restore(SPACE, OWNER, OWNER, 7L);
        assertEquals("restored", restored.status());
        assertEquals(deleted.batchId(), restored.batchId());
        assertEquals("7", restored.fileId());
        assertEquals(7L, file.getFileId());
        assertEquals(3L, file.getVersionId());
        assertEquals("restored-copy-etag", metadata.get(file.getFilePath()).etag());
        assertNotEquals(metadata.get(snapshot).etag(), metadata.get(file.getFilePath()).etag());
        assertArrayEquals(BODY, bodies.get(file.getFilePath()));
        assertArrayEquals(BODY, bodies.get(snapshot));
        verify(storage, times(4)).sha256At(PREFIX, file.getFilePath());
        verify(storage, times(2)).sha256At(PREFIX, snapshot);
        verify(files, never()).insert(any(EsFile.class));
        verify(storage, never()).deleteAt(anyString(), anyString());
    }

    @Test
    void sameSizeAndUnchangedEtagsCannotHideChangedSourceOrCopiedSha() {
        assertEquals(BODY.length, OTHER_BODY.length);
        assertNotEquals(CONTENT_SHA, OTHER_SHA);
        for (boolean sourceChanged : new boolean[]{true, false}) {
            setUp();
            EsFile file = row(7L, "docs/a.txt", false);
            prepareDelete(file);
            when(storage.metadataAt(eq(PREFIX), anyString())).thenReturn(CONTENT);
            if (sourceChanged) {
                when(storage.sha256At(PREFIX, file.getFilePath())).thenReturn(CONTENT_SHA, OTHER_SHA);
            } else {
                when(storage.sha256At(eq(PREFIX), startsWith(DriveRecycleKeys.STORAGE_DIR))).thenReturn(OTHER_SHA);
            }
            assertThrows(ServiceException.class, () -> service.recycle(SPACE, OWNER, OWNER,
                7L, file.getFilePath(), 3L, OPERATION, false));
            EsRecycleBatch preparing = savedBatches.values().iterator().next();
            EsFile frozen = codec.decodeMembers(preparing.getMembersJson()).getFirst();
            assertEquals("preparing", preparing.getStatus());
            assertEquals(CONTENT_SHA, frozen.getRecycleSnapshotSha256());
            assertEquals(7L, frozen.getFileId());
            assertEquals(3L, frozen.getRecycleSnapshotVersion());
            assertNull(frozen.getRecycleSnapshotState());
            assertEquals(EsFile.STATE_ACTIVE, file.getState());
            verify(storage, times(2)).metadataAt(PREFIX, file.getFilePath());
            verify(storage).metadataAt(PREFIX, frozen.getRecycleStoragePath());
            verify(storage).sha256At(PREFIX, frozen.getRecycleStoragePath());
            verify(storage, times(sourceChanged ? 2 : 1)).sha256At(PREFIX, file.getFilePath());
            verify(files, never()).updateById(any(EsFile.class));
            verify(files, never()).update(any(), any());
            verifyNoInteractions(changes, usage);
        }
    }

    @Test
    void readySnapshotRetryNeverCopiesAndRejectsSameSizeHashChanges() {
        for (String change : List.of("none", "source", "snapshot")) {
            setUp();
            EsFile active = row(7L, "docs/a.txt", false);
            EsFile frozen = recycledRow(7L, "docs/a.txt", false, "batch-1", true);
            frozen.setState(EsFile.STATE_ACTIVE);
            EsRecycleBatch preparing = batch("batch-1", 7L, frozen.getFilePath(), false, 1);
            preparing.setStatus("preparing");
            preparing.setMembersJson(codec.encodeMembers(List.of(frozen)));
            savedBatches.put("batch-1", copyBatch(preparing));
            prepareDelete(active);
            when(storage.metadataAt(eq(PREFIX), anyString())).thenReturn(CONTENT);
            if ("source".equals(change)) {
                when(storage.sha256At(PREFIX, active.getFilePath())).thenReturn(OTHER_SHA);
            } else if ("snapshot".equals(change)) {
                when(storage.sha256At(PREFIX, frozen.getRecycleStoragePath())).thenReturn(OTHER_SHA);
            }
            if ("none".equals(change)) {
                DriveRecycleService.Result result = service.recycle(SPACE, OWNER, OWNER,
                    7L, active.getFilePath(), 3L, OPERATION, false);
                assertEquals("batch-1", result.batchId());
                assertEquals("7", result.fileId());
                assertEquals("recycled", result.status());
                var update = ArgumentCaptor.forClass(EsFile.class);
                verify(files).updateById(update.capture());
                assertEquals(CONTENT_SHA, update.getValue().getRecycleSnapshotSha256());
                assertEquals(frozen.getRecycleStoragePath(), update.getValue().getRecycleStoragePath());
            } else {
                assertThrows(ServiceException.class, () -> service.recycle(SPACE, OWNER, OWNER,
                    7L, active.getFilePath(), 3L, OPERATION, false));
                verify(files, never()).updateById(any(EsFile.class));
                assertEquals("preparing", savedBatches.get("batch-1").getStatus());
                assertEquals(CONTENT_SHA, codec.decodeMembers(savedBatches.get("batch-1").getMembersJson())
                    .getFirst().getRecycleSnapshotSha256());
                verifyNoInteractions(changes, usage);
            }
            verify(storage, never()).copyAt(anyString(), anyString(), anyString());
            verify(ledger, never()).reserve(any(EsRecycleBatch.class));
            verify(batches, never()).insert(any(EsRecycleBatch.class));
            verify(storage, times("source".equals(change) ? 1 : 2)).metadataAt(PREFIX, active.getFilePath());
            verify(storage, times("none".equals(change) ? 2 : 1)).sha256At(PREFIX, active.getFilePath());
            if (!"source".equals(change)) {
                verify(storage).metadataAt(PREFIX, frozen.getRecycleStoragePath());
                verify(storage).sha256At(PREFIX, frozen.getRecycleStoragePath());
            }
        }
    }

    @Test
    void restoreRejectsSameSizeHashCorruptionBeforeOrAfterCopyWithoutStateCommit() {
        for (String corrupt : List.of("snapshot", "occupied", "restored")) {
            setUp();
            EsFile file = recycledRow(7L, "docs/a.txt", false, "batch-1", true);
            EsRecycleBatch batch = batch("batch-1", 7L, file.getFilePath(), false, 1);
            prepareRestore(file, batch, List.of(file));
            Map<String, byte[]> bodies = new java.util.HashMap<>();
            bodies.put(file.getRecycleStoragePath(), "snapshot".equals(corrupt) ? OTHER_BODY : BODY);
            if ("occupied".equals(corrupt)) { bodies.put(file.getOriginPath(), OTHER_BODY); }
            when(storage.metadataAt(eq(PREFIX), anyString())).thenAnswer(i -> {
                byte[] body = bodies.get(i.getArgument(1));
                return body == null ? null : new DriveStorage.ObjectMetadata(body.length, CONTENT.etag());
            });
            when(storage.sha256At(eq(PREFIX), anyString())).thenAnswer(i -> sha256(bodies.get(i.getArgument(1))));
            doAnswer(i -> {
                bodies.put(i.getArgument(2), OTHER_BODY);
                return null;
            }).when(storage).copyAt(eq(PREFIX), anyString(), anyString());
            assertThrows(ServiceException.class, () -> service.restore(SPACE, OWNER, OWNER, 7L), corrupt);
            assertEquals(EsFile.STATE_RECYCLED, file.getState(), corrupt);
            assertEquals(7L, file.getFileId());
            assertEquals(CONTENT_SHA, file.getRecycleSnapshotSha256());
            assertEquals("restored".equals(corrupt) ? "restoring" : "recycled", savedBatches.get("batch-1").getStatus());
            verify(storage).metadataAt(PREFIX, file.getRecycleStoragePath());
            verify(storage).sha256At(PREFIX, file.getRecycleStoragePath());
            verify(files, never()).update(any(), any());
            verify(files, never()).updateById(any(EsFile.class));
            verify(storage, never()).deleteAt(anyString(), anyString());
            verifyNoInteractions(changes, usage);
            if ("restored".equals(corrupt)) {
                verify(storage).copyAt(PREFIX, file.getRecycleStoragePath(), file.getOriginPath());
                verify(storage, times(2)).metadataAt(PREFIX, file.getOriginPath());
                verify(storage).sha256At(PREFIX, file.getOriginPath());
                assertArrayEquals(BODY, bodies.get(file.getRecycleStoragePath()));
            } else {
                verify(storage, never()).copyAt(anyString(), anyString(), anyString());
                verify(ledger, never()).checkpoint(any(EsRecycleBatch.class));
            }
        }
    }

    @Test
    void restoredFileEditedAndDeletedAgainUsesNewBatchAndShaWithoutTouchingOldSnapshot() {
        EsFile file = row(7L, "docs/a.txt", false);
        prepareDelete(file);
        Map<String, byte[]> bodies = new java.util.HashMap<>();
        Map<String, String> etags = new java.util.HashMap<>();
        bodies.put(file.getFilePath(), BODY.clone());
        etags.put(file.getFilePath(), CONTENT.etag());
        when(storage.metadataAt(eq(PREFIX), anyString())).thenAnswer(i -> {
            String key = i.getArgument(1);
            byte[] body = bodies.get(key);
            return body == null ? null : new DriveStorage.ObjectMetadata(body.length, etags.get(key));
        });
        when(storage.sha256At(eq(PREFIX), anyString())).thenAnswer(i -> sha256(bodies.get(i.getArgument(1))));
        doAnswer(i -> {
            String from = i.getArgument(1), to = i.getArgument(2);
            byte[] body = bodies.get(from);
            assertNotNull(body);
            if (to.startsWith(DriveRecycleKeys.STORAGE_DIR)) {
                EsRecycleBatch persisted = savedBatches.get(file.getRecycleBatchId());
                EsFile frozen = codec.decodeMembers(persisted.getMembersJson()).getFirst();
                assertEquals("preparing", persisted.getStatus());
                assertEquals(to, frozen.getRecycleStoragePath());
                assertEquals(sha256(body), frozen.getRecycleSnapshotSha256(), "copy 前必须持久保存本批新正文 SHA");
                assertNull(frozen.getRecycleSnapshotState(), "旧批 ready 状态不能让新批跳过 copy");
                assertNull(frozen.getRecycleSnapshotEtag());
                assertNull(frozen.getRecycleSnapshotSize());
            }
            bodies.put(to, body.clone());
            etags.put(to, "copy-" + sha256(body));
            return null;
        }).when(storage).copyAt(eq(PREFIX), anyString(), anyString());

        DriveRecycleService.Result first = service.recycle(SPACE, OWNER, OWNER,
            7L, file.getFilePath(), 3L, OPERATION, false);
        String oldSnapshot = DriveRecycleKeys.storagePathOf(first.batchId(), 7L, 3L);
        when(files.selectById(7L)).thenReturn(file);
        when(files.selectList(any())).thenAnswer(i -> new ArrayList<>(List.of(file)));
        assertEquals("restored", service.restore(SPACE, OWNER, OWNER, 7L).status());
        assertEquals(EsFile.STATE_ACTIVE, file.getState());
        assertEquals(first.batchId(), file.getRecycleBatchId());
        assertEquals("ready", file.getRecycleSnapshotState());
        assertEquals(CONTENT_SHA, file.getRecycleSnapshotSha256());
        String oldMembersJson = savedBatches.get(first.batchId()).getMembersJson();

        file.setVersionId(4L);
        file.setFileSize((long) OTHER_BODY.length);
        bodies.put(file.getFilePath(), OTHER_BODY.clone());
        etags.put(file.getFilePath(), "edited-v4-etag");
        DriveRecycleService.Result second = service.recycle(SPACE, OWNER, OWNER,
            7L, file.getFilePath(), 4L, "delete-op-2", false);
        String newSnapshot = DriveRecycleKeys.storagePathOf(second.batchId(), 7L, 4L);
        assertNotEquals(first.batchId(), second.batchId());
        assertNotEquals(oldSnapshot, newSnapshot);
        assertEquals("delete-op-2", second.operationId());
        assertEquals("7", second.fileId());
        assertEquals("recycled", second.status());
        assertEquals(7L, file.getFileId());
        assertEquals(4L, file.getVersionId());
        assertEquals(4L, file.getRecycleSnapshotVersion());
        assertEquals(second.batchId(), file.getRecycleBatchId());
        assertEquals(newSnapshot, file.getRecycleStoragePath());
        assertEquals(OTHER_SHA, file.getRecycleSnapshotSha256());
        assertEquals("ready", file.getRecycleSnapshotState());
        assertArrayEquals(OTHER_BODY, bodies.get(newSnapshot));
        assertArrayEquals(BODY, bodies.get(oldSnapshot), "旧批独立快照不能被第二次删除覆盖");
        assertEquals("restored", savedBatches.get(first.batchId()).getStatus());
        assertEquals(oldMembersJson, savedBatches.get(first.batchId()).getMembersJson());
        EsFile oldFrozen = codec.decodeMembers(oldMembersJson).getFirst();
        assertEquals(oldSnapshot, oldFrozen.getRecycleStoragePath());
        assertEquals(CONTENT_SHA, oldFrozen.getRecycleSnapshotSha256());
        assertEquals(3L, oldFrozen.getRecycleSnapshotVersion());
        verify(storage).copyAt(PREFIX, file.getFilePath(), oldSnapshot);
        verify(storage).copyAt(PREFIX, oldSnapshot, file.getFilePath());
        verify(storage).copyAt(PREFIX, file.getFilePath(), newSnapshot);
        verify(storage).sha256At(PREFIX, newSnapshot);
        verify(ledger, times(2)).reserve(any(EsRecycleBatch.class));
        verify(batches, times(2)).insert(any(EsRecycleBatch.class));
        verify(files, never()).insert(any(EsFile.class));
        verify(storage, never()).deleteAt(anyString(), anyString());
    }

    private void prepareDelete(EsFile root) {
        when(fileService.resolveDeletePath(eq(SPACE), eq(OWNER), eq(root.getFileId()), nullable(String.class)))
            .thenReturn(root.getFilePath());
        when(fileService.lockActiveForPresign(SPACE, OWNER, root.getFilePath())).thenReturn(root);
    }

    private void prepareRestore(EsFile selected, EsRecycleBatch batch, List<EsFile> members) {
        batch.setMembersJson(codec.encodeMembers(members));
        savedBatches.put(batch.getBatchId(), copyBatch(batch));
        when(files.selectById(selected.getFileId())).thenReturn(selected);
        when(files.selectList(any())).thenAnswer(invocation -> new ArrayList<>(members));
    }

    private EsRecycleBatch copyBatch(EsRecycleBatch batch) {
        return batch == null ? null : json.readValue(json.writeValueAsString(batch), EsRecycleBatch.class);
    }

    private static EsFile row(long id, String path, boolean directory) {
        EsFile row = new EsFile();
        row.setFileId(id);
        row.setSpaceType(SPACE);
        row.setOwnerId(OWNER);
        row.setFilePath(path);
        row.setFileName(path.substring(path.lastIndexOf('/') + 1));
        row.setDirectory(directory);
        row.setFileSize(directory ? 0L : CONTENT.size());
        row.setVersionId(3L);
        row.setState(EsFile.STATE_ACTIVE);
        return row;
    }

    private static EsFile recycledRow(long id, String path, boolean directory, String batchId, boolean root) {
        EsFile row = row(id, path, directory);
        row.setState(EsFile.STATE_RECYCLED);
        row.setOriginPath(path);
        row.setRecycleBatchId(batchId);
        row.setRecycleOperationId(OPERATION);
        row.setRecycleRoot(root);
        row.setRecycleSnapshotVersion(3L);
        row.setRecycleSnapshotSize(directory ? 0L : CONTENT.size());
        row.setRecycleSnapshotEtag(directory ? null : CONTENT.etag());
        row.setRecycleSnapshotSha256(directory ? null : CONTENT_SHA);
        row.setRecycleSnapshotState("ready");
        if (!directory) { row.setRecycleStoragePath(DriveRecycleKeys.storagePathOf(batchId, id, 3L)); }
        return row;
    }

    private static EsRecycleBatch batch(String id, long rootId, String path, boolean directory, int count) {
        EsRecycleBatch batch = new EsRecycleBatch();
        batch.setBatchId(id);
        batch.setSpaceType(SPACE);
        batch.setOwnerId(OWNER);
        batch.setOperatorId(OWNER);
        batch.setOperationId(OPERATION);
        batch.setRootFileId(rootId);
        batch.setRootPath(path);
        batch.setDirectory(directory);
        batch.setExpectedVersion(3L);
        batch.setMemberCount(count);
        batch.setStatus("recycled");
        return batch;
    }

    private static String sha256(byte[] body) {
        try {
            return java.util.HexFormat.of().formatHex(java.security.MessageDigest.getInstance("SHA-256").digest(body));
        } catch (java.security.NoSuchAlgorithmException e) {
            throw new AssertionError(e);
        }
    }

    private static Long updateFileId(LambdaUpdateWrapper<?> wrapper) {
        String sql = wrapper.getSqlSegment();
        assertTrue(sql.contains("file_id") && sql.contains("state"), sql);
        assertTrue(wrapper.getSqlSet().contains("deleted_at") && wrapper.getSqlSet().contains("origin_path"));
        assertTrue(wrapper.getParamNameValuePairs().containsValue(EsFile.STATE_RECYCLED));
        assertTrue(wrapper.getParamNameValuePairs().containsValue(EsFile.STATE_ACTIVE));
        return wrapper.getParamNameValuePairs().values().stream().filter(Long.class::isInstance)
            .map(Long.class::cast).findFirst().orElseThrow();
    }
}
