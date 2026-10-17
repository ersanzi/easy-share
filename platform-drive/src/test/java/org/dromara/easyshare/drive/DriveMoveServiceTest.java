package org.dromara.easyshare.drive;

import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.service.DriveFileService;
import org.dromara.easyshare.drive.service.DriveMoveService;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.InOrder;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.doThrow;
import static org.mockito.Mockito.inOrder;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.verify;

/**
 * 改名/移动的编排与**补偿**测试。
 *
 * <p>补偿是「平时不执行、出事才执行」的代码。正因为平时不跑，它更需要测试：
 * 出错时用户已经在丢数据的边缘，没人会去手工审查这段逻辑。
 *
 * @author EasyShare
 */
class DriveMoveServiceTest {

    private DriveStorage storage;
    private DriveFileService fileService;
    private DriveMoveService service;

    @BeforeEach
    void setUp() {
        storage = mock(DriveStorage.class);
        fileService = mock(DriveFileService.class);
        service = new DriveMoveService(storage, fileService);
    }

    @Test
    void moveOrdersRowThenCopyThenDeleteSource() {
        // 顺序是正确性的一部分：
        //   行先 —— 用唯一索引占住目标名字（并发闸门）
        //   复制 —— 字节不出对象存储
        //   删源 —— 放最后，失败也不丢数据
        service.move(EsFile.TYPE_PERSONAL, 1L, "users/1/", "a.txt", "b.txt");

        InOrder order = inOrder(fileService, storage);
        order.verify(fileService).moveRegistered(EsFile.TYPE_PERSONAL, 1L, "a.txt", "b.txt");
        order.verify(storage).copyAt("users/1/", "a.txt", "b.txt");
        order.verify(storage).deleteAt("users/1/", "a.txt");
    }

    @Test
    void copyFailureRollsRowBackAndCleansTargetCopy() {
        // 复制失败：行必须挪回原位，否则会留下
        //   ① 新路径的幽灵行（登记了但没对象，点开 404）
        //   ② 旧路径有对象却没行 → 下次列表惰性补账成**新 fileId**，
        //      旧引用的分享链接/任务/知识索引全部失效（正是 KI-12 要解决的问题）
        ServiceException boom = new ServiceException("对象存储访问失败");
        doThrow(boom).when(storage).copyAt(anyString(), anyString(), anyString());

        ServiceException thrown = assertThrows(ServiceException.class,
            () -> service.move(EsFile.TYPE_PERSONAL, 1L, "users/1/", "a.txt", "b.txt"));
        assertEquals("对象存储访问失败", thrown.getMessage());

        // 行挪回原位
        verify(fileService).moveRegistered(EsFile.TYPE_PERSONAL, 1L, "b.txt", "a.txt");
        // 目标处可能已经有半份副本，要清掉
        verify(storage).deleteAt("users/1/", "b.txt");
        // 源**绝不能**删：复制都没成功，删源就是丢数据
        verify(storage, never()).deleteAt("users/1/", "a.txt");
    }

    @Test
    void deleteSourceFailureRollsRowBackAndRemovesCopiedTarget() {
        // 复制成功、删源失败：这也是「两份都在」的状态，必须补偿掉目标处的副本，
        // 否则用户会看到两个同名文件，其中一个还会被补账成新 fileId。
        ServiceException boom = new ServiceException("删除对象失败");
        doThrow(boom).when(storage).deleteAt(anyString(), eq("a.txt"));

        assertThrows(ServiceException.class,
            () -> service.move(EsFile.TYPE_PERSONAL, 1L, "users/1/", "a.txt", "b.txt"));

        verify(storage).copyAt("users/1/", "a.txt", "b.txt");
        verify(fileService).moveRegistered(EsFile.TYPE_PERSONAL, 1L, "b.txt", "a.txt");
        verify(storage).deleteAt("users/1/", "b.txt");
    }

    @Test
    void rollbackFailureDoesNotMaskOriginalError() {
        // 补偿本身失败时，原始错误（配额/权限/存储故障）对用户更有诊断价值，
        // 不能被补偿异常盖掉。补偿失败会留 error 日志供人工介入。
        ServiceException original = new ServiceException("配额不足");
        doThrow(original).when(storage).copyAt(anyString(), anyString(), anyString());
        doThrow(new ServiceException("rollback 也炸了"))
            .when(fileService).moveRegistered(EsFile.TYPE_PERSONAL, 1L, "b.txt", "a.txt");

        ServiceException thrown = assertThrows(ServiceException.class,
            () -> service.move(EsFile.TYPE_PERSONAL, 1L, "users/1/", "a.txt", "b.txt"));
        assertEquals("配额不足", thrown.getMessage(), "必须是原始错误，不是补偿错误");
    }

    @Test
    void occupiedTargetRejectsBeforeTouchingObjects() {
        // 目标被占用时在目录层就拒绝：**一个字节都不该复制**
        ServiceException conflict = new ServiceException("目标位置已有同名文件，未覆盖");
        doThrow(conflict).when(fileService)
            .moveRegistered(EsFile.TYPE_PERSONAL, 1L, "a.txt", "b.txt");

        assertThrows(ServiceException.class,
            () -> service.move(EsFile.TYPE_PERSONAL, 1L, "users/1/", "a.txt", "b.txt"));

        verify(storage, never()).copyAt(anyString(), anyString(), anyString());
        verify(storage, never()).deleteAt(anyString(), anyString());
    }
}
