package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;

/**
 * RuoYi 系统配置表（sys_config）的投影。
 * <p>
 * 租户级服务配置（知识服务地址等）复用 sys_config 存储——不建新表，部署免 DDL；
 * 读取/写入都走本模块端点（读全员可读、写仅超管），不经 RuoYi 的配置缓存，
 * 两边口径一致：控制面后台改动本模块立即可见，本模块改动对 RuoYi 后台透明。
 *
 * @author EasyShare
 */
@Data
@TableName("sys_config")
public class SysConfigRow {

    @TableId(value = "config_id")
    private Long configId;

    private String configName;

    private String configKey;

    private String configValue;

    /** Y 系统内置 / N 非内置（RuoYi 口径，仅作标注） */
    private String configType;

    private String remark;
}
