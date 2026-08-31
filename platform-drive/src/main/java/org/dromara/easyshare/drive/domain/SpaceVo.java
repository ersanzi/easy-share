package org.dromara.easyshare.drive.domain;

/**
 * 空间的对外视图：配额 + 实时已用量。
 * <p>
 * ownerId/spaceId 均为字符串——雪花 ID 超出 JS 安全整数范围，一路按字符串透传到前端。
 *
 * @param spaceId    空间 ID
 * @param spaceType  personal / shared
 * @param ownerId    归属账号 ID，共享空间为 "0"
 * @param spaceName  空间名称
 * @param quotaBytes 配额字节数：0 未分配、-1 不限
 * @param usedBytes  已用字节数（实时聚合）
 * @param status     0 正常、1 停用
 * @param permission 当前登录者对该空间的权限：owner / write / read / null
 * @author EasyShare
 */
public record SpaceVo(
    String spaceId,
    String spaceType,
    String ownerId,
    String spaceName,
    long quotaBytes,
    long usedBytes,
    String status,
    String permission
) {

    /**
     * 由实体与用量构造视图。
     *
     * @param space      空间实体
     * @param usedBytes  已用字节数
     * @param permission 当前登录者的权限
     * @return 视图
     */
    public static SpaceVo of(EsSpace space, long usedBytes, String permission) {
        return new SpaceVo(
            String.valueOf(space.getSpaceId()),
            space.getSpaceType(),
            String.valueOf(space.getOwnerId()),
            space.getSpaceName(),
            space.getQuotaBytes() == null ? 0L : space.getQuotaBytes(),
            usedBytes,
            space.getStatus(),
            permission);
    }

    /**
     * 未开通的个人空间占位：配额 0，客户端据此显示「待开空间」。
     *
     * @param ownerId 账号 ID
     * @return 视图
     */
    public static SpaceVo unset(Long ownerId) {
        return new SpaceVo(null, EsSpace.TYPE_PERSONAL, String.valueOf(ownerId),
            "个人空间", 0L, 0L, "0", "owner");
    }
}
