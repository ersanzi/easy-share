package account

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// 本文件是客户端「管理」页所需的控制面接口。
//
// 管理界面自绘在客户端内（沿用客户端色调），不跳 RuoYi 自带后台，因此这些接口由桌面端
// 直接调用。鉴权仍在控制面：JWT 是管理员的，接口才会放行——客户端的 isAdmin 只管入口显隐。

// registerUserConfigKey 是 RuoYi 控制「是否允许用户自助注册」的配置键。
const registerUserConfigKey = "sys.account.registerUser"

// ManagedUser 是管理页列表里的一行账号。
//
// UserID 用 string：RuoYi 的雪花 ID（如 1761100000000000001）超出 JS 安全整数范围，
// 控制面按字符串下发，客户端也必须按字符串透传，不能中途转数字。
type ManagedUser struct {
	UserID   string `json:"userId"`
	UserName string `json:"userName"`
	NickName string `json:"nickName"`
	DeptName string `json:"deptName"`
	// Status "0" 正常、"1" 停用（RuoYi 口径，保持原样不翻译为 bool，避免与其字典脱节）。
	Status     string `json:"status"`
	CreateTime string `json:"createTime"`
	LoginDate  string `json:"loginDate"`
}

// UserPage 是账号列表的一页。
type UserPage struct {
	Total int64         `json:"total"`
	Rows  []ManagedUser `json:"rows"`
}

// NewUser 是新建账号的入参。DeptID / RoleIDs 可空——控制面允许只给用户名、昵称、密码。
type NewUser struct {
	UserName string   `json:"userName"`
	NickName string   `json:"nickName"`
	Password string   `json:"password"`
	DeptID   string   `json:"deptId,omitempty"`
	RoleIDs  []string `json:"roleIds,omitempty"`
}

// ListUsers 分页取账号列表。
func (c *Client) ListUsers(ctx context.Context, token string, pageNum, pageSize int) (*UserPage, error) {
	if pageNum < 1 {
		pageNum = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	query := url.Values{}
	query.Set("pageNum", fmt.Sprint(pageNum))
	query.Set("pageSize", fmt.Sprint(pageSize))

	var page UserPage
	if err := c.getJSON(ctx, "/system/user/list?"+query.Encode(), token, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// CreateUser 新建账号。
func (c *Client) CreateUser(ctx context.Context, token string, user NewUser) error {
	user.UserName = strings.TrimSpace(user.UserName)
	user.NickName = strings.TrimSpace(user.NickName)
	if user.UserName == "" {
		return fmt.Errorf("账号名不能为空")
	}
	if user.Password == "" {
		return fmt.Errorf("密码不能为空")
	}
	if user.NickName == "" {
		// 昵称是列表与头像的展示来源，留空会显示为空白行，故回落为账号名。
		user.NickName = user.UserName
	}
	return c.postValue(ctx, "/system/user", token, user, nil)
}

// SetUserStatus 启用/停用账号。enabled=false 即 RuoYi 的 status="1"（停用）。
func (c *Client) SetUserStatus(ctx context.Context, token, userID string, enabled bool) error {
	if strings.TrimSpace(userID) == "" {
		return fmt.Errorf("缺少账号标识")
	}
	status := "1"
	if enabled {
		status = "0"
	}
	body := map[string]string{"userId": userID, "status": status}
	return c.putValue(ctx, "/system/user/changeStatus", token, body, nil)
}

// ResetUserPassword 重置指定账号的密码。
func (c *Client) ResetUserPassword(ctx context.Context, token, userID, password string) error {
	if strings.TrimSpace(userID) == "" {
		return fmt.Errorf("缺少账号标识")
	}
	if password == "" {
		return fmt.Errorf("新密码不能为空")
	}
	body := map[string]string{"userId": userID, "password": password}
	return c.putValue(ctx, "/system/user/resetPwd", token, body, nil)
}

// DeleteUser 删除账号。
func (c *Client) DeleteUser(ctx context.Context, token, userID string) error {
	// 只允许纯数字 ID：该值会拼进 URL 路径，防止越权拼出别的接口路径。
	if err := validateUserID(strings.TrimSpace(userID)); err != nil {
		return err
	}
	return c.deleteJSON(ctx, "/system/user/"+userID, token)
}

// RegisterEnabled 读「是否允许自助注册」开关。
func (c *Client) RegisterEnabled(ctx context.Context, token string) (bool, error) {
	// 该接口的 data 是字符串 "true"/"false"，不是布尔。
	var raw string
	if err := c.getJSON(ctx, "/system/config/configKey/"+registerUserConfigKey, token, &raw); err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(raw), "true"), nil
}

// SetRegisterEnabled 写「是否允许自助注册」开关。
//
// RuoYi 的配置更新要求整行数据，故先按 key 取回该行再改 configValue 回写。
func (c *Client) SetRegisterEnabled(ctx context.Context, token string, enabled bool) error {
	var row map[string]any
	query := url.Values{}
	query.Set("configKey", registerUserConfigKey)
	query.Set("pageNum", "1")
	query.Set("pageSize", "1")
	var page struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := c.getJSON(ctx, "/system/config/list?"+query.Encode(), token, &page); err != nil {
		return err
	}
	if len(page.Rows) == 0 {
		return fmt.Errorf("控制面未找到注册开关配置项 %s", registerUserConfigKey)
	}
	row = page.Rows[0]
	row["configValue"] = fmt.Sprint(enabled)
	return c.putValue(ctx, "/system/config", token, row, nil)
}
