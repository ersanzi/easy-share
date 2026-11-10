package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;
import lombok.EqualsAndHashCode;
import org.dromara.common.mybatis.core.domain.BaseEntity;

/**
 * 成员功能权限。
 * <p>
 * 一行只描述**被管理员改过**的账号；没有行的账号五项全开，由服务层按「缺行=全开」
 * 解释，避免新账号开出来就先锁死。字段与 {@code deploy/ruoyi-db/easyshare-user-permission.sql}
 * 一一对应。
 *
 * @author EasyShare
 */
@Data
@EqualsAndHashCode(callSuper = true)
@TableName("es_user_permission")
public class EsUserPermission extends BaseEntity {

    /** 账号 ID（sys_user.user_id）。刻意不做自增：与账号同生共死，一账号一行。 */
    @TableId(value = "user_id")
    private Long userId;

    /** 知识问答：知识库查询与入库 */
    private Boolean knowledge;

    /** 会议听记：录音转写与纪要 */
    private Boolean meeting;

    /** 网盘：个人空间文件读写 */
    private Boolean drive;

    /** 共享空间：需同时开启 drive */
    private Boolean shared;

    /** 插件中心：扩展的安装与使用 */
    private Boolean plugin;
}
