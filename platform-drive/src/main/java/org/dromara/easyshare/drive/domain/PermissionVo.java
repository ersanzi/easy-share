package org.dromara.easyshare.drive.domain;

/**
 * 一个账号的功能权限快照。
 * <p>
 * 管理页按这份结构画开关矩阵，客户端登录后按 {@code /easyshare/permission/mine}
 * 的同一结构决定入口显隐——两处共用一种形状，避免「管理页开的」和「用户看到的」对不上。
 *
 * @param userId    账号 ID
 * @param knowledge 知识问答
 * @param meeting   会议听记
 * @param drive     网盘
 * @param shared    共享空间
 * @param plugin    插件中心
 * @author EasyShare
 */
public record PermissionVo(
    Long userId,
    boolean knowledge,
    boolean meeting,
    boolean drive,
    boolean shared,
    boolean plugin
) {

    /** 缺行账号的默认值：五项全开 */
    public static PermissionVo all(Long userId) {
        return new PermissionVo(userId, true, true, true, true, true);
    }

    /**
     * 角色并集：成员挂多个角色时取最宽（任一角色开启即开启）。
     * <p>
     * 与逐账号矩阵不同，角色是「正向授权」：角色里没开的项不会因为另一个角色开了
     * 而变成关闭——后端开关只有开/关两态，收窄一律靠调整角色本身。
     *
     * @param userId 账号 ID
     * @param roles  角色列表（非空）
     * @return 并集快照
     */
    public static PermissionVo union(Long userId, java.util.List<EsPermissionRole> roles) {
        boolean knowledge = false;
        boolean meeting = false;
        boolean drive = false;
        boolean shared = false;
        boolean plugin = false;
        for (EsPermissionRole role : roles) {
            knowledge |= !Boolean.FALSE.equals(role.getKnowledge());
            meeting |= !Boolean.FALSE.equals(role.getMeeting());
            drive |= !Boolean.FALSE.equals(role.getDrive());
            shared |= !Boolean.FALSE.equals(role.getShared());
            plugin |= !Boolean.FALSE.equals(role.getPlugin());
        }
        return new PermissionVo(userId, knowledge, meeting, drive, shared, plugin);
    }

    public static PermissionVo of(EsUserPermission row) {
        return new PermissionVo(
            row.getUserId(),
            !Boolean.FALSE.equals(row.getKnowledge()),
            !Boolean.FALSE.equals(row.getMeeting()),
            !Boolean.FALSE.equals(row.getDrive()),
            !Boolean.FALSE.equals(row.getShared()),
            !Boolean.FALSE.equals(row.getPlugin()));
    }
}
