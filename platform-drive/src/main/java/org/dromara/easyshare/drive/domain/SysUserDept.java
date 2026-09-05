package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableName;
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

    private Long userId;

    private Long deptId;

    private String userName;

    private String nickName;
}
