package org.dromara.easyshare.drive;

import lombok.RequiredArgsConstructor;
import org.dromara.easyshare.drive.service.FeaturePermissionService;
import org.springframework.context.annotation.Configuration;
import org.springframework.web.servlet.config.annotation.InterceptorRegistry;
import org.springframework.web.servlet.config.annotation.WebMvcConfigurer;

/**
 * 本模块的 MVC 扩展点：目前只注册网盘权限闸门。
 *
 * @author EasyShare
 */
@Configuration
@RequiredArgsConstructor
public class DriveWebConfig implements WebMvcConfigurer {

    private final FeaturePermissionService permissionService;

    @Override
    public void addInterceptors(InterceptorRegistry registry) {
        registry.addInterceptor(new DriveFeatureInterceptor(permissionService))
            .addPathPatterns("/easyshare/drive/**");
    }
}
