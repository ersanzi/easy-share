package org.dromara.easyshare.drive;

import org.springframework.context.annotation.Configuration;
import org.springframework.scheduling.annotation.EnableScheduling;

/**
 * 开启平台调度。
 *
 * <p>单独成类而不是挂到启动类上：platform-drive 是可选模块，
 * 调度开关跟着模块走，不依赖 ruoyi-admin 是否已经启用调度。
 *
 * @author EasyShare
 */
@Configuration
@EnableScheduling
public class DriveSchedulingConfig {
}
