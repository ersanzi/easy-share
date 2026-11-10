package org.dromara.easyshare.drive;

import cn.dev33.satoken.stp.StpUtil;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import lombok.RequiredArgsConstructor;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.service.FeaturePermissionService;
import org.springframework.web.servlet.HandlerInterceptor;

import java.io.IOException;

/**
 * 网盘能力的强制闸门：{@code /easyshare/drive/**} 的每个请求都要过「drive 开关」。
 * <p>
 * 不放在控制器方法上逐个加注解，是因为网盘入口二十多个、以后还会加——拦截器按路径
 * 一次覆盖，新增端点自动受管。共享空间另由 SpaceService 按 shared 开关裁决。
 * <p>
 * 未登录直接放行：登录校验由各控制器的 {@code @SaCheckLogin} 负责，这里若抢先返回
 * 403 会把「没登录」误导成「没权限」。
 *
 * @author EasyShare
 */
@RequiredArgsConstructor
public class DriveFeatureInterceptor implements HandlerInterceptor {

    private final FeaturePermissionService permissionService;

    @Override
    public boolean preHandle(HttpServletRequest request, HttpServletResponse response, Object handler)
        throws IOException {
        if (!StpUtil.isLogin()) {
            return true;
        }
        if (permissionService.enabled(LoginHelper.getUserId(), FeaturePermissionService.DRIVE)) {
            return true;
        }
        response.setStatus(HttpServletResponse.SC_FORBIDDEN);
        response.setCharacterEncoding("UTF-8");
        response.setContentType("application/json;charset=UTF-8");
        response.getWriter().write("{\"code\":403,\"msg\":\"你的账号未开通网盘功能，请联系管理员\"}");
        return false;
    }
}
