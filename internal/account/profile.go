// Package account 的个人资料扩展：昵称、头像、改密。
//
// 昵称与密码走 RuoYi 标准个人中心接口（任何登录用户都有权改自己）；
// 头像走 EasyShare 自己的 /easyshare/profile（标准通道要求 system:oss:upload 权限，
// 普通成员没有）。见 deploy/ruoyi-db/easyshare-user-profile.sql。
package account

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// maxAvatarBytes 与服务端上限一致，提前拒绝省得白传 2MB。
const maxAvatarBytes = 2 * 1024 * 1024

// UpdateNickName 修改自己的昵称。控制面只更新非空字段，不会动邮箱/头像等其它列。
func (c *Client) UpdateNickName(ctx context.Context, token, nickName string) error {
	nickName = strings.TrimSpace(nickName)
	if nickName == "" {
		return fmt.Errorf("昵称不能为空")
	}
	if utf8.RuneCountInString(nickName) > 30 {
		return fmt.Errorf("昵称不能超过 30 个字符")
	}
	return c.putValue(ctx, "/system/user/profile", token, map[string]string{"nickName": nickName}, nil)
}

// ChangePassword 修改自己的登录口令。控制面会校验旧口令并拒绝「新旧相同」。
func (c *Client) ChangePassword(ctx context.Context, token, oldPassword, newPassword string) error {
	if oldPassword == "" || newPassword == "" {
		return fmt.Errorf("新旧密码都不能为空")
	}
	if oldPassword == newPassword {
		return fmt.Errorf("新密码不能与旧密码相同")
	}
	return c.putValue(ctx, "/system/user/profile/updatePwd", token, map[string]string{
		"oldPassword": oldPassword,
		"newPassword": newPassword,
	}, nil)
}

// ProfileAvatarURL 读自己的头像地址（相对控制面，空串表示没设置）。
func (c *Client) ProfileAvatarURL(ctx context.Context, token string) (string, error) {
	var out struct {
		AvatarURL string `json:"avatarUrl"`
	}
	if err := c.getJSON(ctx, "/easyshare/profile/mine", token, &out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.AvatarURL), nil
}

// UploadAvatar 上传本地图片作为头像，返回新的头像地址（相对控制面）。
func (c *Client) UploadAvatar(ctx context.Context, token, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("读取图片失败：%w", err)
	}
	if info.Size() > maxAvatarBytes {
		return "", fmt.Errorf("头像不能超过 2MB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取图片失败：%w", err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return "", fmt.Errorf("构造上传请求失败：%w", err)
	}
	if _, err := part.Write(data); err != nil {
		return "", fmt.Errorf("构造上传请求失败：%w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("构造上传请求失败：%w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/easyshare/profile/avatar", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.setAuth(req, token)
	var out struct {
		AvatarURL string `json:"avatarUrl"`
	}
	if err := c.do(req, token, &out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.AvatarURL), nil
}

// RemoveAvatar 清除自己的头像。
func (c *Client) RemoveAvatar(ctx context.Context, token string) error {
	return c.deleteJSON(ctx, "/easyshare/profile/avatar", token)
}
