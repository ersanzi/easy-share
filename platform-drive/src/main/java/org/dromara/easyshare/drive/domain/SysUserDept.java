package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableName;
import com.baomidou.mybatisplus.annotation.TableId;
import lombok.Data;

/**
 * RuoYi 系统用户表的只读投影（部门级权限的部门归属来源）。
 * <p>
 * 刻意只映射授权判定需要的列：platform-drive 与 RuoYi 同进程同库，直接读系统表
 * 是最小侵入；写操作永远走 RuoYi 自己的管理链路，本表只读。
 *
 * @author EasyShare
 */
@Data
@TableName("sys_user")
public class SysUserDept {

    /**
     * 主键列必须显式标注。
     *
     * <p>**没有它 `selectById` 会直接抛错**（而不是返回 null）：
     * MyBatis-Plus 在「实体没有 @TableId」时无法为 `selectById` 生成
     * WHERE 子句，于是 SQL 语句根本没被注册，调用时报
     * {@code BindingException: Invalid bound statement (not found)}。
     *
     * <p>这个缺陷 **2026-10-16 才被发现**，而且**只有跑起真服务才暴露**：
     * 单测里 mapper 是 mock 的，`selectById` 返回 stub 值，永远不经过 MyBatis。
     * 实机症状是——凡是走到部门归属查询的接口全部 500：
     * `/easyshare/space/mine`、`/easyshare/drive/presign-get`（**下载任何文件**）。
     *
     * <p>对照：同包的 {@code SysConfigRow} 标了 {@code @TableId(value="config_id")}，
     * 所以它工作正常。这是「同一模式在一处做对、另一处漏掉」。
     */
    @TableId(value = "user_id")
    private Long userId;

    private Long deptId;

    private String userName;

    private String nickName;
}
