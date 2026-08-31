// Package account 是 EasyShare 桌面端连接账号控制面（RuoYi-Vue-Plus）的客户端。
//
// 桌面端是控制面的客户端：用户名密码 → 控制面换 JWT，再用 JWT 取用户信息。
// 见 docs/adr/0007-account-control-plane-ruoyi.md。本包只做 P1（登录 + 用户信息），
// 不涉及存储授权（P2）。
package account

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// pcClientID 是 RuoYi 内置 PC 客户端标识（sys_client 表，grant_type 含 password）。
	pcClientID = "e5cd7e4891bf95d1d19206ce24a7b32e"
	grantType  = "password"
	// adminRoleKey / allPermissions 是 RuoYi 超级管理员的两个标志（sys_role.role_key 与通配权限）。
	adminRoleKey   = "superadmin"
	allPermissions = "*:*:*"
)

// User 是登录后展示所需的用户信息（P1 只用到昵称与头像）。
// 注意 RuoYi 把 Long 型 ID 序列化为字符串（避免 JS 精度丢失），故 UserID 用 string。
type User struct {
	UserID   string `json:"userId"`
	UserName string `json:"userName"`
	NickName string `json:"nickName"`
	Avatar   string `json:"avatar"`
}

// Session 是一次登录的结果：令牌 + 用户信息 + 角色/权限。JWT 由桌面端保管，用于后续请求。
//
// Roles/Permissions 只用于客户端的入口显隐（如「管理」按钮），**不是权限裁决**——
// 真正的鉴权在控制面（ADR-0007 不变量 3：Core 不做权限裁决）。前端拿到 isAdmin=false
// 只是看不到入口，即便伪造为 true，后台接口仍会按 Sa-Token 的真实角色拒绝。
type Session struct {
	Token       string   `json:"token"`
	ExpireIn    int64    `json:"expireIn"`
	User        User     `json:"user"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
}

// IsAdmin 判断该会话是否具备管理员身份。
//
// RuoYi 的口径：超级管理员角色 key 为 superadmin，其权限集为通配 "*:*:*"。
// 两者取或，避免只认角色名导致自定义管理角色被漏掉。
func (s *Session) IsAdmin() bool {
	if s == nil {
		return false
	}
	for _, role := range s.Roles {
		if role == adminRoleKey {
			return true
		}
	}
	for _, perm := range s.Permissions {
		if perm == allPermissions {
			return true
		}
	}
	return false
}

// Client 连接账号控制面。baseURL 形如 http://localhost:8090。
type Client struct {
	baseURL string
	http    *http.Client
}

// New 创建控制面客户端。baseURL 末尾多余斜杠会被去除。
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// ruoyiResp 是 RuoYi 统一响应包装。
type ruoyiResp struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// Login 用用户名密码登录，成功后返回带 JWT 与用户信息的 Session。
//
// 注意：控制面生产开启接口加密（api-decrypt），届时登录报文需加密；当前 P1 面向
// dev（加密关闭）走明文。加密适配在后续阶段处理。
func (c *Client) Login(ctx context.Context, username, password string) (*Session, error) {
	body, _ := json.Marshal(map[string]string{
		"clientId":  pcClientID,
		"grantType": grantType,
		"username":  username,
		"password":  password,
	})
	var loginData struct {
		AccessToken string `json:"access_token"`
		ExpireIn    int64  `json:"expire_in"`
	}
	if err := c.postJSON(ctx, "/auth/login", "", body, &loginData); err != nil {
		return nil, err
	}
	if loginData.AccessToken == "" {
		return nil, fmt.Errorf("登录未返回令牌")
	}

	info, err := c.userInfo(ctx, loginData.AccessToken)
	if err != nil {
		// 令牌拿到但取用户信息失败：仍返回会话，用户信息留空由上层降级展示。
		// 此时 Roles/Permissions 也为空，IsAdmin() 为 false——宁可少给入口，不可多给。
		return &Session{Token: loginData.AccessToken, ExpireIn: loginData.ExpireIn}, nil
	}
	return &Session{
		Token:       loginData.AccessToken,
		ExpireIn:    loginData.ExpireIn,
		User:        info.User,
		Roles:       info.Roles,
		Permissions: info.Permissions,
	}, nil
}

// Logout 通知控制面登出（失败不影响本地清除会话）。
func (c *Client) Logout(ctx context.Context, token string) error {
	return c.postJSON(ctx, "/auth/logout", token, nil, nil)
}

// userInfoResult 是 /system/user/getInfo 的 data 部分：用户 + 角色 key 列表 + 权限串列表。
type userInfoResult struct {
	User        User     `json:"user"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
}

// userInfo 用 JWT 取当前登录用户信息、角色与权限。
func (c *Client) userInfo(ctx context.Context, token string) (*userInfoResult, error) {
	var data userInfoResult
	if err := c.getJSON(ctx, "/system/user/getInfo", token, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// postJSON 发送 JSON POST，解 RuoYi 包装，成功时把 data 反序列化进 out（out 可为 nil）。
func (c *Client) postJSON(ctx context.Context, path, token string, body []byte, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req, token)
	return c.do(req, out)
}

// postValue 把任意值序列化为 JSON 后 POST。
func (c *Client) postValue(ctx context.Context, path, token string, value, out any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.postJSON(ctx, path, token, body, out)
}

// putValue 把任意值序列化为 JSON 后 PUT（RuoYi 的更新类接口一律 PUT）。
func (c *Client) putValue(ctx context.Context, path, token string, value, out any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req, token)
	return c.do(req, out)
}

// deleteJSON 发送 DELETE，解 RuoYi 包装。
func (c *Client) deleteJSON(ctx context.Context, path, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	c.setAuth(req, token)
	return c.do(req, nil)
}

// getJSON 发送 GET，解 RuoYi 包装。
func (c *Client) getJSON(ctx context.Context, path, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	c.setAuth(req, token)
	return c.do(req, out)
}

// setAuth 设置鉴权头。RuoYi(Sa-Token) 接受 Authorization: Bearer；clientid 头用于客户端识别。
func (c *Client) setAuth(req *http.Request, token string) {
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("clientid", pcClientID)
}

// do 执行请求并解析 RuoYi 统一响应；code != 200 视为业务错误，返回其 msg。
func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("连接账号服务失败：%w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var wrapped ruoyiResp
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return fmt.Errorf("账号服务响应异常(HTTP %d)", resp.StatusCode)
	}
	if wrapped.Code != 200 {
		msg := wrapped.Msg
		if msg == "" {
			msg = fmt.Sprintf("账号服务返回 code=%d", wrapped.Code)
		}
		return fmt.Errorf("%s", msg)
	}
	if out != nil && len(wrapped.Data) > 0 {
		if err := json.Unmarshal(wrapped.Data, out); err != nil {
			return fmt.Errorf("解析账号数据失败：%w", err)
		}
	}
	return nil
}
