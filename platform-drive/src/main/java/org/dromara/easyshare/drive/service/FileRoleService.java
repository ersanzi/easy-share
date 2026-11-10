package org.dromara.easyshare.drive.service;

import lombok.RequiredArgsConstructor;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.DriveObject;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.domain.EsFileMember;
import org.dromara.easyshare.drive.domain.EsSpace;
import org.dromara.easyshare.drive.domain.EsSpaceMember;
import org.dromara.easyshare.drive.domain.SysDeptRow;
import org.dromara.easyshare.drive.domain.SysUserDept;
import org.dromara.easyshare.drive.mapper.EsFileMapper;
import org.dromara.easyshare.drive.mapper.EsFileMemberMapper;
import org.dromara.easyshare.drive.mapper.SysDeptRowMapper;
import org.dromara.easyshare.drive.mapper.SysUserDeptMapper;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Set;

/**
 * 文档级成员角色：四档梯子**一处收口**，列表过滤与动作拦截共用同一份裁决。
 * <p>
 * 裁决顺序（{@link #ladder}，企业权限第三片，参照钉钉文档权限模型
 * docs/intelligence/2026-11-05-dingtalk-docs-permission-reverse.md）：
 * <ol>
 *   <li>上传者恒 manage（隐式，不落表——今天上传者可改可见性，升级为全权）；</li>
 *   <li>{@code es_file_member} 成员行：user 行与 dept 行取高，覆盖一切默认；</li>
 *   <li>{@code es_file.visible_depts} 命中本人部门 → download（既有语义：可见即可下载）；</li>
 *   <li>文件未限制可见性时按空间权限取默认：write 成员 manage / read 成员 download——
 *       即「未配任何成员角色的文件，行为与引入本表之前完全一致」；</li>
 *   <li>其余不可见。</li>
 * </ol>
 * 护栏（对齐钉钉错误码语义 52610016/52610018）：不能修改自己的角色行；
 * 仅上传者与 manage 档可授权。删除是 manage 独占，改名/上传新版 edit 起步。
 *
 * @author EasyShare
 */
@Service
@RequiredArgsConstructor
public class FileRoleService {

    /** 可管理：预览/下载/改名/删除/分享/上传新版/授权他人/改可见性 */
    public static final String ROLE_MANAGE = "manage";

    /** 可编辑：预览/下载/上传新版/改名/分享（删除是 manage 独占） */
    public static final String ROLE_EDIT = "edit";

    /** 可查看下载：预览/下载 */
    public static final String ROLE_DOWNLOAD = "download";

    /** 仅可查看：仅预览，不能下载 */
    public static final String ROLE_READ = "read";

    private final EsFileMemberMapper memberMapper;
    private final EsFileMapper fileMapper;
    private final SpaceService spaceService;
    private final SysUserDeptMapper sysUserDeptMapper;
    private final SysDeptRowMapper sysDeptRowMapper;

    /** 档位高低（比较用）：manage > edit > download > read；未知档位按 0 处理（宁少勿错） */
    public static int rank(String role) {
        return switch (role == null ? "" : role) {
            case ROLE_MANAGE -> 4;
            case ROLE_EDIT -> 3;
            case ROLE_DOWNLOAD -> 2;
            case ROLE_READ -> 1;
            default -> 0;
        };
    }

    /**
     * 生效档位（纯函数核心；{@code row} 必须是共享空间的 active 行）。
     * 未登记的行（{@code row == null}）不走本方法——调用方按「可见、空间级权限」处理，
     * 与既有「目录层没有行不拦」的口径一致。
     *
     * @param row             文件行（共享空间）
     * @param userId          查看者账号 ID
     * @param deptId          查看者部门 ID（可空）
     * @param spacePermission 查看者对共享空间的权限（write/read/null）
     * @param userRoleRow     该查看者在本文件上的用户行角色（可空）
     * @param deptRoleRow     查看者部门在本文件上的部门行角色（可空）
     * @return manage/edit/download/read；null = 不可见
     */
    public static String ladder(EsFile row, Long userId, Long deptId, String spacePermission,
                                String userRoleRow, String deptRoleRow) {
        if (row == null || !EsSpace.TYPE_SHARED.equals(row.getSpaceType())) {
            return null;
        }
        // 1. 上传者恒 manage（存量补账行 uploadBy=0 不认，避免「全网上传者」）
        if (userId != null && row.getUploadBy() != null && row.getUploadBy() > 0
            && row.getUploadBy().equals(userId)) {
            return ROLE_MANAGE;
        }
        // 2. 显式成员行覆盖一切默认（user 行与 dept 行取高，与空间权限 widen 取宽同向）
        String explicit = higher(userRoleRow, deptRoleRow);
        if (explicit != null) {
            return explicit;
        }
        boolean restricted = row.getVisibleDepts() != null && !row.getVisibleDepts().isBlank();
        if (restricted) {
            // 3. 受限文件：部门命中 → download（今天语义：可见即可下载）；无行未命中 → 不可见
            if (deptId != null && DriveFileService.deptListContains(row.getVisibleDepts(), deptId)) {
                return ROLE_DOWNLOAD;
            }
            return null;
        }
        // 4. 未限制文件按空间权限取默认——「未配角色 = 行为与今天完全一致」的兼容前提
        if (EsSpaceMember.PERM_WRITE.equals(spacePermission)) {
            return ROLE_MANAGE;
        }
        if (spacePermission != null) {
            return ROLE_DOWNLOAD;
        }
        return null;
    }

