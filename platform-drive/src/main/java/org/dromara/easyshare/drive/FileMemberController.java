package org.dromara.easyshare.drive;

import cn.dev33.satoken.annotation.SaCheckLogin;
import jakarta.validation.constraints.NotEmpty;
import jakarta.validation.constraints.NotNull;
import lombok.RequiredArgsConstructor;
import org.dromara.common.core.domain.R;
import org.dromara.common.satoken.utils.LoginHelper;
import org.dromara.easyshare.drive.domain.EsFileMember;
import org.dromara.easyshare.drive.service.FileRoleService;
import org.springframework.validation.annotation.Validated;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PutMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

import java.util.List;

/**
 * 文档级成员角色的读写入口（企业权限第三片）。
 * <p>
 * 只有上传者与 manage 档成员能读写（服务端 {@link FileRoleService} 按四档梯子裁决）；
 * 护栏与钉钉对齐：不能修改自己的角色行。这些端点不受功能矩阵管辖——
 * 管理动作不能被功能开关误伤，与 {@code /easyshare/permission/*} 同口径。
 * <p>
 * 只针对共享空间文件；个人空间 owner 独占，不需要成员角色。
 *
 * @author EasyShare
 */
@Validated
@SaCheckLogin
@RestController
@RequiredArgsConstructor
@RequestMapping("/easyshare/file-member")
public class FileMemberController {

    private final FileRoleService fileRoleService;

    /**
     * 某文件的成员角色行（含档位语义，管理面板渲染用）。
     *
     * @param fileId 文件 ID
     * @return 成员行
     */
    @GetMapping("/{fileId}/members")
    public R<List<EsFileMember>> members(@PathVariable Long fileId) {
        return R.ok(fileRoleService.membersOf(fileId, LoginHelper.getUserId()));
    }

    /**
     * 整组替换成员角色行。操作者已有的用户行原样保留（授权者不能刷掉自己）；
     * 请求里出现操作者自己的用户行会被拒绝（不能修改自己的角色）。
     *
     * @param fileId 文件 ID
     * @param body   成员行集合
     * @return 空响应
     */
    @PutMapping("/{fileId}/members")
    public R<Void> replace(@PathVariable Long fileId,
                           @RequestBody List<MemberBo> body) {
        Long userId = LoginHelper.getUserId();
        List<EsFileMember> rows = body == null ? List.of() : body.stream()
            .map(bo -> {
                EsFileMember member = new EsFileMember();
                member.setFileId(fileId);
                member.setMemberType(bo.memberType());
                member.setMemberId(bo.memberId());
                member.setRole(bo.role());
                return member;
            })
            .toList();
        fileRoleService.replaceMembers(fileId, rows, userId);
        return R.ok();
    }

    /**
     * 删除单条成员行（不能删自己的行）。
     *
     * @param fileId     文件 ID
     * @param memberType user / dept
     * @param memberId   主体 ID
     * @return 空响应
     */
    @DeleteMapping("/{fileId}/members/{memberType}/{memberId}")
    public R<Void> remove(@PathVariable Long fileId, @PathVariable String memberType,
                          @PathVariable Long memberId) {
        fileRoleService.removeMember(fileId, memberType, memberId, LoginHelper.getUserId());
        return R.ok();
    }

    /**
     * 成员行请求体。memberType 用字符串（user/dept），memberId 雪花 ID 数字。
     *
     * @param memberType user / dept
     * @param memberId   sys_user.user_id 或 sys_dept.dept_id
     * @param role       manage / edit / download / read
     */
    public record MemberBo(
        @NotEmpty String memberType,
        @NotNull Long memberId,
        @NotEmpty String role
    ) {
    }
}
