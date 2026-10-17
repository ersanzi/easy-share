package drive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// ShareLink 是一条分享记录，供前端展示与撤销。
//
// ShareId 用字符串而不是 int64：雪花 ID 是 19 位，超出 JS 安全整数（2^53-1），
// 跨 Wails 边界会被静默截断，再传回来就查不到任何行——这与 RecycleEntry.FileID
// 踩过的是同一个坑（known-issues KI-21）。
type ShareLink struct {
	ShareId    string     `json:"shareId"`
	Token      string     `json:"token"`
	Url        string     `json:"url"`
	FileName   string     `json:"fileName"`
	FileSize   int64      `json:"fileSize"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	Revoked    bool       `json:"revoked"`
	VisitCount int        `json:"visitCount"`
	CreatedAt  *time.Time `json:"createdAt,omitempty"`
}

// shareResponse 是控制面的原始响应。
//
// ShareId 用 FlexInt64：RuoYi 对超范围的 Long 会序列化成字符串，未超范围仍是数字，
// 两种形态都要能接住（与 Object.FileId 同理）。
type shareResponse struct {
	ShareId    FlexInt64  `json:"shareId"`
	Token      string     `json:"token"`
	Url        string     `json:"url"`
	FileName   string     `json:"fileName"`
	FileSize   int64      `json:"fileSize"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	Revoked    bool       `json:"revoked"`
	VisitCount int        `json:"visitCount"`
	CreatedAt  *time.Time `json:"createdAt"`
}

func (r shareResponse) toLink() ShareLink {
	return ShareLink{
		ShareId:    strconv.FormatInt(r.ShareId.Int64(), 10),
		Token:      r.Token,
		Url:        r.Url,
		FileName:   r.FileName,
		FileSize:   r.FileSize,
		ExpiresAt:  r.ExpiresAt,
		Revoked:    r.Revoked,
		VisitCount: r.VisitCount,
		CreatedAt:  r.CreatedAt,
	}
}

// CreateShare 创建一条长效可撤销的分享。
//
// expiresHours <= 0 时由控制面取默认值（7 天），上限 30 天也由控制面截断——
// 客户端不做二次裁剪，避免两边规则分叉。
func (c *Client) CreateShare(ctx context.Context, token, space, path string, expiresHours int) (*ShareLink, error) {
	body, err := json.Marshal(map[string]any{
		"path": path, "expiresHours": expiresHours, "space": normalizeSpace(space),
	})
	if err != nil {
		return nil, err
	}
	var resp shareResponse
	if err := c.call(ctx, http.MethodPost, "/easyshare/drive/share", token, body, &resp); err != nil {
		return nil, fmt.Errorf("创建分享失败：%w", err)
	}
	link := resp.toLink()
	return &link, nil
}

// Shares 列出当前用户创建的分享（含已撤销与已过期）。
func (c *Client) Shares(ctx context.Context, token string) ([]ShareLink, error) {
	var resp []shareResponse
	if err := c.call(ctx, http.MethodGet, "/easyshare/drive/shares", token, nil, &resp); err != nil {
		return nil, fmt.Errorf("获取分享列表失败：%w", err)
	}
	links := make([]ShareLink, 0, len(resp))
	for _, item := range resp {
		links = append(links, item.toLink())
	}
	return links, nil
}

// RevokeShare 撤销一条分享。撤销立即生效，不等自然过期。
//
// shareID 是字符串形态的雪花 ID；解析失败直接报错，不做兜底猜测。
func (c *Client) RevokeShare(ctx context.Context, token, shareID string) (bool, error) {
	id, err := strconv.ParseInt(shareID, 10, 64)
	if err != nil {
		return false, fmt.Errorf("分享 ID 不合法：%w", err)
	}
	body, err := json.Marshal(map[string]int64{"shareId": id})
	if err != nil {
		return false, err
	}
	var ok bool
	if err := c.call(ctx, http.MethodPost, "/easyshare/drive/share/revoke", token, body, &ok); err != nil {
		return false, fmt.Errorf("撤销分享失败：%w", err)
	}
	return ok, nil
}
