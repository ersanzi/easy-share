package account

import (
	"context"
	"fmt"
	"strings"
)

// FeatureFlags 是一个账号的五项功能开关，与控制面 es_user_permission 一一对应。
//
// 缺行账号在控制面按「全开」返回，所以这里拿到的永远是一个完整的判断依据；
// 旧版控制面没有这个接口时调用方回落到 AllFeatures()——不隐藏入口，
// 服务端仍会按真实配置拦。挂了权限角色的账号，控制面返回的是角色并集。
type FeatureFlags struct {
	// Admin 接口会带 userId；/mine 也有，客户端不必关心，仅用于管理页回写
	UserID    string `json:"userId,omitempty"`
	Knowledge bool   `json:"knowledge"`
	Meeting   bool   `json:"meeting"`
	Drive     bool   `json:"drive"`
	Shared    bool   `json:"shared"`
	Plugin    bool   `json:"plugin"`
}

// 管理范围：决定「管理」入口是否显示，以及管理页能看到哪些成员。
// 档位挂在权限角色上（es_permission_role.admin_scope），多角色取并集；
// 成员裁剪在控制面服务端做，客户端只消费档位做入口显隐与徽标展示。
const (
	// AdminScopeAll 全部成员（含超管）。
	AdminScopeAll = "all"
	// AdminScopeDeptChildren 子管理员：本部门及下级部门。
	AdminScopeDeptChildren = "dept_children"
	// AdminScopeDept 子管理员：只管自己部门（精确匹配）的成员。
	AdminScopeDept = "dept"
	// AdminScopeSpecified 子管理员：管角色上勾选的指定部门。
	AdminScopeSpecified = "specified"
	// AdminScopeNone 普通成员：没有管理入口。
	AdminScopeNone = "none"
)

// AllFeatures 五项全开。
func AllFeatures() FeatureFlags {
	return FeatureFlags{Knowledge: true, Meeting: true, Drive: true, Shared: true, Plugin: true}
}

// HasAdminEntry 判断管理范围档位是否授予「管理」入口：非 none 且非空即有
// （dept / dept_children / specified / all 都是子管理员档位，真实成员范围由控制面裁剪）。
// 空串表示未知（旧控制面或拉取失败），返回 false——调用方按 isAdmin 走老口径。
func HasAdminEntry(scope string) bool {
	switch scope {
	case AdminScopeDept, AdminScopeDeptChildren, AdminScopeSpecified, AdminScopeAll:
		return true
	default:
		return false
	}
}

// MyPermission 是 /easyshare/permission/mine 的应答：五项生效开关 + 管理范围 + 角色名。
//
// AdminScope 为空表示**未知**（旧版控制面没有这个字段，或本次拉取失败）：
// 此时入口显隐沿用 localStorage 之外的老口径（只看 isAdmin），不要当成 none。
type MyPermission struct {
	FeatureFlags
	AdminScope string   `json:"adminScope"`
	Roles      []string `json:"roles"`
}

// MyPermission 读当前账号的生效权限与管理范围。
func (c *Client) MyPermission(ctx context.Context, token string) (*MyPermission, error) {
	var permission MyPermission
	if err := c.getJSON(ctx, "/easyshare/permission/mine", token, &permission); err != nil {
		return nil, err
	}
	return &permission, nil
}

// AdminMember 是管理页成员矩阵的一行：身份 + 生效权限 + 所属角色。
//
// 五项开关是生效值（角色并集 / 逐账号行 / 缺行全开）；RoleIDs 非空时开关只读，
// 要改就改角色——与服务端「角色优先」的裁决一致。
type AdminMember struct {
	UserID     string   `json:"userId"`
	UserName   string   `json:"userName"`
	NickName   string   `json:"nickName"`
	DeptID     string   `json:"deptId,omitempty"`
	DeptName   string   `json:"deptName"`
	Status     string   `json:"status"`
	Knowledge  bool     `json:"knowledge"`
	Meeting    bool     `json:"meeting"`
	Drive      bool     `json:"drive"`
	Shared     bool     `json:"shared"`
	Plugin     bool     `json:"plugin"`
	RoleIDs    []string `json:"roleIds"`
	RoleNames  []string `json:"roleNames"`
	AdminScope string   `json:"adminScope"`
}