    /** 两档取高；同档取任一（null 与 null 还是 null） */
    private static String higher(String a, String b) {
        if (a == null) {
            return b;
        }
        if (b == null) {
            return a;
        }
        return rank(a) >= rank(b) ? a : b;
    }

    /** 查看者对某共享空间文件的生效档位；null = 不可见 */
    public String effective(EsFile row, Long userId) {
        if (row == null || userId == null) {
            return null;
        }
        Long deptId = spaceService.deptIdOf(userId);
        return ladder(row, userId, deptId, spaceService.sharedPermissionOf(userId),
            memberMapper.selectRole(row.getFileId(), EsFileMember.TYPE_USER, userId),
            deptId != null && deptId > 0
                ? memberMapper.selectRole(row.getFileId(), EsFileMember.TYPE_DEPT, deptId)
                : null);
    }

    /** 查看者能否看到某文件（列表过滤与下载授权共用的可见性判据） */
    public boolean visible(EsFile row, Long userId) {
        return effective(row, userId) != null;
    }

    /**
     * 共享列表过滤（唯一列举出口）：生效档位非空即可见。
     * 未登记的行按可见处理（与既有「未登记不拦」口径一致）。
     * 成员行按查看者各查一次（用户行 + 部门行），整批共用，不逐文件查库。
     */
    public List<DriveObject> filterSharedVisible(List<DriveObject> objects,
                                                 Map<String, EsFile> rowsByPath, Long userId) {
        Long deptId = spaceService.deptIdOf(userId);
        String spacePermission = spaceService.sharedPermissionOf(userId);
        Map<Long, String> userRows = new HashMap<>();
        for (EsFileMember member : memberMapper.selectUserRowsOf(userId)) {
            userRows.put(member.getFileId(), member.getRole());
        }
        Map<Long, String> deptRows = new HashMap<>();
        if (deptId != null && deptId > 0) {
            for (EsFileMember member : memberMapper.selectDeptRowsOf(deptId)) {
                deptRows.put(member.getFileId(), member.getRole());
            }
        }
        List<DriveObject> visible = new ArrayList<>(objects.size());
        for (DriveObject object : objects) {
            EsFile row = rowsByPath.get(object.path());
            if (row == null
                || ladder(row, userId, deptId, spacePermission,
                    userRows.get(row.getFileId()), deptRows.get(row.getFileId())) != null) {
                visible.add(object);
            }
        }
        return visible;
    }

    /**
     * 下载/预览签发授权：档位必须 ≥ download（read 档仅预览在线内容，拿不到文件本体）。
     *
     * @throws ServiceException read 档或不可见
     */
    public void assertDownloadable(EsFile row, Long userId) {
        String role = effective(row, userId);
        if (role == null) {
            throw new ServiceException("文件不存在或无权访问");
        }
        if (rank(role) < rank(ROLE_DOWNLOAD)) {
            throw new ServiceException("当前权限仅可在线预览，不能下载");
        }
    }

    /** 删除/授权门槛：manage 档（未登记行不拦，沿用空间级写权限口径） */
    public void assertManage(EsFile row, Long userId) {
        if (row == null) {
            return;
        }
        if (!ROLE_MANAGE.equals(effective(row, userId))) {
            throw new ServiceException("只有可管理权限的成员可以执行此操作");
        }
    }

    /** 改名/上传新版门槛：edit 起步 */
    public void assertEditable(EsFile row, Long userId) {
        if (row == null) {
            return;
        }
        if (rank(effective(row, userId)) < rank(ROLE_EDIT)) {
            throw new ServiceException("当前权限不能修改此文件，需要可编辑及以上权限");
        }
    }

    /** 改可见性门槛：上传者恒可（既有行为），其余按 manage 档 */
    public void assertVisibilityManageable(EsFile row, Long userId) {
        if (row == null) {
            return;
        }
        if (userId != null && row.getUploadBy() != null && row.getUploadBy() > 0
            && row.getUploadBy().equals(userId)) {
            return;
        }
        assertManage(row, userId);
    }

