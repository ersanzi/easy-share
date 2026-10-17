package org.dromara.easyshare.drive;

import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import lombok.RequiredArgsConstructor;
import org.dromara.easyshare.drive.domain.EsShare;
import org.dromara.easyshare.drive.domain.EsSpace;
import org.dromara.easyshare.drive.service.ShareService;
import org.dromara.easyshare.drive.service.SpaceService;
import org.springframework.stereotype.Component;
import org.springframework.web.filter.OncePerRequestFilter;

import java.io.IOException;

/**
 * 公开分享下载：{@code GET /easyshare/s/{token}} → 302 到对象存储的预签名 URL。
 *
 * <p><b>为什么放在 Filter 而不是 Controller</b>：本项目的鉴权是 Sa-Token 的
 * {@code SaRouter.match(所有URL)} 全局拦截，{@code @SaIgnore} 在这种模式下不生效，
 * 免登录路径只能配在 {@code security.excludes}（位于 gitignore 的 RuoYi 工程里）。
 * Servlet Filter 天然早于 MVC 拦截器，放在这一层既不依赖部署时的外部配置，
 * 也把「公开入口」收敛在一个只有 40 行的类里，审计时好找。
 *
 * <p><b>安全取舍</b>：
 * <ul>
 *   <li>token 不存在 / 已撤销 / 已过期一律返回同一个 404，不区分原因——避免被用来探测 token 有效性。</li>
 *   <li>只允许 GET；访问计数在跳转前 +1（302 计数，不是精确审计）。</li>
 *   <li>分享者的共享空间权限被收回时返回 403，不降级到个人空间前缀。</li>
 * </ul>
 *
 * @author EasyShare
 */
@Component
@RequiredArgsConstructor
public class ShareDownloadFilter extends OncePerRequestFilter {

    /** 公开路径前缀；不带 context-path，用 servletPath 比对 */
    static final String PATH_PREFIX = "/easyshare/s/";

    private final ShareService shareService;
    private final SpaceService spaceService;
    private final DriveStorage storage;

    @Override
    protected void doFilterInternal(HttpServletRequest request, HttpServletResponse response,
                                    FilterChain chain) throws ServletException, IOException {
        String path = request.getServletPath();
        if (path == null || !path.startsWith(PATH_PREFIX)) {
            chain.doFilter(request, response);
            return;
        }
        if (!"GET".equalsIgnoreCase(request.getMethod())) {
            response.sendError(HttpServletResponse.SC_METHOD_NOT_ALLOWED);
            return;
        }
        String token = path.substring(PATH_PREFIX.length());
        if (token.isEmpty() || token.length() > 32) {
            response.sendError(HttpServletResponse.SC_NOT_FOUND);
            return;
        }

        EsShare share = shareService.resolve(token);
        if (share == null) {
            // 不存在 / 已撤销 / 已过期 → 统一 404，不泄漏是哪一种
            response.sendError(HttpServletResponse.SC_NOT_FOUND);
            return;
        }

        String prefix;
        try {
            prefix = EsSpace.TYPE_SHARED.equals(share.getSpaceType())
                ? spaceService.checkSharedReadable(share.getOwnerId())
                : DriveKeys.userPrefix(share.getOwnerId());
        } catch (RuntimeException ex) {
            // 分享者已被移出共享空间：拒绝，而不是退回个人空间前缀
            response.sendError(HttpServletResponse.SC_FORBIDDEN);
            return;
        }

        String url = storage.presignGetAt(prefix, share.getFilePath());
        shareService.recordVisit(share.getShareId());
        response.setStatus(HttpServletResponse.SC_FOUND);
        response.setHeader("Location", url);
    }
}
