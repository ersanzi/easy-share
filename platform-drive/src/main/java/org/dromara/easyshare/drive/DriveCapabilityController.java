package org.dromara.easyshare.drive;

import cn.dev33.satoken.annotation.SaCheckLogin;
import org.dromara.common.core.domain.R;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

/** 云盘协议能力显式协商；旧服务端的空清单不能冒充目录支持。 */
@SaCheckLogin
@RestController
@RequestMapping("/easyshare/drive")
public class DriveCapabilityController {
    @GetMapping("/capabilities")
    public R<Capabilities> capabilities() {
        return R.ok(new Capabilities(true, 1));
    }

    public record Capabilities(boolean persistentDirectories, int protocolVersion) {
    }
}