    // ── 成员角色管理（授权面） ──────────────────────────────────────

    /** 某文件的成员角色行（manage 门） */
    public List<EsFileMember> membersOf(Long fileId, Long operatorId) {
        requireManageable(fileId, operatorId);
        return memberMapper.selectByFile(fileId);
    }

    /**
     * 整组替换成员角色行（manage 门）。
     * <p>
     * 护栏：不能修改操作者自己的用户行（对齐钉钉 52610018）——显式拿到 manage 档的
     * 成员若能给自己改行，等于可自我提权；上传者虽是隐式 manage 不受行影响，
     * 仍按同一规则拒绝，口径只有一条。操作者已有的用户行在整组替换中**原样保留**。
     *
     * @param fileId     文件 ID（共享空间 active 行）
     * @param members    新的成员行集合（整组替换）
     * @param operatorId 操作者
     */
    @Transactional(rollbackFor = Exception.class)
    public void replaceMembers(Long fileId, List<EsFileMember> members, Long operatorId) {
        requireManageable(fileId, operatorId);
        String keepOperatorRole = memberMapper.selectRole(fileId, EsFileMember.TYPE_USER, operatorId);
        Set<String> seen = new HashSet<>();
        List<EsFileMember> rows = new ArrayList<>();
        for (EsFileMember member : members == null ? List.<EsFileMember>of() : members) {
            validateRole(member.getRole());
            // 自改护栏先于主体存在性校验：护栏是授权语义，不该被「主体查不到」抢先后掩盖
            if (EsFileMember.TYPE_USER.equals(member.getMemberType())
                && Objects.equals(operatorId, member.getMemberId())) {
                throw new ServiceException("不能修改自己的角色");
            }
            validateSubject(member);
            if (!seen.add(member.getMemberType() + ":" + member.getMemberId())) {
                throw new ServiceException("同一主体重复出现，请合并为一行");
            }
            rows.add(member);
        }
        memberMapper.deleteByFile(fileId);
        for (EsFileMember member : rows) {
            memberMapper.insertRow(member.getFileId(), member.getMemberType(),
                member.getMemberId(), member.getRole());
        }
        // 操作者既有行原样保留：整组替换不该把授权者自己刷掉
        if (keepOperatorRole != null) {
            memberMapper.insertRow(fileId, EsFileMember.TYPE_USER, operatorId, keepOperatorRole);
        }
    }

    /** 删除单条成员行（manage 门 + 不能删自己的行） */
    public void removeMember(Long fileId, String memberType, Long memberId, Long operatorId) {
        requireManageable(fileId, operatorId);
        if (EsFileMember.TYPE_USER.equals(memberType) && Objects.equals(operatorId, memberId)) {
            throw new ServiceException("不能修改自己的角色");
        }
        memberMapper.deleteRow(fileId, memberType, memberId);
    }

    /** 文件删除（purge）时随行清理，防孤儿行 */
    public void purgeWithFile(Long fileId) {
        memberMapper.deleteByFile(fileId);
    }

    private EsFile requireManageable(Long fileId, Long operatorId) {
        EsFile row = fileId == null ? null : fileMapper.selectById(fileId);
        if (row == null || !EsFile.STATE_ACTIVE.equals(row.getState())) {
            throw new ServiceException("文件不存在或已删除");
        }
        if (!EsSpace.TYPE_SHARED.equals(row.getSpaceType())) {
            throw new ServiceException("个人空间文件不需要成员角色");
        }
        if (!ROLE_MANAGE.equals(effective(row, operatorId))) {
            throw new ServiceException("只有可管理权限的成员可以授权");
        }
        return row;
    }

    private static void validateRole(String role) {
        if (role == null || rank(role) == 0) {
            throw new ServiceException("未知的角色档位：" + role);
        }
    }

    /** 主体存在性校验：账号须存在且未删除，部门须存在 */
    private void validateSubject(EsFileMember member) {
        if (member.getMemberId() == null || member.getMemberId() <= 0) {
            throw new ServiceException("成员标识非法");
        }
        if (EsFileMember.TYPE_USER.equals(member.getMemberType())) {
            SysUserDept user = sysUserDeptMapper.selectById(member.getMemberId());
            if (user == null || "2".equals(user.getDelFlag())) {
                throw new ServiceException("账号不存在或已删除");
            }
        } else if (EsFileMember.TYPE_DEPT.equals(member.getMemberType())) {
            SysDeptRow dept = sysDeptRowMapper.selectById(member.getMemberId());
            if (dept == null || "2".equals(dept.getDelFlag())) {
                throw new ServiceException("部门不存在或已删除");
            }
        } else {
            throw new ServiceException("未知的主体类型：" + member.getMemberType());
        }
    }
}
