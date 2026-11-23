package org.dromara.easyshare.drive;

import cn.dev33.satoken.annotation.SaCheckLogin;
import lombok.RequiredArgsConstructor;
import org.dromara.common.core.domain.R;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.service.KnowledgeAclService;
import org.springframework.web.bind.annotation.*;
import java.util.List;

/** 知识来源授权专用入口，身份只取登录会话，不接受调用方用户标识。 */
@SaCheckLogin
@RestController
@RequiredArgsConstructor
@RequestMapping("/easyshare/knowledge/acl")
public class KnowledgeAclController {
    private final KnowledgeAclService service;
    public record AccessBo(List<String> fileIds) {}
    public record BindBo(String fileId, String expectedVersionId, String objectKey, String action) {}

    @PostMapping("/bind")
    public R<KnowledgeAclService.BindingVo> bind(@RequestBody BindBo body) {
        return R.ok(service.bind(body, LoginHelper.getUserId()));
    }

    @PostMapping("/access")
    public R<List<KnowledgeAclService.AccessVo>> access(@RequestBody AccessBo body) {
        return R.ok(service.access(body == null ? null : body.fileIds(), LoginHelper.getUserId()));
    }
}