// AdminRole 是一个权限角色（权限组）。
type AdminRole struct {
	RoleID string `json:"roleId,omitempty"`
	// AdminScope 见本文件顶部常量：none / dept / dept_children / specified / all
	AdminScope string `json:"adminScope"`
	// ScopeDeptIDs 指定部门 ID 列表（字符串透传雪花 ID），仅 AdminScope=specified 时非空；
	// 其余档位保存时置空，控制面会清掉旧值。
	ScopeDeptIDs []string `json:"scopeDeptIds,omitempty"`
	RoleName     string   `json:"roleName"`
	Knowledge    bool     `json:"knowledge"`
	Meeting      bool     `json:"meeting"`
	Drive        bool     `json:"drive"`
	Shared       bool     `json:"shared"`
	Plugin       bool     `json:"plugin"`
	MemberCount  int      `json:"memberCount,omitempty"`
}

// AdminPermissionMembers 取当前管理员可见的成员矩阵（子管理员只拿到本部门）。
func (c *Client) AdminPermissionMembers(ctx context.Context, token string) ([]AdminMember, error) {
	var members []AdminMember
	if err := c.getJSON(ctx, "/easyshare/permission/admin/members", token, &members); err != nil {
		return nil, err
	}
	return members, nil
}

// AdminPermissionRoles 取全部权限角色（仅超管）。
func (c *Client) AdminPermissionRoles(ctx context.Context, token string) ([]AdminRole, error) {
	var roles []AdminRole
	if err := c.getJSON(ctx, "/easyshare/permission/admin/roles", token, &roles); err != nil {
		return nil, err
	}
	return roles, nil
}

// AdminSavePermissionRole 新建（RoleID 为空）或更新权限角色。
func (c *Client) AdminSavePermissionRole(ctx context.Context, token string, role AdminRole) error {
	role.RoleName = strings.TrimSpace(role.RoleName)
	if role.RoleName == "" {
		return fmt.Errorf("角色名不能为空")
	}
	return c.putValue(ctx, "/easyshare/permission/admin/roles", token, role, nil)
}

// AdminDeletePermissionRole 删除权限角色；只挂该角色的成员会落逐账号快照。
func (c *Client) AdminDeletePermissionRole(ctx context.Context, token, roleID string) error {
	if err := validateUserID(roleID); err != nil {
		return fmt.Errorf("角色标识非法")
	}
	return c.deleteJSON(ctx, "/easyshare/permission/admin/roles/"+roleID, token)
}

// AdminAssignRoles 整组替换某成员的角色；空列表 = 回到逐账号配置。
func (c *Client) AdminAssignRoles(ctx context.Context, token, userID string, roleIDs []string) error {
	if err := validateUserID(userID); err != nil {
		return err
	}
	ids := make([]string, 0, len(roleIDs))
	for _, id := range roleIDs {
		if err := validateUserID(id); err != nil {
			return fmt.Errorf("角色标识非法")
		}
		ids = append(ids, id)
	}
	body := map[string][]string{"roleIds": ids}
	return c.putValue(ctx, "/easyshare/permission/admin/members/"+userID+"/roles", token, body, nil)
}

// AdminSavePermissions 保存某账号的逐账号权限矩阵。
func (c *Client) AdminSavePermissions(ctx context.Context, token string, flags FeatureFlags) error {
	if flags.UserID == "" {
		return fmt.Errorf("缺少账号标识")
	}
	return c.putValue(ctx, "/easyshare/permission/admin", token, flags, nil)
}
