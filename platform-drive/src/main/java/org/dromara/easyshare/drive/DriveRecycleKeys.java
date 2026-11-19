package org.dromara.easyshare.drive;

import org.dromara.common.core.exception.ServiceException;

/** 回收站键仅使用服务端批次和稳定身份；空间前缀由登录态另行确定。 */
public final class DriveRecycleKeys {
    public static final String STORAGE_DIR = ".recycle/";

    private DriveRecycleKeys() {
    }

    /** 批次/操作标识允许 UUID 或安全 ASCII token，绝不接受路径片段。 */
    public static String requireToken(String value) {
        if (value == null || !value.matches("[A-Za-z0-9][A-Za-z0-9_-]{0,63}")) {
            throw new ServiceException("回收站批次或操作标识无效");
        }
        return value;
    }

    /** 返回空间内相对键；v0 兼容旧版本，fileId 必须为正数。 */
    public static String storagePathOf(String batchId, Long fileId, Long versionId) {
        requireToken(batchId);
        if (fileId == null || fileId <= 0L || versionId == null || versionId < 0L) {
            throw new ServiceException("回收站文件身份或版本无效");
        }
        return STORAGE_DIR + batchId + "/" + fileId + "/v" + versionId;
    }

    /** 读/清理快照前必须精确核对归属；不规范化外来键以免接受穿越/邻居对象。 */
    public static String requireOwnedPath(String candidate, String batchId, Long fileId, Long versionId) {
        String expected = storagePathOf(batchId, fileId, versionId);
        if (!expected.equals(candidate)) {
            throw new ServiceException("回收站快照不属于该文件和删除批次");
        }
        return expected;
    }
}
