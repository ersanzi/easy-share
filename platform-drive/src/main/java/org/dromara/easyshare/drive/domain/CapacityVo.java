package org.dromara.easyshare.drive.domain;

/**
 * 容量总览：管理员据此判断是否超配。
 *
 * @param enabled       是否启用了池上限（未配置探测路径时为 false）
 * @param usableBytes   物理可用字节数，未启用时为 -1
 * @param poolBytes     扣除预留水位后可用于云盘的字节数，未启用时为 -1
 * @param reservedBytes 预留水位
 * @param committedBytes 已承诺总量：各空间配额之和（不含「不限」的空间）
 * @param usedBytes     实际总用量（实时聚合）
 * @param unlimitedCount 配额为「不限」的空间数——它们无法计入已承诺总量
 * @author EasyShare
 */
public record CapacityVo(
    boolean enabled,
    long usableBytes,
    long poolBytes,
    long reservedBytes,
    long committedBytes,
    long usedBytes,
    int unlimitedCount
) {
}
