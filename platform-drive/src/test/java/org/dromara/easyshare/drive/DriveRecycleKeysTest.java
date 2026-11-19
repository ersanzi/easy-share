package org.dromara.easyshare.drive;

import org.dromara.common.core.exception.ServiceException;
import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.*;

/** 纯函数隔离测试：不连接 DB/RustFS，不运行回收站清理。 */
class DriveRecycleKeysTest {
    @Test
    void stableRelativeKeySupportsLegacyZeroAndSeparatesDeleteBatches() {
        assertEquals(".recycle/batch_01/7/v0", DriveRecycleKeys.storagePathOf("batch_01", 7L, 0L));
        assertEquals(".recycle/batch_01/7/v3", DriveRecycleKeys.storagePathOf("batch_01", 7L, 3L));
        assertNotEquals(DriveRecycleKeys.storagePathOf("a", 7L, 3L),
            DriveRecycleKeys.storagePathOf("b", 7L, 3L));
    }

    @Test
    void rejectsTraversalAndInvalidIdsWithoutTrimmingOrNormalization() {
        for (String batch : new String[]{null, "", "..", "../x", "a/b", "a\\b", "a.b", " a", "a\n", "a".repeat(65)}) {
            assertThrows(ServiceException.class, () -> DriveRecycleKeys.storagePathOf(batch, 7L, 0L));
        }
        for (Long id : new Long[]{null, 0L, -1L}) {
            assertThrows(ServiceException.class, () -> DriveRecycleKeys.storagePathOf("a", id, 0L));
        }
        for (Long version : new Long[]{null, -1L}) {
            assertThrows(ServiceException.class, () -> DriveRecycleKeys.storagePathOf("a", 7L, version));
        }
    }

    @Test
    void exactOwnershipRejectsNeighborBatchFileVersionAndAbsoluteKeys() {
        String owned = DriveRecycleKeys.storagePathOf("a", 7L, 3L);
        assertEquals(owned, DriveRecycleKeys.requireOwnedPath(owned, "a", 7L, 3L));
        for (String other : new String[]{null, ".recycle/b/7/v3", ".recycle/a/8/v3", ".recycle/a/7/v4",
            "users/1/" + owned, ".recycle/a/7/../7/v3", owned + "/", ".recycle//a/7/v3"}) {
            assertThrows(ServiceException.class, () -> DriveRecycleKeys.requireOwnedPath(other, "a", 7L, 3L));
        }
    }
}
